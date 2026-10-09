// SPDX-License-Identifier: Apache-2.0
// Package profile captures individual benchmark CPU/memory profiles and test binaries.
package profile

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/simonswine/benchstat-prof/evidence"
)

// ExecCommand identifies the private profiling subprocess invocation.
const ExecCommand = "__benchstat_prof_profile_exec"

// Execute is invoked by go test -exec once per package. Measured benchmark
// names drive separate binary executions, isolating sub-benchmarks and CPUs.
func Execute(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("profile helper requires evidence directory and test binary")
	}
	root, binary := args[0], args[1]
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		return fmt.Errorf("read test binary metadata: %w", err)
	}
	pkg := strings.TrimSuffix(info.Path, ".test")
	if pkg == "" {
		pkg = filepath.Base(binary)
	}
	packageDir := filepath.Join(root, evidence.PackageKey(pkg))
	results, err := os.ReadFile(filepath.Join(packageDir, "bench.txt"))
	if os.IsNotExist(err) {
		return nil
	} // Packages without measured benchmarks.
	if err != nil {
		return err
	}
	benchmarks := evidence.BenchmarkResults(results)
	if len(benchmarks) == 0 {
		return nil
	}
	if err := copyBinary(binary, filepath.Join(packageDir, "test.bin")); err != nil {
		return err
	}
	var names []string
	for name := range benchmarks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Join(root, evidence.BenchmarkKey(pkg, name))
		argv := benchmarkArguments(args[2:], name, dir)
		fmt.Fprintf(os.Stderr, "Profiling %s / %s\n", pkg, name)
		if err := capture(ctx, binary, pkg, name, dir, argv); err != nil {
			return err
		}
	}
	return nil
}

var cpuSuffix = regexp.MustCompile(`-(\d+)$`)

// benchmarkArguments replaces only the flags that must be isolated. All other
// benchmark flags and custom test-binary arguments retain their original order.
func benchmarkArguments(args []string, name, dir string) []string {
	base := name
	cpu := "1"
	if match := cpuSuffix.FindStringSubmatchIndex(name); match != nil {
		base, cpu = name[:match[0]], name[match[2]:match[3]]
	}
	parts := strings.Split("Benchmark"+base, "/")
	for i := range parts {
		parts[i] = "^" + regexp.QuoteMeta(parts[i]) + "$"
	}
	out := []string{
		"-test.run=^$", "-test.bench=" + strings.Join(parts, "/"),
		"-test.cpuprofile=" + filepath.Join(dir, "cpu.pprof"),
		"-test.memprofile=" + filepath.Join(dir, "heap.pprof"),
	}
	if cpu != "" {
		out = append(out, "-test.cpu="+cpu)
	}
	controlled := map[string]bool{"-test.run": true, "-test.bench": true, "-test.cpu": cpu != "", "-test.cpuprofile": true, "-test.memprofile": true}
	for i := 0; i < len(args); i++ {
		key, _, hasValue := strings.Cut(args[i], "=")
		if controlled[key] {
			if !hasValue && i+1 < len(args) {
				i++
			}
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func capture(ctx context.Context, binary, pkg, name, dir string, argv []string) error {
	metadata, err := json.MarshalIndent(struct {
		Package, Benchmark string
		Arguments          []string
	}{pkg, name, argv}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "profile-command.json"), metadata, 0600); err != nil {
		return err
	}
	stdout, err := os.Create(filepath.Join(dir, "profile-bench.txt"))
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(dir, "profile-stderr.txt"))
	if err != nil {
		return err
	}
	defer stderr.Close()
	cmd := exec.CommandContext(ctx, binary, argv...)
	cmd.Stdout = io.MultiWriter(os.Stdout, stdout)
	cmd.Stderr = io.MultiWriter(os.Stderr, stderr)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("profile %s / %s: %w", pkg, name, err)
	}
	return nil
}

func copyBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func execWord(word string) (string, error) {
	if !strings.Contains(word, "'") {
		return "'" + word + "'", nil
	}
	if !strings.Contains(word, `"`) {
		return `"` + word + `"`, nil
	}
	return "", fmt.Errorf("profile executor path contains both quote characters: %q", word)
}

// Executor constructs go test's -exec command for individual benchmark profiling.
func Executor(dir string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	self, err = execWord(self)
	if err != nil {
		return "", err
	}
	dir, err = execWord(dir)
	if err != nil {
		return "", err
	}
	return self + " " + ExecCommand + " " + dir, nil
}
