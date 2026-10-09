// SPDX-License-Identifier: Apache-2.0
package benchmark

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTargetMetadata(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	git("init", "-b", "fixture")
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("initial"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", "file")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-m", "initial")
	sha, err := resolveCommit(ctx, dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	r := describeTarget(ctx, "path:"+dir, target{dir: dir})
	if r.Commit != sha || r.Branch != "fixture" || r.Dirty == nil || *r.Dirty {
		t.Fatalf("incorrect clean metadata: %+v", r)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".benchstat-prof", "reports"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".benchstat-prof", "reports", "evidence"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	r = describeTarget(ctx, "path:"+dir, target{dir: dir})
	if r.Dirty == nil || *r.Dirty {
		t.Fatal("report evidence should not mark source dirty")
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}
	r = describeTarget(ctx, "path:"+dir, target{dir: dir})
	if r.Dirty == nil || !*r.Dirty {
		t.Fatal("modified tracked file should be dirty")
	}
	git("add", "file")
	r = describeTarget(ctx, "path:"+dir, target{dir: dir})
	if r.Dirty == nil || !*r.Dirty {
		t.Fatal("staged changes should be dirty")
	}
	git("reset", "--hard", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "untracked"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	r = describeTarget(ctx, "path:"+dir, target{dir: dir})
	if r.Dirty == nil || !*r.Dirty {
		t.Fatal("untracked files should be dirty")
	}
	git("checkout", "--detach", "HEAD")
	r = describeTarget(ctx, "path:"+dir, target{dir: dir})
	if r.Commit != sha || r.Branch != "" {
		t.Fatalf("incorrect detached metadata: %+v", r)
	}
	r = describeTarget(ctx, "HEAD", target{sha: sha})
	if r.Commit != sha || r.Dirty == nil || *r.Dirty {
		t.Fatal("commit target should be clean")
	}
	r = describeTarget(ctx, "path:non-git", target{dir: t.TempDir()})
	if r.Commit != "" || r.Branch != "" || r.Dirty != nil {
		t.Fatal("non-Git target should have unavailable metadata")
	}
}
