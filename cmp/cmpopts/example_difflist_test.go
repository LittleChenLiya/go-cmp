// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmpopts_test

import (
	"fmt"

	"github.com/google/go-cmp/cmp/cmpopts"
)

// Use DiffList to compare two lists of elements that may have been
// reordered, matching elements by a business key rather than by position.
// Elements present in both lists are reported in old-list order, followed
// by added elements in new-list order.
func ExampleDiffList() {
	type Server struct {
		ID    string
		Image string
	}
	oldList := []Server{
		{ID: "a", Image: "app:1"},
		{ID: "b", Image: "app:1"},
		{ID: "c", Image: "app:1"},
	}
	newList := []Server{
		{ID: "c", Image: "app:1"},
		{ID: "a", Image: "app:2"}, // a changed image and moved
		{ID: "d", Image: "app:1"}, // d was added
	}
	keyOf := func(s Server) (string, error) { return s.ID, nil }

	diff, err := cmpopts.DiffList(oldList, newList, keyOf)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Print(diff)

	// Output:
	// list diff:
	//   ~ [old 0 -> new 1] key=a: modified
	//         cmpopts_test.Server{
	//         	ID:    "a",
	//       - 	Image: "app:1",
	//       + 	Image: "app:2",
	//         }
	//   > [old 0 -> new 1] key=a: moved
	//   - [old 1] key=b: removed
	//   > [old 2 -> new 0] key=c: moved
	//   + [new 2] key=d: added
}
