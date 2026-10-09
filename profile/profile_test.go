// SPDX-License-Identifier: Apache-2.0
package profile

import (
	"context"
	"strings"
	"testing"
)

func TestExecWord(t *testing.T) {
	for _, tc := range []struct{ word, want string }{
		{"/path with spaces/binary", "'/path with spaces/binary'"},
		{"/path's/binary", `"/path's/binary"`},
	} {
		got, err := execWord(tc.word)
		if err != nil || got != tc.want {
			t.Errorf("execWord(%q) = %q, %v", tc.word, got, err)
		}
	}
	if _, err := execWord(`both'and"quotes`); err == nil {
		t.Fatal("ambiguous quoting should fail")
	}
}

func TestIndividualBenchmarkArguments(t *testing.T) {
	args := []string{"-test.bench=.*", "-test.cpu", "1,2", "-test.run=TestSomething", "-test.cpuprofile=old.cpu", "-test.memprofile", "old.mem", "-test.count=3", "-fixture-size=8192"}
	got := benchmarkArguments(args, "Sort/Random.(8)-2", "/profiles")
	joined := strings.Join(got, " ")
	for _, want := range []string{`-test.bench=^BenchmarkSort$/^Random\.\(8\)$`, "-test.cpu=2", "-test.run=^$", "-test.count=3", "-fixture-size=8192", "-test.cpuprofile=/profiles/cpu.pprof", "-test.memprofile=/profiles/heap.pprof"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q: %v", want, got)
		}
	}
	for _, unwanted := range []string{"1,2", "TestSomething", "old.cpu", "old.mem", "-test.bench=.*"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("retained aggregate flag %q: %v", unwanted, got)
		}
	}
	if got := strings.Join(benchmarkArguments(args, "Sort/Random", "/profiles"), " "); !strings.Contains(got, "-test.cpu=1") {
		t.Fatal("unsuffixed benchmark must use CPU 1")
	}
}

func TestExecuteRequiresArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"directory"}} {
		if err := Execute(context.Background(), args); err == nil {
			t.Fatal("missing helper arguments accepted")
		}
	}
}
