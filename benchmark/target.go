// SPDX-License-Identifier: Apache-2.0
package benchmark

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/simonswine/benchstat-prof/evidence"
)

type target struct{ dir, sha string }

// describeTarget captures Git metadata without changing which directory is run.
// Git status is best-effort for directory targets and ignores our own evidence.
func describeTarget(ctx context.Context, ref string, t target) evidence.Revision {
	r := evidence.Revision{Ref: ref, Commit: t.sha}
	if t.sha != "" {
		clean := false
		r.Dirty = &clean // Detached commit targets start as clean worktrees.
		return r
	}
	git := func(args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "git", append([]string{"-C", t.dir}, args...)...).Output()
	}
	if out, err := git("rev-parse", "--verify", "HEAD^{commit}"); err == nil {
		r.Commit = strings.TrimSpace(string(out))
	}
	if out, err := git("symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		r.Branch = strings.TrimSpace(string(out))
	}
	if out, err := git("status", "--porcelain", "-z", "--untracked-files=all", "--", ":(top)**", ":(top,exclude).benchstat-prof"); err == nil {
		dirty := len(out) > 0
		r.Dirty = &dirty
	}
	return r
}

func resolveCommit(ctx context.Context, repo, ref string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve commit %q: %w: %s", ref, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func resolveTarget(ctx context.Context, repo, ref string) (target, error) {
	if strings.HasPrefix(ref, "path:") {
		path := strings.TrimPrefix(ref, "path:")
		if path == "" {
			return target{}, fmt.Errorf("path target must specify a directory")
		}
		dir, err := filepath.Abs(path)
		if err != nil {
			return target{}, err
		}
		info, err := os.Stat(dir)
		if err != nil {
			return target{}, err
		}
		if !info.IsDir() {
			return target{}, fmt.Errorf("%s is not a directory", dir)
		}
		return target{dir: dir}, nil
	}
	if ref == "" {
		return target{}, fmt.Errorf("commit reference must not be empty")
	}
	rel, err := worktreeRelativeDir(ctx, repo)
	if err != nil {
		return target{}, err
	}
	sha, err := resolveCommit(ctx, repo, ref)
	return target{dir: rel, sha: sha}, err
}

// worktreeRelativeDir preserves the invocation directory in detached worktrees.
func worktreeRelativeDir(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s is not in a Git worktree: %w: %s", dir, err, out)
	}
	root := strings.TrimSpace(string(out))
	// Resolve symlinks on both sides (notably macOS /var versus /private/var).
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	return filepath.Rel(root, dir)
}
