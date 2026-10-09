# Quicksort comparison fixture

Two standalone Go projects use the same module path and byte-identical test and
benchmark code. Only the quicksort implementation differs:

- `baseline/`: last-pivot Lomuto partitioning.
- `comparison/`: middle-pivot three-way partitioning, insertion sort for small
  partitions, and recursion on the smaller partition.

Both sort identical, deterministic 2,048-element inputs: random, sorted, reverse,
and duplicate-heavy. Benchmarks include the same input-cloning allocation on
both sides. They use `b.Loop()` and report a custom `items/op` metric (2,048).
Correctness tests compare results with `slices.Sort`. Integration tests check that
the custom metric survives raw output, benchstat analysis, and both report modes;
browser tests also exercise its overview selection.

From the repository root:

```sh
go test ./integration -run '^TestQuickSortDirectories$' -count=1

go run ./cmd/benchstat-prof test \
  --base path:./integration/testdata/quicksort/baseline \
  --comparison path:./integration/testdata/quicksort/comparison \
  --run-name quicksort \
  -- -bench=BenchmarkQuickSort -count=10 -benchtime=200ms ./...
```

The CLI writes reports under `.benchstat-prof/reports/quicksort/`. The integration
test copies the projects to temporary directories, checks both implementations,
and verifies benchmark results, separate CPU/memory evidence for each workload
and CPU configuration (`-cpu=1,2`), and both HTML reports. Each profiling subprocess
is checked to run exactly one measured benchmark case.
The browser integration suite uses this fixture to test flamegraphs and downloads.

Do not change one benchmark file without applying the same change to the other.
The integration test checks that the files are byte-identical. Performance deltas
are deliberately not asserted: machine load and sampling make them nondeterministic.
