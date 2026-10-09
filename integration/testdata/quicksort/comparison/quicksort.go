// SPDX-License-Identifier: Apache-2.0
package quicksortfixture

// QuickSort uses a middle pivot, three-way partitioning for duplicate values,
// insertion sort for small partitions, and bounded recursion on the smaller side.
func QuickSort(values []int) {
	for len(values) > 16 {
		pivot := values[len(values)/2]
		low, current, high := 0, 0, len(values)
		for current < high {
			switch {
			case values[current] < pivot:
				values[low], values[current] = values[current], values[low]
				low++
				current++
			case values[current] > pivot:
				high--
				values[current], values[high] = values[high], values[current]
			default:
				current++
			}
		}
		if low < len(values)-high {
			QuickSort(values[:low])
			values = values[high:]
		} else {
			QuickSort(values[high:])
			values = values[:low]
		}
	}
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
