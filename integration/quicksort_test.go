// SPDX-License-Identifier: Apache-2.0
package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/simonswine/benchstat-prof/evidence"
)

// TestQuickSortDirectories compares two real directory targets, with identical
// benchmark code and deterministic inputs but different quicksort implementations.
func TestQuickSortDirectories(t *testing.T) {
	if testing.Short() {
		t.Skip("quicksort CLI integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	if parent := os.Getenv("BENCHSTAT_PROF_INTEGRATION_DIR"); parent != "" {
		var err error
		root, err = os.MkdirTemp(parent, "quicksort-integration-")
		if err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(root, "baseline")
	comparison := filepath.Join(root, "comparison")
	for _, side := range []string{"baseline", "comparison"} {
		source := filepath.Join("testdata", "quicksort", side)
		destination := filepath.Join(root, side)
		if err := os.Mkdir(destination, 0755); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"go.mod", "quicksort.go", "quicksort_test.go"} {
			data, err := os.ReadFile(filepath.Join(source, file))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(destination, file), data, 0644); err != nil {
				t.Fatal(err)
			}
		}
		// Check correctness independently; timings/profiles only run benchmarks.
		cmd := exec.CommandContext(ctx, "go", "test", "-run=^TestQuickSort$", "-count=1")
		cmd.Dir = destination
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s correctness: %v\n%s", side, err, out)
		}
	}
	baseBench, err := os.ReadFile(filepath.Join(baseline, "quicksort_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	compBench, err := os.ReadFile(filepath.Join(comparison, "quicksort_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(baseBench, compBench) {
		t.Fatal("baseline and comparison benchmarks must be byte-identical")
	}
	binary := filepath.Join(root, "benchstat-prof")
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", binary, "../cmd/benchstat-prof").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	cmd := exec.CommandContext(ctx, binary, "test", "--repo", root,
		"--base", "path:"+baseline, "--comparison", "path:"+comparison,
		"--run-name", "quicksort", "--", "-bench=BenchmarkQuickSort", "-count=2", "-benchtime=100ms", "-cpu=1,2", "./...")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compare quicksort directories: %v\n%s", err, out)
	}
	runDir := filepath.Join(root, ".benchstat-prof", "reports", "quicksort")
	for _, side := range []string{"baseline", "comparison"} {
		packageDir := filepath.Join(runDir, side, evidence.PackageKey("quicksortfixture"))
		bench, err := os.ReadFile(filepath.Join(packageDir, "bench.txt"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"random", "sorted", "reverse", "duplicates"} {
			if !bytes.Contains(bench, []byte("BenchmarkQuickSort/"+name)) {
				t.Errorf("%s missing %s workload", side, name)
			}
		}
		for _, line := range strings.Split(string(bench), "\n") {
			if strings.HasPrefix(line, "BenchmarkQuickSort/") {
				fields := strings.Fields(line)
				found := false
				for i := 2; i+1 < len(fields); i += 2 {
					if fields[i+1] == "items/op" {
						found = true
						if fields[i] != "2048" {
							t.Errorf("%s: unexpected items/op: %s", side, fields[i])
						}
					}
				}
				if !found {
					t.Errorf("%s: custom metric missing: %s", side, line)
				}
			}
		}
		cases := evidence.BenchmarkResults(bench)
		if len(cases) != 8 {
			t.Fatalf("expected eight benchmark/CPU cases, got %d", len(cases))
		}
		for benchmark := range cases {
			profileDir := filepath.Join(runDir, side, evidence.BenchmarkKey("quicksortfixture", benchmark))
			capture, err := os.ReadFile(filepath.Join(profileDir, "profile-bench.txt"))
			if err != nil {
				t.Fatal(err)
			}
			captured := evidence.BenchmarkResults(capture)
			if len(captured) != 1 || captured[benchmark] == nil {
				t.Fatalf("profile mixes benchmarks: %s", capture)
			}
			for _, file := range []string{"cpu.pprof", "heap.pprof"} {
				raw, err := os.ReadFile(filepath.Join(profileDir, file))
				if err != nil {
					t.Fatal(err)
				}
				p, err := profile.ParseData(raw)
				if err != nil {
					t.Fatal(err)
				}
				if err := p.CheckValid(); err != nil {
					t.Fatal(err)
				}
				name := "cpu"
				if file == "heap.pprof" {
					name = "alloc_space"
				}
				index, err := p.SampleIndexByName(name)
				if err != nil {
					t.Fatal(err)
				}
				var total int64
				for _, sample := range p.Sample {
					total += sample.Value[index]
				}
				if total <= 0 {
					t.Fatalf("%s/%s has no samples", side, file)
				}
			}
		}
	}
	stats, err := os.ReadFile(filepath.Join(runDir, "benchstat.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stats, []byte("items/op")) {
		t.Fatal("benchstat missing custom metric")
	}
	for _, name := range []string{"random", "sorted", "reverse", "duplicates"} {
		if !bytes.Contains(stats, []byte("QuickSort/"+name)) {
			t.Errorf("benchstat missing %s", name)
		}
	}
	for _, file := range []string{"report.html", "report-local.html"} {
		data := readReportData(t, filepath.Join(runDir, file))
		if len(data.Benchmarks) != 8 {
			t.Fatal("expected eight individual benchmark/CPU reports")
		}
		customMetrics := 0
		for _, benchmark := range data.Benchmarks {
			for _, metric := range benchmark.Metrics {
				if metric.Key == "items/op" {
					customMetrics++
					if metric.BaselineValue != 2048 || metric.ComparisonValue != 2048 {
						t.Errorf("%s: custom metric not preserved: %+v", file, metric)
					}
				}
			}
		}
		if customMetrics != 8 {
			t.Errorf("%s: expected 8 custom metric rows, got %d", file, customMetrics)
		}
		if data.Warning != "" {
			t.Fatalf("report failed: %s", data.Warning)
		}
		for _, benchmark := range data.Benchmarks {
			if len(benchmark.Profiles) != 2 {
				t.Fatal("expected CPU and allocation profiles")
			}
			for _, p := range benchmark.Profiles {
				for _, path := range []string{p.BaselineFile, p.ComparisonFile} {
					if path == "" {
						t.Fatal("report missing profile")
					}
					if embedded := strings.HasPrefix(path, "data:"); embedded != (file == "report-local.html") {
						t.Fatalf("unexpected profile link in %s", file)
					}
				}
			}
		}
	}
	if manifest := os.Getenv("BENCHSTAT_PROF_INTEGRATION_MANIFEST"); manifest != "" {
		if err := os.WriteFile(manifest, []byte(runDir), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
