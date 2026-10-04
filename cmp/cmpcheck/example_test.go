// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmpcheck_test

import (
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpcheck"
)

// ExampleCheckWithOptions demonstrates a finite-sample equivalence check over
// a small set of samples. The options use an approximate-equality comparer
// that tolerates a difference of at most one: 0 ~ 1 and 1 ~ 2, but 0 !~ 2.
// The check reports the broken transitivity chains; it does not prove that
// the relation is an equivalence relation in general.
func ExampleCheckWithOptions() {
	samples := []any{0, 1, 2}
	opts := cmp.Options{cmp.Comparer(func(x, y int) bool {
		d := x - y
		if d < 0 {
			d = -d
		}
		return d <= 1
	})}
	report := cmpcheck.CheckWithOptions(samples, opts...)
	fmt.Println(report)

	// Output:
	// Finite-sample equivalence check over 3 sample(s) (indices 0..2).
	// This is a finite-sample check: it examines only the given samples and cannot
	// prove that the relation is an equivalence relation in general. A panicked
	// comparison is treated as undetermined, not unequal; transitivity is judged
	// only when all three relevant comparisons are determined.
	//
	// Reflexivity: OK
	// Symmetry: OK
	// Transitivity: VIOLATED
	//   counterexample 1: R(0,1)=true, R(1,2)=true, but R(0,2)=false (expected true)
	//   counterexample 2: R(2,1)=true, R(1,0)=true, but R(2,0)=false (expected true)
	// Undetermined (panicked) pairs: none
}
