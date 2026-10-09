package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/simonswine/benchstat-prof/evidence"
	"github.com/simonswine/benchstat-prof/profile"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == profile.ExecCommand {
		if err := Run(context.Background(), os.Args[1:]); err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"unknown"}, {"test"}, {"test", "--base", "HEAD", "./..."}, {"publish"}, {"publish", "one", "two"}} {
		if err := Run(context.Background(), args); err == nil {
			t.Fatalf("Run(%v) succeeded unexpectedly", args)
		}
	}
}

func TestBenchmarkCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	git("init")
	for name, content := range map[string]string{
		"go.mod":        "module fixture\n\ngo 1.24.0\n",
		".gitignore":    ".benchstat-prof/\n",
		"bench_test.go": "package fixture\nimport \"testing\"\nfunc BenchmarkExample(b *testing.B) { for i := 0; i < b.N; i++ {} }\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(repo, "child"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "child", "bench_test.go"), []byte("package child\nimport \"testing\"\nfunc BenchmarkChild(b *testing.B) { for i := 0; i < b.N; i++ {} }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	output := filepath.Join(repo, ".benchstat-prof", "reports", "integration")
	err := Run(context.Background(), []string{"test", "--repo", repo, "--run-name", "integration", "--base", "HEAD", "--comparison", "HEAD", "--", "-count=1", "-benchtime=1x", "./..."})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"baseline", "comparison"} {
		data, err := os.ReadFile(filepath.Join(output, name, "bench.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "BenchmarkExample") {
			t.Fatalf("missing benchmark in %s: %s", name, data)
		}
	}
	for _, side := range []string{"baseline", "comparison"} {
		for _, pkg := range []string{"fixture", "fixture/child"} {
			for _, file := range []string{"bench.txt", "test.bin"} {
				info, err := os.Stat(filepath.Join(output, side, evidence.PackageKey(pkg), file))
				if err != nil || info.Size() == 0 {
					t.Fatalf("missing evidence %s/%s/%s: %v", side, pkg, file, err)
				}
			}
			data, err := os.ReadFile(filepath.Join(output, side, evidence.PackageKey(pkg), "bench.txt"))
			if err != nil {
				t.Fatal(err)
			}
			for name := range evidence.BenchmarkResults(data) {
				for _, file := range []string{"cpu.pprof", "heap.pprof", "profile-command.json"} {
					if _, err := os.Stat(filepath.Join(output, side, evidence.BenchmarkKey(pkg, name), file)); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	for _, file := range []string{"report.html", "report-local.html", "plan.yaml", "status.yaml", "benchstat.txt"} {
		if _, err := os.Stat(filepath.Join(output, file)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(output, "report.assets")); !os.IsNotExist(err) {
		t.Fatalf("unexpected generated profile JSON directory: %v", err)
	}
	html, err := os.ReadFile(filepath.Join(output, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"baselineFile", "comparisonFile", "Download baseline pprof", "Download comparison pprof", "cpu.pprof"} {
		if !strings.Contains(string(html), want) {
			t.Errorf("report missing %q", want)
		}
	}
	if worktrees := git("worktree", "list", "--porcelain"); strings.Count(worktrees, "worktree ") != 1 {
		t.Fatalf("leaked worktrees: %s", worktrees)
	}
	if status := git("status", "--porcelain"); status != "" {
		t.Fatalf("repository modified: %s", status)
	}
	// The default comparison uses the current directory, including dirty files.
	t.Chdir(repo)
	if err := os.WriteFile(filepath.Join(repo, "dirty_test.go"), []byte("package fixture\nimport \"testing\"\nfunc BenchmarkDirty(b *testing.B) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pathOutput := filepath.Join(repo, ".benchstat-prof", "reports", "local")
	if err := Run(context.Background(), []string{"test", "--base", "HEAD", "--run-name", "local", "--profiles=false", "--", "-count=1", "-benchtime=1x", "-json"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(pathOutput, "comparison", "bench.txt"))
	if err != nil || !strings.Contains(string(data), "BenchmarkDirty") {
		t.Fatalf("local comparison did not include dirty benchmark: %s, %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(pathOutput, "baseline", "bench.txt"))
	if err != nil || strings.Contains(string(data), "BenchmarkDirty") {
		t.Fatalf("commit base included dirty benchmark: %s, %v", data, err)
	}
}
