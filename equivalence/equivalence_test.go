// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package equivalence_test

import (
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/equivalence"
)

// approxEqual reports |x-y| <= 1, so 0 ~ 1 and 1 ~ 2, but 0 !~ 2.
func approxEqual() cmp.Option {
	return cmp.Comparer(func(x, y int) bool {
		d := x - y
		return d*d <= 1
	})
}

func TestTransitivityCounterexample(t *testing.T) {
	r := equivalence.Check([]int{0, 1, 2}, approxEqual())
	if r.OK() {
		t.Fatal("OK() = true, want false: approximate equality is not transitive")
	}
	if !r.Conclusive() {
		t.Fatalf("Conclusive() = false, want true; Unknowns = %v", r.Unknowns)
	}
	if r.Calls != 9 {
		t.Errorf("Calls = %d, want 9 (one cmp.Equal per ordered pair)", r.Calls)
	}
	if len(r.Reflexive) != 0 {
		t.Errorf("Reflexive = %v, want none", r.Reflexive)
	}
	if len(r.Symmetric) != 0 {
		t.Errorf("Symmetric = %v, want none", r.Symmetric)
	}
	want := map[[3]int]bool{{0, 1, 2}: true, {2, 1, 0}: true}
	if len(r.Transitive) != len(want) {
		t.Fatalf("Transitive = %v, want chains (0,1,2) and (2,1,0)", r.Transitive)
	}
	for _, c := range r.Transitive {
		if !want[c] {
			t.Errorf("unexpected transitivity chain %v", c)
		}
	}
	// The cached verdict matrix must reflect 0 ~ 1, 1 ~ 2, 0 !~ 2.
	if got := r.Verdict(0, 1); got != equivalence.Equal {
		t.Errorf("Verdict(0, 1) = %v, want equal", got)
	}
	if got := r.Verdict(1, 2); got != equivalence.Equal {
		t.Errorf("Verdict(1, 2) = %v, want equal", got)
	}
	if got := r.Verdict(0, 2); got != equivalence.NotEqual {
		t.Errorf("Verdict(0, 2) = %v, want not-equal", got)
	}
}

func TestReflexivityCounterexample(t *testing.T) {
	t.Run("NaN", func(t *testing.T) {
		// Under default comparison NaN != NaN, so the relation is not
		// reflexive on samples containing NaN.
		r := equivalence.Check([]float64{math.NaN(), 1.0})
		if r.OK() {
			t.Fatal("OK() = true, want false: NaN != NaN")
		}
		if len(r.Reflexive) != 1 || r.Reflexive[0] != 0 {
			t.Errorf("Reflexive = %v, want [0]", r.Reflexive)
		}
		if len(r.Symmetric) != 0 || len(r.Transitive) != 0 {
			t.Errorf("Symmetric = %v, Transitive = %v, want both empty", r.Symmetric, r.Transitive)
		}
	})
	t.Run("NotEqualComparer", func(t *testing.T) {
		ne := cmp.Comparer(func(x, y int) bool { return x != y })
		r := equivalence.Check([]int{5}, ne)
		if len(r.Reflexive) != 1 || r.Reflexive[0] != 0 {
			t.Errorf("Reflexive = %v, want [0]", r.Reflexive)
		}
	})
}

func TestSymmetryCounterexample(t *testing.T) {
	// A stateful comparer that is consistent within a single cmp.Equal
	// call (cmp invokes it twice per comparison for its own symmetry
	// spot-check, and both invocations agree) but flips between calls,
	// so Equal(1, 2) and Equal(2, 1) disagree. This models comparers
	// whose result depends on state outside the compared values, which
	// the per-call spot-check inside cmp cannot catch.
	var n int
	flip := cmp.Comparer(func(x, y int) bool {
		if x == y {
			return true
		}
		n++
		return (n+1)/2%2 == 1
	})
	r := equivalence.Check([]int{1, 2}, flip)
	if r.OK() {
		t.Fatal("OK() = true, want false: relation is not symmetric")
	}
	if len(r.Symmetric) != 1 || r.Symmetric[0] != [2]int{0, 1} {
		t.Errorf("Symmetric = %v, want [[0 1]]", r.Symmetric)
	}
	if len(r.Reflexive) != 0 {
		t.Errorf("Reflexive = %v, want none", r.Reflexive)
	}
	if len(r.Transitive) != 0 {
		t.Errorf("Transitive = %v, want none", r.Transitive)
	}
	if got := r.Verdict(0, 1); got != equivalence.Equal {
		t.Errorf("Verdict(0, 1) = %v, want equal", got)
	}
	if got := r.Verdict(1, 0); got != equivalence.NotEqual {
		t.Errorf("Verdict(1, 0) = %v, want not-equal", got)
	}
}

// panicky is a type whose comparer always panics.
type panicky int

func TestPanicIsUnknown(t *testing.T) {
	boom := cmp.Comparer(func(x, y panicky) bool { panic("boom") })
	r := equivalence.Check([]panicky{0, 1}, boom)
	if !r.OK() {
		t.Errorf("OK() = false, want true: a panic is undecidable, not a violation")
	}
	if r.Conclusive() {
		t.Error("Conclusive() = true, want false")
	}
	// All four ordered pairs must be undecidable and nothing else may
	// be reported: a panicking type shows up as unknown only.
	if len(r.Unknowns) != 4 {
		t.Fatalf("Unknowns = %v, want 4 ordered pairs", r.Unknowns)
	}
	if len(r.Reflexive) != 0 || len(r.Symmetric) != 0 || len(r.Transitive) != 0 {
		t.Errorf("Reflexive = %v, Symmetric = %v, Transitive = %v, want all empty",
			r.Reflexive, r.Symmetric, r.Transitive)
	}
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			if got := r.Verdict(i, j); got != equivalence.Unknown {
				t.Errorf("Verdict(%d, %d) = %v, want unknown", i, j, got)
			}
		}
	}
	s := r.String()
	if !strings.Contains(s, "undecidable") {
		t.Errorf("report does not mention undecidable comparisons:\n%s", s)
	}
	if strings.Contains(s, "violations") {
		t.Errorf("report must not claim violations for undecidable pairs:\n%s", s)
	}
}

func TestComparerCallCounts(t *testing.T) {
	samples := []int{0, 1, 2}
	var mu sync.Mutex // cmp may invoke the comparer from a goroutine
	calls := map[[2]int]int{}
	counting := cmp.Comparer(func(x, y int) bool {
		mu.Lock()
		calls[[2]int{x, y}]++
		mu.Unlock()
		return x == y
	})
	r := equivalence.Check(samples, counting)

	// The checker must call cmp.Equal exactly once per ordered pair.
	if want := len(samples) * len(samples); r.Calls != want {
		t.Errorf("Calls = %d, want %d (one cmp.Equal per ordered pair)", r.Calls, want)
	}

	// Every ordered pair of sample values must have been compared, and
	// cmp.Equal invokes the comparer exactly twice per call (its own
	// symmetry spot-check on the first comparer call), so a count of 2
	// per pair proves the checker made exactly one cmp.Equal per pair.
	if len(calls) != 9 {
		t.Errorf("comparer saw %d distinct ordered pairs, want 9", len(calls))
	}
	for pair, n := range calls {
		if n != 2 {
			t.Errorf("comparer called %d times for pair %v, want 2 (one cmp.Equal)", n, pair)
		}
	}
}

func TestEquivalenceRelationPasses(t *testing.T) {
	mod2 := cmp.Comparer(func(x, y int) bool { return x%2 == y%2 })
	r := equivalence.Check([]int{0, 1, 2, 3}, mod2)
	if !r.OK() {
		t.Errorf("OK() = false, want true: equality mod 2 is an equivalence relation\n%s", r)
	}
	if !r.Conclusive() {
		t.Error("Conclusive() = false, want true")
	}
	if r.Calls != 16 {
		t.Errorf("Calls = %d, want 16", r.Calls)
	}
}

// secret serializes to a marker string; the report must never contain it.
type secret int

func (secret) String() string { return "SECRET-VALUE" }

func TestReportUsesIndicesOnly(t *testing.T) {
	approx := cmp.Comparer(func(a, b secret) bool {
		d := a - b
		return d*d <= 1
	})
	r := equivalence.Check([]secret{0, 1, 2}, approx)
	if r.OK() {
		t.Fatalf("OK() = true, want false: approximate equality is not transitive")
	}
	s := r.String()
	if strings.Contains(s, "SECRET-VALUE") {
		t.Errorf("report serializes sample values, want indices only:\n%s", s)
	}
	if !strings.Contains(s, "samples[") {
		t.Errorf("report does not reference samples by index:\n%s", s)
	}
	if !strings.Contains(s, "finite-sample check") {
		t.Errorf("report does not state its finite-sample scope:\n%s", s)
	}
}
