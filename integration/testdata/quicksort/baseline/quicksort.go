// SPDX-License-Identifier: Apache-2.0
package quicksortfixture

// QuickSort uses a straightforward Lomuto partition with the last-element pivot.
func QuickSort(values []int) {
	if len(values) < 2 {
		return
	}
	pivot := values[len(values)-1]
	boundary := 0
	for i := 0; i < len(values)-1; i++ {
		if values[i] < pivot {
			values[i], values[boundary] = values[boundary], values[i]
			boundary++
		}
	}
	values[boundary], values[len(values)-1] = values[len(values)-1], values[boundary]
	QuickSort(values[:boundary])
	QuickSort(values[boundary+1:])
}
