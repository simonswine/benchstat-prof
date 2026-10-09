// SPDX-License-Identifier: Apache-2.0
package evidence

import (
	"strings"
	"testing"
	"time"
)

func TestGeneratedRunName(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 34, 56, 123456789, time.FixedZone("test", 3600))
	name, err := NewRunName(now)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidRunName(name) || !strings.HasPrefix(name, "20261009-113456.123456789Z-") {
		t.Fatalf("unexpected generated name: %s", name)
	}
	if parts := strings.Split(name, "-"); len(parts) != 4 || parts[2] == "" || parts[3] == "" {
		t.Fatalf("missing verb or animal: %s", name)
	}
	for _, name := range []string{"", ".", "..", "../escape", "/absolute", `a\b`, strings.Repeat("a", 121)} {
		if ValidRunName(name) {
			t.Errorf("unsafe run name accepted: %q", name)
		}
	}
}

func TestNormalizeTestOutput(t *testing.T) {
	raw := []byte("{\"Action\":\"start\"}\n{\"Action\":\"output\",\"Output\":\"BenchmarkWork 10 100 ns/op\\n\"}\n")
	if got := string(NormalizeTestOutput(raw)); got != "BenchmarkWork 10 100 ns/op\n" {
		t.Fatalf("unexpected normalized output: %q", got)
	}
}

func TestBenchmarkResults(t *testing.T) {
	data := []byte("pkg: example.com/project\ncpu: test\nBenchmarkSort/random-2 10 100 ns/op\nBenchmarkSort/random-2 20 110 ns/op\nBenchmarkSort/random-4 10 90 ns/op\nPASS\n")
	cases := BenchmarkResults(data)
	if len(cases) != 2 {
		t.Fatalf("expected separate CPU cases: %v", cases)
	}
	if strings.Count(string(cases["Sort/random-2"]), "BenchmarkSort/random-2") != 2 {
		t.Fatal("benchmark repetitions not grouped")
	}
	if strings.Contains(string(cases["Sort/random-2"]), "random-4") {
		t.Fatal("CPU configurations mixed")
	}
	if BenchmarkKey("example.com/pkg", "Sort") == BenchmarkKey("example.com", "pkg/Sort") {
		t.Fatal("package and sub-benchmark paths collide")
	}
}

func TestPackageKey(t *testing.T) {
	first, second := PackageKey("example.com/a/pkg"), PackageKey("example.com/b/pkg")
	if first == second {
		t.Fatal("different import paths must not collide")
	}
	if strings.ContainsAny(first, `/\`) {
		t.Fatalf("unsafe package key: %q", first)
	}
	if first != PackageKey("example.com/a/pkg") {
		t.Fatal("package key is unstable")
	}
}
