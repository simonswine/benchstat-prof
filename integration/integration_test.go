// SPDX-License-Identifier: Apache-2.0
package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/simonswine/benchstat-prof/evidence"
)

// TestCLIIntegration builds and runs the actual binary against two commits,
// exercising forwarding, multiple packages, real captures, reports, and cleanup.
// Browser tests may supply a temporary artifact directory to retain the output.
func TestCLIIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("full CLI integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	if parent := os.Getenv("BENCHSTAT_PROF_INTEGRATION_DIR"); parent != "" {
		var err error
		root, err = os.MkdirTemp(parent, "cli-integration-")
		if err != nil {
			t.Fatal(err)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "benchstat-prof")
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", binary, "../cmd/benchstat-prof").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	repo := filepath.Join(root, "project with spaces")
	if err := os.MkdirAll(filepath.Join(repo, "child"), 0755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	git("init")
	write("go.mod", "module integrationfixture\n\ngo 1.24.0\n")
	write(".gitignore", ".benchstat-prof/\n")
	implementation := func(iterations string) string {
		return "package integrationfixture\nimport \"crypto/sha256\"\nvar Sink []byte\nfunc Work(size int) { p := make([]byte, size); for i := 0; i < " + iterations + "; i++ { sum := sha256.Sum256(p); copy(p, sum[:]) }; Sink = p }\n"
	}
	write("work.go", implementation("8"))
	write("bench_test.go", `//go:build perfbench

package integrationfixture
import (
 "flag"
 "testing"
)
var size = flag.Int("fixture-size", 8192, "workload size")
func BenchmarkWork(b *testing.B) { for i := 0; i < b.N; i++ { Work(*size) } }
`)
	write("child/work.go", "package child\n")
	write("child/bench_test.go", `//go:build perfbench

package child
import (
 "crypto/sha256"
 "flag"
 "testing"
)
var size = flag.Int("fixture-size", 8192, "workload size")
var sink []byte
func BenchmarkChild(b *testing.B) {
 for i := 0; i < b.N; i++ {
  p := make([]byte, *size)
  for j := 0; j < 8; j++ { sum := sha256.Sum256(p); copy(p, sum[:]) }
  sink = p
 }
}
`)
	commit := func(message string) {
		git("add", ".")
		git("-c", "user.name=Integration", "-c", "user.email=integration@example.com", "-c", "commit.gpgsign=false", "commit", "-m", message)
	}
	commit("baseline")
	write("work.go", implementation("4"))
	commit("comparison")
	if diff := git("diff", "HEAD~1", "HEAD", "--", "bench_test.go", "child/bench_test.go"); diff != "" {
		t.Fatalf("benchmark definitions changed: %s", diff)
	}
	invoke := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, binary, args...)
		// Exercise worktree-root discovery from a nested invocation directory.
		cmd.Dir = filepath.Join(repo, "child")
		return cmd.CombinedOutput()
	}
	forwarded := []string{"-tags=perfbench", "-count=2", "-benchtime=150ms", "-json", "./...", "-args", "-fixture-size=8192"}
	args := append([]string{"test", "--base", "HEAD~1", "--comparison", "HEAD", "--run-name", "full-integration", "--"}, forwarded...)
	out, err := invoke(args...)
	if err != nil {
		t.Fatalf("CLI: %v\n%s", err, out)
	}
	reports := filepath.Join(repo, ".benchstat-prof", "reports")
	runDir := filepath.Join(reports, "full-integration")
	for _, file := range []string{"report.html", "report-local.html", "benchstat.txt", "plan.yaml", "status.yaml"} {
		if data, err := os.ReadFile(filepath.Join(runDir, file)); err != nil || len(data) == 0 {
			t.Fatalf("missing %s: %v", file, err)
		}
	}
	for _, side := range []string{"baseline", "comparison"} {
		for _, stage := range []string{"go-test", "profile-go-test"} {
			data, err := os.ReadFile(filepath.Join(runDir, side, stage+".command.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			for _, arg := range forwarded {
				if !bytes.Contains(data, []byte(arg)) {
					t.Errorf("%s/%s did not forward %s", side, stage, arg)
				}
			}
		}
		for _, pkg := range []string{"integrationfixture", "integrationfixture/child"} {
			dir := filepath.Join(runDir, side, evidence.PackageKey(pkg))
			packageBench, err := os.ReadFile(filepath.Join(dir, "bench.txt"))
			if err != nil {
				t.Fatal(err)
			}
			for benchmark := range evidence.BenchmarkResults(packageBench) {
				profileDir := filepath.Join(runDir, side, evidence.BenchmarkKey(pkg, benchmark))
				for _, file := range []string{"cpu.pprof", "heap.pprof"} {
					data, err := os.ReadFile(filepath.Join(profileDir, file))
					if err != nil {
						t.Fatal(err)
					}
					p, err := profile.ParseData(data)
					if err != nil {
						t.Fatalf("%s/%s: %v", side, file, err)
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
						t.Fatalf("%s/%s/%s has no samples", side, pkg, file)
					}
				}
			}
			if info, err := os.Stat(filepath.Join(dir, "test.bin")); err != nil || info.Size() == 0 {
				t.Fatalf("test binary missing: %v", err)
			}
			bench, err := os.ReadFile(filepath.Join(dir, "bench.txt"))
			if err != nil || !bytes.Contains(bench, []byte("Benchmark")) {
				t.Fatalf("package timings missing: %v", err)
			}
		}
	}

	// The standalone document embeds exactly the same native profile bytes as
	// the web report serves, with no references to neighboring profile files.
	web := readReportData(t, filepath.Join(runDir, "report.html"))
	local := readReportData(t, filepath.Join(runDir, "report-local.html"))
	if len(web.Benchmarks) != 2 || len(local.Benchmarks) != 2 {
		t.Fatal("expected both packages in reports")
	}
	server := httptest.NewServer(http.FileServer(http.Dir(runDir)))
	defer server.Close()
	for bi, benchmark := range web.Benchmarks {
		for pi, p := range benchmark.Profiles {
			lp := local.Benchmarks[bi].Profiles[pi]
			for _, pair := range [][2]string{{p.BaselineFile, lp.BaselineFile}, {p.ComparisonFile, lp.ComparisonFile}} {
				if pair[0] == "" || strings.HasPrefix(pair[0], "data:") {
					t.Fatal("web report should link to pprof files")
				}
				raw, err := os.ReadFile(filepath.Join(runDir, filepath.FromSlash(pair[0])))
				if err != nil {
					t.Fatal(err)
				}
				const prefix = "data:application/octet-stream;base64,"
				if !strings.HasPrefix(pair[1], prefix) {
					t.Fatal("local report must embed every pprof")
				}
				embedded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(pair[1], prefix))
				if err != nil || !bytes.Equal(raw, embedded) {
					t.Fatal("local profile does not match original evidence")
				}
				response, err := http.Get(server.URL + "/" + pair[0])
				if err != nil {
					t.Fatal(err)
				}
				served, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(raw, served) {
					t.Fatal("web profile cannot be loaded")
				}
			}
		}
	}
	if status := git("status", "--porcelain"); status != "" {
		t.Fatalf("source modified: %s", status)
	}
	if trees := git("worktree", "list", "--porcelain"); strings.Count(trees, "worktree ") != 1 {
		t.Fatalf("leaked worktrees: %s", trees)
	}
	if _, err := invoke(args...); err == nil {
		t.Fatal("existing run must not be overwritten")
	}
	// Keep the successful report available to the browser suite.
	if manifest := os.Getenv("BENCHSTAT_PROF_INTEGRATION_MANIFEST"); manifest != "" {
		if err := os.WriteFile(manifest, []byte(runDir), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A failed comparison still retains logs and both reports.
	write("broken.go", "this is not valid Go\n")
	failedArgs := []string{"test", "--base", "HEAD", "--comparison", "path:" + repo, "--run-name", "failed-integration", "--profiles=false", "--", "-tags=perfbench", "-count=1", "-benchtime=1x", "./..."}
	if _, err := invoke(failedArgs...); err == nil {
		t.Fatal("broken comparison unexpectedly succeeded")
	}
	for _, file := range []string{"report.html", "report-local.html"} {
		data := readReportData(t, filepath.Join(reports, "failed-integration", file))
		if !strings.Contains(data.Warning, "Run failed:") {
			t.Fatal("failed report missing warning")
		}
	}
	if trees := git("worktree", "list", "--porcelain"); strings.Count(trees, "worktree ") != 1 {
		t.Fatal("failed run leaked worktree")
	}
}

// Decode the report's public JSON schema rather than depending on renderer internals.
type reportData struct {
	Benchmarks []struct {
		Profiles []struct{ BaselineFile, ComparisonFile string }
		Metrics  []struct {
			Key, Scope                     string
			BaselineValue, ComparisonValue float64
		}
	}
	Warning string
}

func readReportData(t *testing.T, path string) reportData {
	t.Helper()
	html, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const start = `<script type="application/json" id="report-data">`
	_, tail, ok := strings.Cut(string(html), start)
	if !ok {
		t.Fatal("report metadata not found")
	}
	raw, _, ok := strings.Cut(tail, "</script>")
	if !ok {
		t.Fatal("report metadata is unterminated")
	}
	var data reportData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	return data
}
