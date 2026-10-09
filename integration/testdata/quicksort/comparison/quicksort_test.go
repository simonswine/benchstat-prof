// SPDX-License-Identifier: Apache-2.0
package quicksortfixture

import (
	"math/rand"
	"slices"
	"testing"
)

func input(name string, size int) []int {
	values := make([]int, size)
	random := rand.New(rand.NewSource(42))
	for i := range values {
		switch name {
		case "sorted":
			values[i] = i
		case "reverse":
			values[i] = size - i
		case "duplicates":
			values[i] = random.Intn(8)
		default:
			values[i] = random.Intn(size * 4)
		}
	}
	return values
}

func TestQuickSort(t *testing.T) {
	for _, name := range []string{"random", "sorted", "reverse", "duplicates"} {
		for _, size := range []int{0, 1, 2, 15, 16, 17, 64, 2048} {
			values := input(name, size)
			want := slices.Clone(values)
			slices.Sort(want)
			QuickSort(values)
			if !slices.Equal(values, want) {
				t.Fatalf("%s/%d: got %v, want %v", name, size, values, want)
			}
		}
	}
	QuickSort(nil)
	values := []int{0, -1, -5, 3, -1}
	QuickSort(values)
	if !slices.Equal(values, []int{-5, -1, -1, 0, 3}) {
		t.Fatalf("negative values: %v", values)
	}
}

var sink []int

func BenchmarkQuickSort(b *testing.B) {
	for _, name := range []string{"random", "sorted", "reverse", "duplicates"} {
		b.Run(name, func(b *testing.B) {
			original := input(name, 2048)
			b.ReportAllocs()
			for b.Loop() {
				values := slices.Clone(original)
				QuickSort(values)
				sink = values
			}
			b.ReportMetric(float64(len(original)), "items/op")
		})
	}
}
