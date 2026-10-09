// SPDX-License-Identifier: Apache-2.0
// Package benchmark executes comparable measurements in commits or directories.
package benchmark

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/simonswine/benchstat-prof/benchstat"
	"github.com/simonswine/benchstat-prof/evidence"
	profiling "github.com/simonswine/benchstat-prof/profile"
	"github.com/simonswine/benchstat-prof/report"
)

// Options controls benchmark execution and retained evidence.
type Options struct {
	Repo, RunName string
	TestArgs      []string
	Profiles      bool
}

// Run benchmarks base and comparison, then writes both report formats.
// Targets are Git references or path:<directory>. TestArgs are passed unchanged.
func Run(ctx context.Context, opts Options, base, comparison string) (err error) {
	repo, err := filepath.Abs(opts.Repo)
	if err != nil {
		return err
	}
	baseTarget, err := resolveTarget(ctx, repo, base)
	if err != nil {
		return fmt.Errorf("base: %w", err)
	}
	comparisonTarget, err := resolveTarget(ctx, repo, comparison)
	if err != nil {
		return fmt.Errorf("comparison: %w", err)
	}
	baseRevision := describeTarget(ctx, base, baseTarget)
	comparisonRevision := describeTarget(ctx, comparison, comparisonTarget)
	started := time.Now()
	if opts.RunName == "" {
		opts.RunName, err = evidence.NewRunName(started)
		if err != nil {
			return err
		}
	}
	if !evidence.ValidRunName(opts.RunName) {
		return fmt.Errorf("invalid run name: use 1–120 letters, digits, dots, underscores or hyphens, starting with a letter or digit")
	}
	root := evidence.ReportsRoot(repo)
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	output := filepath.Join(root, opts.RunName)
	if err := os.Mkdir(output, 0700); err != nil {
		return fmt.Errorf("create run directory: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Run: %s\nEvidence: %s\n", opts.RunName, output)
	status := evidence.Status{StartedAt: started}
	// Retain evidence and generate reports even when execution fails.
	defer func() {
		status.FinishedAt = time.Now()
		if err != nil {
			status.Error = err.Error()
		}
		err = errors.Join(err, evidence.WriteYAML(filepath.Join(output, "status.yaml"), status))
		err = errors.Join(err, report.WriteRun(output))
		fmt.Fprintf(os.Stderr, "Web report: %s\nLocal report: %s\n", filepath.Join(output, "report.html"), filepath.Join(output, "report-local.html"))
	}()
	goVersion, versionErr := exec.CommandContext(ctx, "go", "version").Output()
	if versionErr != nil {
		return versionErr
	}
	plan := evidence.Plan{
		Baseline:   baseRevision,
		Comparison: comparisonRevision,
		Arguments:  opts.TestArgs, Profiles: opts.Profiles,
		GoVersion: strings.TrimSpace(string(goVersion)), Host: evidence.HostDescription(),
	}
	if err := evidence.WriteYAML(filepath.Join(output, "plan.yaml"), plan); err != nil {
		return err
	}
	if err := evidence.WriteYAML(filepath.Join(output, "status.yaml"), status); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "cpu.txt"), []byte(fmt.Sprintf("CPU(s): %d\n", runtime.NumCPU())), 0600); err != nil {
		return err
	}
	for _, revision := range []struct {
		label  string
		target target
	}{{"baseline", baseTarget}, {"comparison", comparisonTarget}} {
		fmt.Fprintf(os.Stderr, "Benchmarking %s\n", revision.label)
		side := filepath.Join(output, revision.label)
		if err := os.Mkdir(side, 0700); err != nil {
			return err
		}
		if err := benchmarkTarget(ctx, repo, revision.target, side, opts); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stats, err := os.Create(filepath.Join(output, "benchstat.txt"))
	if err != nil {
		return err
	}
	err = benchstat.Run(io.MultiWriter(os.Stdout, stats), os.Stderr, []string{
		"baseline=" + filepath.Join(output, "baseline", "bench.txt"),
		"comparison=" + filepath.Join(output, "comparison", "bench.txt"),
	})
	return errors.Join(err, stats.Close())
}

func benchmarkTarget(ctx context.Context, repo string, revision target, side string, opts Options) (err error) {
	dir := revision.dir
	if revision.sha != "" {
		tmp, err := os.MkdirTemp("", "benchstat-prof-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		dir = filepath.Join(tmp, "worktree")
		cmd := exec.CommandContext(ctx, "git", "-C", repo, "worktree", "add", "--detach", dir, revision.sha)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("create worktree: %w: %s", err, out)
		}
		defer func() {
			// Cleanup must still run when the benchmark context was canceled.
			cmd := exec.Command("git", "-C", repo, "worktree", "remove", "--force", dir)
			if out, cleanupErr := cmd.CombinedOutput(); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("remove worktree: %w: %s", cleanupErr, out))
			}
		}()
	}
	if err := executeTests(ctx, dir, side, "go-test", nil, opts.TestArgs); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(side, "go-test.stdout.txt"))
	if err != nil {
		return err
	}
	data := evidence.NormalizeTestOutput(raw)
	if err := os.WriteFile(filepath.Join(side, "bench.txt"), data, 0600); err != nil {
		return err
	}
	if err := evidence.SavePackageResults(side, data); err != nil {
		return err
	}
	if opts.Profiles {
		executor, err := profiling.Executor(side)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Collecting CPU and memory profiles (separate pass)")
		if err := executeTests(ctx, dir, side, "profile-go-test", []string{"-exec=" + executor}, opts.TestArgs); err != nil {
			return err
		}
	}
	return nil
}

func executeTests(ctx context.Context, dir, side, name string, extra, forwarded []string) error {
	stdout, err := os.Create(filepath.Join(side, name+".stdout.txt"))
	if err != nil {
		return err
	}
	stderr, err := os.Create(filepath.Join(side, name+".stderr.txt"))
	if err != nil {
		stdout.Close()
		return err
	}
	// Defaults precede forwarded arguments, preserving all user arguments and
	// allowing overrides. Profile overhead never enters timing results.
	args := []string{"test", "-run=^$", "-bench=.", "-benchmem", "-count=10"}
	args = append(args, extra...)
	args = append(args, forwarded...)
	if err := evidence.WriteYAML(filepath.Join(side, name+".command.yaml"), struct {
		Directory string
		Arguments []string
	}{dir, args}); err != nil {
		stdout.Close()
		stderr.Close()
		return err
	}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = stdout, io.MultiWriter(os.Stderr, stderr)
	runErr := cmd.Run()
	return errors.Join(runErr, stdout.Close(), stderr.Close())
}
