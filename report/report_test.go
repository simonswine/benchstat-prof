// SPDX-License-Identifier: Apache-2.0
package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBenchmarkCPUInNodeInfo(t *testing.T) {
	files := map[string][]byte{
		"plan.yaml":                []byte("host: benchmark-node (linux/amd64)\n"),
		"cpu.txt":                  []byte("Model name: obsolete hostname\nCPU(s): 8\n"),
		"baseline/pkg/bench.txt":   []byte("cpu: Example CPU Model\nBenchmarkSort-8 10 100 ns/op\n"),
		"comparison/pkg/bench.txt": []byte("cpu: Example CPU Model\nBenchmarkSort-8 10 90 ns/op\n"),
	}
	summary := buildReportSummary(files)
	if summary.CPU != "Example CPU Model" || summary.Host != "benchmark-node (linux/amd64)" || summary.Cores != 8 {
		t.Fatalf("incorrect node info: %+v", summary)
	}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := generateReport(files, path, false); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "CPU: Example CPU Model") || strings.Contains(string(html), "Instance type") {
		t.Fatal("incorrect node information in HTML")
	}
	files["comparison/pkg/bench.txt"] = []byte("cpu: Another CPU\nBenchmarkSort-8 10 90 ns/op\n")
	if got := buildReportSummary(files).CPU; got != "Another CPU / Example CPU Model" {
		t.Fatalf("CPU models not deterministic: %s", got)
	}
}

func TestReportsWithoutProfiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"baseline/pkg/bench.txt":   []byte("BenchmarkExample-2 100 200 ns/op 16 B/op 1 allocs/op\n"),
		"comparison/pkg/bench.txt": []byte("BenchmarkExample-2 100 100 ns/op 16 B/op 1 allocs/op\n"),
	}
	for _, local := range []bool{false, true} {
		path := filepath.Join(dir, "report.html")
		if err := generateReport(files, path, local); err != nil {
			t.Fatal(err)
		}
		data := readReportData(t, path)
		for _, p := range data.Benchmarks[0].Profiles {
			if p.BaselineFile != "" || p.ComparisonFile != "" {
				t.Fatal("missing profiles should not be linked")
			}
			if !strings.Contains(p.Warning, "profile missing") {
				t.Fatal("missing profile warning not shown")
			}
		}
	}
}

func TestLocalReportEscapesMetadata(t *testing.T) {
	dir := t.TempDir()
	payload := "</script><script>alert('unsafe')</script>"
	files := map[string][]byte{
		"baseline/pkg/bench.txt":   []byte("BenchmarkExample-2 100 100 ns/op\n"),
		"baseline/pkg/package.txt": []byte(payload),
	}
	path := filepath.Join(dir, "report-local.html")
	if err := generateReport(files, path, true); err != nil {
		t.Fatal(err)
	}
	data := readReportData(t, path)
	if data.Benchmarks[0].Name != payload {
		t.Fatal("metadata did not round-trip")
	}
	html, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), payload) {
		t.Fatal("unescaped HTML in report")
	}
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
