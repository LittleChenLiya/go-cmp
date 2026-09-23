package cmp_test

import (
	"fmt"

	"github.com/google/go-cmp/cmp"
)

// MatchSlices compares two slices as unordered multisets using a maximum
// bipartite matching, which works even when the element relation is not
// transitive. Here values within an absolute difference of 1 are equal,
// so a greedy pairing of left[0]=2 with right[0]=2 would fail, while the
// maximum matching pairs 2 with 3 and 1 with 2.
func ExampleMatchSlices() {
	near := cmp.Comparer(func(a, b int) bool {
		d := a - b
		return d >= -1 && d <= 1
	})
	left := []int{2, 1}
	right := []int{2, 3}

	m, err := cmp.MatchSlices(left, right, cmp.MatchSettings{}, near)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("left:", left)
	fmt.Println("right:", right)
	for _, p := range m.Pairs {
		fmt.Printf("match: left[%d]=%d -> right[%d]=%d\n", p.Left, left[p.Left], p.Right, right[p.Right])
	}
	fmt.Println("unmatched left:", m.UnmatchedLeft)
	fmt.Println("unmatched right:", m.UnmatchedRight)
	fmt.Println("perfect:", m.Perfect())

	// Output:
	// left: [2 1]
	// right: [2 3]
	// match: left[0]=2 -> right[1]=3
	// match: left[1]=1 -> right[0]=2
	// unmatched left: []
	// unmatched right: []
	// perfect: true
}
