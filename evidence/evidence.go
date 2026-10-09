// SPDX-License-Identifier: Apache-2.0
// Package evidence manages run metadata, artifact paths, and benchmark output.
package evidence

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Revision identifies a commit or local directory target.
type Revision struct {
	Ref    string `yaml:"ref" json:"ref"`
	Commit string `yaml:"commit" json:"commit,omitempty"`
	Branch string `yaml:"branch,omitempty" json:"branch,omitempty"`
	Dirty  *bool  `yaml:"dirty,omitempty" json:"dirty,omitempty"`
}

// WorktreeStatus distinguishes known clean/dirty targets from unavailable Git status.
func (r Revision) WorktreeStatus() string {
	if r.Dirty == nil {
		return "Unavailable"
	}
	if *r.Dirty {
		return "Dirty"
	}
	return "Clean"
}

// Plan records the inputs and environment for a benchmark run.
type Plan struct {
	Baseline   Revision `yaml:"baseline" json:"baseline"`
	Comparison Revision `yaml:"comparison" json:"comparison"`
	Arguments  []string `yaml:"arguments" json:"arguments"`
	Profiles   bool     `yaml:"profiles" json:"profiles"`
	GoVersion  string   `yaml:"go_version" json:"go_version"`
	Host       string   `yaml:"host" json:"host"`
}

// Status records the run's lifetime and any execution failure.
type Status struct {
	StartedAt  time.Time `yaml:"started_at"`
	FinishedAt time.Time `yaml:"finished_at"`
	Error      string    `yaml:"error,omitempty"`
}

var runNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,119}$`)
var packageNamePattern = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// ValidRunName reports whether name is safe as a single run-directory component.
func ValidRunName(name string) bool { return runNamePattern.MatchString(name) }

// NewRunName generates a UTC datetime–performance-engineering verb–animal name.
func NewRunName(now time.Time) (string, error) {
	verbs := []string{"tuning", "profiling", "optimizing", "measuring", "tracing", "sampling", "accelerating", "benchmarking"}
	animals := []string{"otter", "falcon", "badger", "lynx", "heron", "panda", "ibis", "fox", "orca", "yak", "gecko", "wombat", "lemur", "bison", "owl", "marten"}
	var random [2]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%s", now.UTC().Format("20060102-150405.000000000Z"), verbs[int(random[0])%len(verbs)], animals[int(random[1])%len(animals)]), nil
}

// ReportsRoot finds the reports directory at the repository's worktree root.
// Outside Git, it uses repo itself, which must be an absolute path.
func ReportsRoot(repo string) string {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return filepath.Join(strings.TrimSpace(string(out)), ".benchstat-prof", "reports")
	}
	return filepath.Join(repo, ".benchstat-prof", "reports")
}

// PackageKey maps an import path to a stable, filesystem-safe evidence directory.
func PackageKey(name string) string {
	sum := sha256.Sum256([]byte(name))
	slug := packageNamePattern.ReplaceAllString(filepath.Base(name), "_")
	if len(slug) > 80 {
		slug = slug[:80]
	}
	return fmt.Sprintf("%s-%x", slug, sum[:8])
}

// WriteYAML saves metadata with owner-only permissions.
func WriteYAML(path string, value any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// NormalizeTestOutput decodes go test -json Output fields for benchmark analysis.
// Raw stdout is retained separately as evidence by the benchmark runner.
func NormalizeTestOutput(raw []byte) []byte {
	var out strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		var event struct{ Action, Output string }
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &event) == nil && event.Action != "" {
			out.WriteString(event.Output)
		} else if line != "" {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return []byte(out.String())
}

// SavePackageResults splits normalized output into per-package evidence files.
func SavePackageResults(side string, data []byte) error {
	var header strings.Builder
	var current string
	blocks := map[string]*strings.Builder{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "pkg: ") {
			current = strings.TrimPrefix(line, "pkg: ")
			if blocks[current] == nil {
				blocks[current] = &strings.Builder{}
			}
			blocks[current].WriteString(header.String())
			blocks[current].WriteString(line + "\n")
		} else if strings.HasPrefix(line, "goos: ") || strings.HasPrefix(line, "goarch: ") {
			header.WriteString(line + "\n")
		} else if current != "" {
			blocks[current].WriteString(line + "\n")
		}
	}
	for pkg, block := range blocks {
		dir := filepath.Join(side, PackageKey(pkg))
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "bench.txt"), []byte(block.String()), 0600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "package.txt"), []byte(pkg), 0600); err != nil {
			return err
		}
		for name, results := range BenchmarkResults([]byte(block.String())) {
			benchmarkDir := filepath.Join(side, BenchmarkKey(pkg, name))
			if err := os.MkdirAll(benchmarkDir, 0700); err != nil {
				return err
			}
			for file, contents := range map[string][]byte{"bench.txt": results, "package.txt": []byte(pkg), "benchmark.txt": []byte(name)} {
				if err := os.WriteFile(filepath.Join(benchmarkDir, file), contents, 0600); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// BenchmarkKey identifies an individual benchmark, including its CPU suffix.
func BenchmarkKey(pkg, name string) string { return PackageKey(pkg + "\x00" + name) }

// BenchmarkResults groups repeated measurements of each exact benchmark name.
func BenchmarkResults(data []byte) map[string][]byte {
	blocks := map[string][]byte{}
	var header []byte
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "goos: ") || strings.HasPrefix(line, "goarch: ") || strings.HasPrefix(line, "pkg: ") || strings.HasPrefix(line, "cpu: ") {
			header = append(header, []byte(line+"\n")...)
		}
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasPrefix(fields[0], "Benchmark") {
			continue
		}
		name := strings.TrimPrefix(fields[0], "Benchmark")
		if _, exists := blocks[name]; !exists {
			blocks[name] = append([]byte(nil), header...)
		}
		blocks[name] = append(blocks[name], []byte(line+"\n")...)
	}
	return blocks
}

// HostDescription identifies the machine and platform in run metadata.
func HostDescription() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s (%s/%s)", host, runtime.GOOS, runtime.GOARCH)
}
