// SPDX-License-Identifier: Apache-2.0
package report

import (
	"os"
	"path/filepath"
	"strings"
)

// WriteRun generates web and self-contained local reports from a run directory.
func WriteRun(dir string) error {
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel != "plan.yaml" && rel != "status.yaml" && rel != "cpu.txt" &&
			!strings.HasSuffix(rel, "/bench.txt") && !strings.HasSuffix(rel, "/cpu.pprof") &&
			!strings.HasSuffix(rel, "/heap.pprof") && !strings.HasSuffix(rel, "/package.txt") && !strings.HasSuffix(rel, "/benchmark.txt") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil {
			files[rel] = data
		}
		return err
	})
	if err != nil {
		return err
	}
	// A failed build may have no package evidence; still produce both reports.
	if len(buildReport(files).Benchmarks) == 0 {
		files["baseline/run/bench.txt"] = nil
	}
	if err := generateReport(files, filepath.Join(dir, "report.html"), false); err != nil {
		return err
	}
	return generateReport(files, filepath.Join(dir, "report-local.html"), true)
}
