// SPDX-License-Identifier: Apache-2.0
// Package cli provides the benchstat-prof command-line interface.
package cli

import (
	"context"
	"fmt"

	"github.com/simonswine/benchstat-prof/benchmark"
	"github.com/simonswine/benchstat-prof/profile"
	"github.com/simonswine/benchstat-prof/publish"

	urfave "github.com/urfave/cli/v2"
)

// Run executes the CLI with arguments excluding the executable name.
// It also dispatches the profiling subprocess invoked by go test -exec.
func Run(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == profile.ExecCommand {
		return profile.Execute(ctx, args[1:])
	}
	app := &urfave.App{
		Name:  "benchstat-prof",
		Usage: "compare Go benchmarks between commits or local directories",
		Action: func(c *urfave.Context) error {
			if c.Args().Len() > 0 {
				return fmt.Errorf("unknown command %q", c.Args().First())
			}
			return urfave.ShowAppHelp(c)
		},
		Commands: []*urfave.Command{{
			Name:  "test",
			Usage: "run benchmarks and compare results using embedded benchstat",
			Description: "You must ensure benchmark code, inputs, and configuration are identical in the base and comparison. " +
				"The same go test arguments are passed to both targets, but benchmark definitions are not checked.",
			ArgsUsage: "-- [go test arguments]",
			Flags: []urfave.Flag{
				&urfave.StringFlag{Name: "base", Required: true, Usage: "base commit/ref, or path:<directory>"},
				&urfave.StringFlag{Name: "comparison", Value: "path:./", Usage: "comparison commit/ref, or path:<directory>"},
				&urfave.StringFlag{Name: "repo", Value: ".", Usage: "Git repository for commit references"},
				&urfave.StringFlag{Name: "run-name", Usage: "run directory name (default: UTC datetime–verb–animal)"},
				&urfave.BoolFlag{Name: "profiles", Value: true, Usage: "collect CPU and memory profiles in a separate pass"},
			},
			Action: func(c *urfave.Context) error {
				// Require a separator so mistyped CLI flags cannot silently
				// become go test arguments.
				if c.Args().Len() > 0 {
					found := false
					for _, arg := range args {
						if arg == "--" {
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("go test arguments must follow --")
					}
				}
				return benchmark.Run(c.Context, benchmark.Options{
					Repo: c.String("repo"), RunName: c.String("run-name"), Profiles: c.Bool("profiles"), TestArgs: c.Args().Slice(),
				}, c.String("base"), c.String("comparison"))
			},
		}, {
			Name:      "publish",
			Usage:     "publish a run report to branch-based GitHub Pages using gh api",
			ArgsUsage: "<run-directory>",
			Flags: []urfave.Flag{
				&urfave.StringFlag{Name: "owner", Usage: "GitHub owner (default: authenticated gh user)"},
				&urfave.StringFlag{Name: "repo", Value: publish.DefaultRepo, Usage: "GitHub reports repository"},
				&urfave.BoolFlag{Name: "init", Usage: "create a public reports repository if missing"},
			},
			Action: func(c *urfave.Context) error {
				if c.Args().Len() != 1 {
					return fmt.Errorf("publish requires exactly one run directory")
				}
				url, err := publish.Run(c.Context, c.Args().First(), publish.Options{Owner: c.String("owner"), Repo: c.String("repo"), Init: c.Bool("init")})
				if err != nil {
					return err
				}
				fmt.Fprintf(c.App.Writer, "Published: %s\nGitHub Pages deployment may take a few minutes.\n", url)
				return nil
			},
		}},
	}
	return app.RunContext(ctx, append([]string{"benchstat-prof"}, args...))
}
