// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package equivalence_test

import (
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/equivalence"
)

// A short demo: four samples are enough to expose that
// "approximately equal within 1" is not transitive.
func ExampleCheck() {
	approx := cmp.Comparer(func(x, y int) bool {
		d := x - y
		return d*d <= 1 // |x-y| <= 1
	})
	r := equivalence.Check([]int{0, 1, 2, 5}, approx)
	fmt.Print(r)

	// Output:
	// finite-sample equivalence check: 4 samples, 16 comparisons
	// verdict: NOT an equivalence relation on the provided samples
	// transitivity violations (2):
	//   samples[0] ~ samples[1] and samples[1] ~ samples[2], but samples[0] !~ samples[2]
	//   samples[2] ~ samples[1] and samples[1] ~ samples[0], but samples[2] !~ samples[0]
	// scope: counterexamples are definitive, but their absence proves nothing beyond these samples; this is a finite-sample check, not a proof
}
