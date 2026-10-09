# benchstat-prof

Run Go benchmarks against commits or local directories and compare with embedded
`benchstat`. The CLI uses urfave/cli.

## Requirements

- Go 1.26 or newer
- Git

Benchstat is embedded; no separate executable or installation is needed.
Go dependencies are pinned in `go.mod` and verified by `go.sum`.
The first build downloads them into the Go module cache.

## Usage

```sh
go build -o benchstat-prof ./cmd/benchstat-prof
./benchstat-prof test --base main -- -bench BenchmarkExample -count 10 ./...
```

`--base` is required. Both `--base` and `--comparison` accept a Git commit/ref
or `path:<directory>`. `--comparison` defaults to `path:./` (the current working
directory, including uncommitted changes). Relative paths are relative to the
invocation directory; `--repo` selects the repository used to resolve Git refs.

```sh
./benchstat-prof test --repo /path/to/project \
  --base main --comparison feature-branch --run-name my-comparison \
  -- -bench BenchmarkExample -count 10 -benchtime 1s ./pkg/example

./benchstat-prof test --base path:../baseline --comparison path:./ \
  -- -bench . -count 5 ./...
```

Everything after `--` is forwarded unchanged to `go test` for both targets.
Default arguments are `-run=^$ -bench=. -benchmem -count=10`, placed before
forwarded arguments so explicit flags override them. Without a package argument,
Go tests the target directory's package (use `./...` to include all packages).

**You must ensure the same benchmarks run in the baseline and comparison.**
Keep benchmark code, inputs, and configuration identical so the results measure
changes to the implementation, not changes to the benchmark itself. This tool
passes the same `go test` arguments to both targets but does not verify that
their benchmark definitions match.

Targets run sequentially. Commit targets use temporary detached Git worktrees,
removed on completion or failure. Path targets run directly in the specified
directory, without requiring Git; benchmark code can modify these directories.

Runs are saved under `<worktree-root>/.benchstat-prof/reports/<run-name>/`.
`--run-name` defaults to a UTC datetime plus a random performance-engineering
verb and animal, such as `20261009-111500.123456789Z-tuning-otter`. Existing run
names are never overwritten. `--repo` determines the worktree root; outside Git,
its directory is used instead.

CPU and memory profiles are collected by default in a separate pass, keeping
profiling overhead out of benchstat timing measurements. Use `--profiles=false`
to disable collection. The unprofiled measurements discover the exact benchmark
and sub-benchmark names; each is then profiled in its own subprocess. Distinct
`-cpu` cases also get separate captures. Each benchmark retains its `cpu.pprof`,
`heap.pprof`, command arguments, and logs; each package retains its test binary.
This also works with `./...`. The profiling pass forces an exact benchmark/CPU
selection, skips ordinary tests, and controls CPU/memory output paths. Other
benchmark flags and custom arguments are preserved. A forwarded `-exec` overrides
the helper and can result in missing profiles, shown as report warnings.

Each run includes two HTML reports, `benchstat.txt`, metadata, and raw evidence
under `baseline/` and `comparison/`:

- **`report-local.html`**: self-contained, with scripts, styles, and original
  `.pprof` bytes embedded. Open it directly from disk, offline; it can be copied
  or shared as a single file with no accompanying evidence directory.
- **`report.html`**: a smaller web report that loads each native `.pprof` file
  on demand. Serve it together with the evidence directory over HTTP.

Both include a sortable overview, comparative flamegraphs, and profile download
buttons. Profiles are decoded in the browser; no intermediate profile-tree
JSON files are generated. The downloaded files match the original evidence.

To serve the web report:

```sh
cd .benchstat-prof/reports/<run-name>
python3 -m http.server 8000
# Open http://localhost:8000/report.html
```

Failed runs retain evidence and generate both reports with a failure warning. The
embedded benchstat comparison is also printed to stdout.

Benchmark code executes with your user permissions: only use trusted projects.
Submodules are not initialized automatically. Keep machine load and Go/toolchain
environment consistent when comparing results. Profiles isolate each benchmark
but still include its setup, calibration, and process startup; profile deltas
are descriptive, not statistical tests.

## Publishing to GitHub Pages

Install [GitHub CLI](https://cli.github.com/) and authenticate with `gh auth login`.
Publish an existing run directory:

```sh
./benchstat-prof publish .benchstat-prof/reports/my-comparison

# Explicitly allow creating a public reports repository if it is missing:
./benchstat-prof publish --init .benchstat-prof/reports/my-comparison

# Publish to another owner or repository:
./benchstat-prof publish --owner my-org --repo benchmark-reports \
  .benchstat-prof/reports/my-comparison
```

`--owner` defaults to the authenticated GitHub user and `--repo` defaults to
`benchstat-prof-reports`. If the repository does not exist, the command asks you
to run again with `--init`; it never creates a repository without that flag.
Creation requires permission to create repositories for the selected user or
organization. Publishing requires repository write and Pages configuration permissions.
Existing repository visibility is not changed; private-repository Pages support
depends on your GitHub plan.

All remote operations use `gh api` on github.com, not a Git clone or push. Reports
are committed atomically to the `pages` branch, with GitHub Pages configured to
serve that branch's root (not an Actions workflow). `.nojekyll` disables Jekyll
processing. GitHub's Pages deployment can take a few minutes after publication.
The command prints the report URL, including any existing custom domain.

The branch contains `index.yaml`, a generated `index.html` catalogue, and reports
under `reports/<run-name>/`. Publishing the same run name twice fails, even if its
content differs. Existing reports are preserved, including during concurrent
publications. A default-branch `README.md` links to the Pages site and
[benchstat-prof](https://github.com/simonswine/benchstat-prof); existing README
content is preserved outside a managed links section.

Only the two HTML reports and native CPU/heap profiles are uploaded; test binaries,
logs, and unrelated evidence are excluded. Reports expose benchmark metadata,
source symbols, and potentially machine information: review them before publishing.
Input symlinks are rejected. An existing empty repository is initialized through
the API before creating the site branch.

If Pages configuration or the default-branch README update fails after the report
commit, the command reports that partial success. The committed report is retained;
fix the reported configuration or README issue rather than republishing the same ID.

## Vendored benchstat

`benchstat/` contains the upstream benchstat command and table renderer,
from `golang.org/x/perf` commit `406019bb8b6893dd1245d31bf511c719619bb5c9`.
The BSD license and original copyright headers are preserved. Local adaptations
expose `Run`, relocate internal imports, and return errors instead of exiting.
Supporting x/perf and go-moremath packages are managed as Go module dependencies.

After updating the pinned dependency, run `go mod tidy`.
Updates to the copied command and renderer must be applied separately, retaining
the local adaptations and licenses.

## License

Project code and the report implementation are licensed under Apache-2.0.
See `LICENSE` and `NOTICE`. Third-party code retains its original licenses;
these are preserved alongside the copied code, bundled assets, and in the
upstream Go modules.

## Package layout

Go module: `github.com/simonswine/benchstat-prof`. The repository root has no Go files.

- `cmd/benchstat-prof/`: binary entry point and signal handling.
- `cli/`: urfave/cli commands and profiling-subprocess dispatch (`cli.Run`).
- `benchmark/`: target resolution, worktree lifecycle, and benchmark execution (`benchmark.Run`).
- `evidence/`: shared metadata, run names, artifact paths, and output normalization.
- `profile/`: per-benchmark CPU/memory capture and retained test binaries.
- `report/`: report generation from run evidence (`report.WriteRun`).
- `publish/`: GitHub API publication, report catalogue, Pages configuration, and repository README.
- `integration/`: black-box CLI integration tests.
- `benchstat/`: copied benchmark comparison command and table renderers.
- `thirdparty/`: legacy benchmark analysis used by the report, with original licenses.
- `report-ui/`: report frontend source, native pprof decoder, and UI tests.
- `report-ui/assets/`: Go-embedded prebuilt report UI and its third-party licenses.

## Development

```sh
go test ./...
go vet ./...
```

After changing frontend source, rebuild the checked-in embedded assets:

```sh
corepack enable
cd report-ui
pnpm install --frozen-lockfile
pnpm test
pnpm run build
```

Node.js 24+ and pnpm (pinned by `packageManager`) are only needed to develop/test
the frontend, not to build or run the Go CLI. Enable pnpm through Corepack, or
install the pinned version separately if Corepack is unavailable.

### Integration tests

`go test ./...` includes `integration/TestCLIIntegration`, which builds the real binary,
creates two Git commits with identical benchmarks, and exercises multiple
packages, forwarded flags, CPU/memory captures, both report formats, HTTP
profile serving, failure reports, and worktree cleanup. Use `go test -short ./...`
to skip this longer test.

`integration/TestQuickSortDirectories` compares the two projects in
`integration/testdata/quicksort/baseline/` and `comparison/` using `path:` targets.
They have different quicksort implementations but byte-identical benchmarks with
deterministic random, sorted, reverse, and duplicate-heavy inputs. The test checks
correctness, benchmark output, captured profiles, and both report formats.

The browser suite uses the quicksort fixture's real captured profiles to test flamegraph rendering,
overview sorting, and byte-for-byte profile downloads in both modes. The local
report is copied away from its evidence and tested via `file://` with the browser
offline:

```sh
cd report-ui
pnpm install --frozen-lockfile
pnpm exec playwright install chromium
pnpm run test:integration
```

On Linux CI, use `pnpm exec playwright install --with-deps chromium`.
Browser tests retain traces on failure under `report-ui/test-results/`.
