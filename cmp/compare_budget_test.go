// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// budgetLink is a node in a singly linked list used to build deep objects.
type budgetLink struct {
	Value int
	Next  *budgetLink
}

func makeBudgetChain(n int) *budgetLink {
	root := &budgetLink{}
	cur := root
	for i := 1; i < n; i++ {
		cur.Next = &budgetLink{}
		cur = cur.Next
	}
	return root
}

// TestCompareStatuses verifies that the three results are distinct values.
func TestCompareStatuses(t *testing.T) {
	if cmp.StatusEqual == cmp.StatusDifferent ||
		cmp.StatusDifferent == cmp.StatusIncomplete ||
		cmp.StatusEqual == cmp.StatusIncomplete {
		t.Fatalf("equal, different, and incomplete must be distinct results")
	}
	if got, want := cmp.StatusIncomplete.String(), "incomplete"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

// TestCompareDeep verifies that with deep objects a difference within the
// budget reports different while a difference beyond the budget reports
// incomplete, and the two are not conflated.
func TestCompareDeep(t *testing.T) {
	const n = 8

	equalX := makeBudgetChain(n)
	equalY := makeBudgetChain(n)

	diffX := makeBudgetChain(n)
	diffY := makeBudgetChain(n)
	diffY.Value = 1 // The difference sits in the first node.

	lateDiffX := makeBudgetChain(n)
	lateDiffY := makeBudgetChain(n)
	lateDiffTail := lateDiffY
	for lateDiffTail.Next != nil {
		lateDiffTail = lateDiffTail.Next
	}
	lateDiffTail.Value = 1 // The difference sits in the last node.

	// Locate the minimum budget required to reach the last node's value.
	var reachBudget int
	for b := 1; b <= 8*n+8; b++ {
		if cmp.Compare(equalX, equalY, b) == cmp.StatusEqual {
			reachBudget = b
			break
		}
	}
	if reachBudget == 0 {
		t.Fatalf("failed to determine the budget that reaches the end of the chain")
	}

	tests := []struct {
		name   string
		x, y   *budgetLink
		budget int
		want   cmp.Status
	}{
		{"equal with enough budget", equalX, equalY, reachBudget, cmp.StatusEqual},
		{"equal with unbounded budget", equalX, equalY, -1, cmp.StatusEqual},
		{"equal with too little budget", equalX, equalY, reachBudget - 1, cmp.StatusIncomplete},
		{"difference within budget", diffX, diffY, reachBudget, cmp.StatusDifferent},
		{"difference within small budget", lateDiffX, lateDiffY, reachBudget, cmp.StatusDifferent},
		{"difference beyond budget", lateDiffX, lateDiffY, reachBudget - 1, cmp.StatusIncomplete},
		{"difference with unbounded budget", lateDiffX, lateDiffY, -1, cmp.StatusDifferent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cmp.Compare(tt.x, tt.y, tt.budget); got != tt.want {
				t.Fatalf("Compare = %v, want %v", got, tt.want)
			}
		})
	}

	// "different" and "incomplete" must not be the same answer.
	diff := cmp.Compare(lateDiffX, lateDiffY, reachBudget)
	inc := cmp.Compare(lateDiffX, lateDiffY, reachBudget-1)
	if diff == inc {
		t.Fatalf("difference within budget (%v) must differ from beyond budget (%v)", diff, inc)
	}
}

// TestCompareZeroBudget verifies that a zero budget never starts comparing.
func TestCompareZeroBudget(t *testing.T) {
	if got := cmp.Compare(1, 1, 0); got != cmp.StatusIncomplete {
		t.Fatalf("Compare(1, 1, 0) = %v, want incomplete", got)
	}
	if got := cmp.Compare(1, 2, 0); got != cmp.StatusIncomplete {
		t.Fatalf("Compare(1, 2, 0) = %v, want incomplete", got)
	}
	if got := cmp.Compare(makeBudgetChain(4), makeBudgetChain(4), 0); got != cmp.StatusIncomplete {
		t.Fatalf("zero budget on deep object = %v, want incomplete", got)
	}
}

// TestCompareCycle verifies that revisiting an already-seen value pair in a
// reference cycle is not charged, so cyclic graphs finish under a finite
// budget instead of being reported incomplete.
func TestCompareCycle(t *testing.T) {
	ringX := &budgetLink{Value: 1}
	ringX.Next = ringX
	ringY := &budgetLink{Value: 1}
	ringY.Next = ringY

	// Root pointer, indirect struct, and Value leaf cost three units; the
	// back-edge to the already-visited pointer pair must be free.
	if got := cmp.Compare(ringX, ringY, 3); got != cmp.StatusEqual {
		t.Fatalf("equal cyclic graph = %v, want equal", got)
	}
	if got := cmp.Compare(ringX, ringY, 100); got != cmp.StatusEqual {
		t.Fatalf("equal cyclic graph with large budget = %v, want equal", got)
	}
	if got := cmp.Compare(ringX, ringY, -1); got != cmp.StatusEqual {
		t.Fatalf("equal cyclic graph (unbounded) = %v, want equal", got)
	}
	if got := cmp.Compare(ringX, ringY, 2); got != cmp.StatusIncomplete {
		t.Fatalf("cyclic graph with too little budget = %v, want incomplete", got)
	}

	ringDiffY := &budgetLink{Value: 2}
	ringDiffY.Next = ringDiffY
	if got := cmp.Compare(ringX, ringDiffY, 3); got != cmp.StatusDifferent {
		t.Fatalf("unequal cyclic graph = %v, want different", got)
	}

	// A map containing a pointer to itself must also terminate under a
	// finite budget; revisiting the map pair is free.
	mapX := map[string]any{}
	mapX["self"] = mapX
	mapY := map[string]any{}
	mapY["self"] = mapY
	if got := cmp.Compare(mapX, mapY, 8); got != cmp.StatusEqual {
		t.Fatalf("self-referential map = %v, want equal", got)
	}
	if got := cmp.Compare(mapX, mapY, 1); got != cmp.StatusIncomplete {
		t.Fatalf("self-referential map with small budget = %v, want incomplete", got)
	}
}

// TestCompareIgnore verifies that Ignore options participate in budgeting.
func TestCompareIgnore(t *testing.T) {
	type hidden struct {
		Pub    int
		hidden int
	}
	opts := []cmp.Option{cmpopts.IgnoreUnexported(hidden{})}

	if got := cmp.Compare(hidden{1, 2}, hidden{1, 3}, 10, opts...); got != cmp.StatusEqual {
		t.Fatalf("ignored comparison = %v, want equal", got)
	}
	if got := cmp.Compare(hidden{1, 2}, hidden{2, 2}, 10, opts...); got != cmp.StatusDifferent {
		t.Fatalf("comparison with a real difference = %v, want different", got)
	}
	if got := cmp.Compare(hidden{1, 2}, hidden{1, 3}, 0, opts...); got != cmp.StatusIncomplete {
		t.Fatalf("ignored comparison with zero budget = %v, want incomplete", got)
	}
}

// budgetBox is transformed into an int by a Transformer in the tests below.
type budgetBox struct {
	N int
}

// TestCompareTransformer verifies Transformers work under a budget and are
// not invoked after the budget is exhausted.
func TestCompareTransformer(t *testing.T) {
	var calls int
	fnc := func(b budgetBox) int {
		calls++
		return b.N
	}
	opt := cmp.Transformer("unbox", fnc)

	calls = 0
	if got := cmp.Compare(budgetBox{1}, budgetBox{1}, 10, opt); got != cmp.StatusEqual {
		t.Fatalf("transformed equal values = %v, want equal", got)
	}
	// The transformer is applied once to each input (two sides), and the
	// dynamic determinism check may apply it a second time per side.
	if calls < 2 || calls > 4 {
		t.Fatalf("transformer called %d times, want 2 to 4", calls)
	}

	calls = 0
	if got := cmp.Compare(budgetBox{1}, budgetBox{2}, 10, opt); got != cmp.StatusDifferent {
		t.Fatalf("transformed different values = %v, want different", got)
	}
	if calls < 2 || calls > 4 {
		t.Fatalf("transformer called %d times, want 2 to 4", calls)
	}

	// With one unit, the root pair is entered and the transformer runs (once
	// per side, possibly doubled by the determinism check), but the
	// transformed output cannot be compared.
	calls = 0
	if got := cmp.Compare(budgetBox{1}, budgetBox{1}, 1, opt); got != cmp.StatusIncomplete {
		t.Fatalf("transformed values with one unit = %v, want incomplete", got)
	}
	if calls < 2 || calls > 4 {
		t.Fatalf("transformer called %d times, want 2 to 4", calls)
	}

	// With zero units the transformer must never be invoked.
	calls = 0
	if got := cmp.Compare(budgetBox{1}, budgetBox{1}, 0, opt); got != cmp.StatusIncomplete {
		t.Fatalf("transformed values with zero units = %v, want incomplete", got)
	}
	if calls != 0 {
		t.Fatalf("transformer called %d times after budget exhaustion, want 0", calls)
	}
}

// budgetLeaf is only comparable through a user Comparer.
type budgetLeaf int

type budgetLeafChain struct {
	L    budgetLeaf
	Next *budgetLeafChain
}

func makeBudgetLeafChain(n int) *budgetLeafChain {
	root := &budgetLeafChain{}
	cur := root
	for i := 1; i < n; i++ {
		cur.Next = &budgetLeafChain{}
		cur = cur.Next
	}
	return root
}

// TestCompareComparerCounting verifies that user Comparers stop being called
// as soon as the budget is exhausted.
func TestCompareComparerCounting(t *testing.T) {
	const n = 3
	var calls int
	opt := cmp.Comparer(func(x, y budgetLeaf) bool {
		calls++
		return x == y
	})

	// Two units only enter the root pointer and the first struct: the first
	// leaf (and therefore the Comparer) is never reached.
	calls = 0
	if got := cmp.Compare(makeBudgetLeafChain(n), makeBudgetLeafChain(n), 2, opt); got != cmp.StatusIncomplete {
		t.Fatalf("short-budget comparison = %v, want incomplete", got)
	}
	if calls != 0 {
		t.Fatalf("comparer called %d times after the budget was exhausted, want 0", calls)
	}

	// Zero budget also means zero Comparer calls.
	calls = 0
	cmp.Compare(makeBudgetLeafChain(n), makeBudgetLeafChain(n), 0, opt)
	if calls != 0 {
		t.Fatalf("comparer called %d times with zero budget, want 0", calls)
	}

	// Three units reach exactly one leaf. The dynamic determinism check may
	// invoke the Comparer at most twice for a single leaf.
	calls = 0
	if got := cmp.Compare(makeBudgetLeafChain(n), makeBudgetLeafChain(n), 3, opt); got != cmp.StatusIncomplete {
		t.Fatalf("one-leaf comparison = %v, want incomplete", got)
	}
	if calls < 1 || calls > 2 {
		t.Fatalf("comparer called %d times for one leaf, want 1 or 2", calls)
	}

	// An unbounded comparison reaches all n leaves.
	calls = 0
	if got := cmp.Compare(makeBudgetLeafChain(n), makeBudgetLeafChain(n), -1, opt); got != cmp.StatusEqual {
		t.Fatalf("unbounded comparison = %v, want equal", got)
	}
	if calls < n || calls > 2*n {
		t.Fatalf("comparer called %d times for %d leaves, want between %d and %d", calls, n, n, 2*n)
	}
}
