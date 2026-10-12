// SPDX-License-Identifier: Apache-2.0
package benchmark

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPathTargetsWithoutGit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	repo := t.TempDir()
	if _, err := worktreeRelativeDir(ctx, repo); err == nil {
		t.Fatal("test fixture must be outside a Git worktree")
	}
	for name, contents := range map[string]string{
		"go.mod":          "module example.com/pathfixture\n\ngo 1.20\n",
		"fixture_test.go": "package pathfixture\nimport \"testing\"\nfunc BenchmarkFixture(b *testing.B) { for i := 0; i < b.N; i++ {} }\n",
	} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	err := Run(ctx, Options{Repo: repo, RunName: "path-only", TestArgs: []string{"-count=1", "-benchtime=1x"}}, "path:"+repo, "path:"+repo)
	if err != nil {
		t.Fatalf("path-only run outside Git: %v", err)
	}
	for _, side := range []string{"baseline", "comparison"} {
		data, err := os.ReadFile(filepath.Join(repo, ".benchstat-prof", "reports", "path-only", side, "bench.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "BenchmarkFixture") {
			t.Fatalf("%s missing benchmark results: %s", side, data)
		}
	}
}

func TestWorktreeRelativeDir(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	nested := filepath.Join(repo, "nested", "package")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{repo: ".", nested: filepath.Join("nested", "package")} {
		got, err := worktreeRelativeDir(ctx, dir)
		if err != nil || got != want {
			t.Fatalf("relative directory for %s = %q, %v; want %q", dir, got, err, want)
		}
	}
	if err := Run(ctx, Options{Repo: t.TempDir()}, "HEAD", "HEAD"); err == nil || !strings.Contains(err.Error(), "not in a Git worktree") {
		t.Fatalf("expected worktree error, got %v", err)
	}
}
