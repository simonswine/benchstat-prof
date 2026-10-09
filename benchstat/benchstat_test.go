package benchstat

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"base.txt", "candidate.txt"} {
		path := filepath.Join(dir, name)
		data := strings.Repeat("BenchmarkExample-8 100 100 ns/op 16 B/op 1 allocs/op\n", 10)
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	var out, warnings bytes.Buffer
	if err := Run(&out, &warnings, paths); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Example", "base.txt", "candidate.txt", "sec/op", "B/op", "allocs/op", "~"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in output: %s", want, &out)
		}
	}
}

func TestRunErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"-unknown"}, {"missing.txt"}} {
		var out bytes.Buffer
		if err := Run(&out, &out, args); err == nil {
			t.Errorf("Run(%v) unexpectedly succeeded", args)
		}
	}
}
