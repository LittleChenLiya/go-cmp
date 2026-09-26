package cmp_test

import (
	"fmt"

	"github.com/google/go-cmp/cmp"
)

// DiffKeyed matches elements of two lists by a business key instead of by
// position, so reordered elements are reported as moves while genuinely
// added, removed, or modified elements are reported separately.
func ExampleDiffKeyed() {
	type Item struct {
		ID    string
		Value int
	}
	key := func(it Item) (string, error) { return it.ID, nil }

	oldList := []Item{{ID: "a", Value: 1}, {ID: "b", Value: 2}, {ID: "c", Value: 3}}
	newList := []Item{{ID: "c", Value: 3}, {ID: "a", Value: 10}, {ID: "d", Value: 4}}

	d, err := cmp.DiffKeyed(oldList, newList, key)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Print(d)

	// Output:
	// removed: [1] {b 2}
	// changed: key=a [0]->[1]
	// 	  cmp_test.Item{
	// 	  	ID:    "a",
	// 	- 	Value: 1,
	// 	+ 	Value: 10,
	// 	  }
	// moved:   key=a [0]->[1]
	// moved:   key=c [2]->[0]
	// added:   [2] {d 4}
}
