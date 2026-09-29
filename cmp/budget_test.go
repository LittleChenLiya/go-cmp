// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"fmt"
	"sync"
	"testing"
)

// budgetNode is a singly linked list used as a deterministic deep object.
// Fully comparing a chain of N nodes charges 3*N+1 value pairs: the root,
// and for every node the struct pair, its V leaf pair, and the Indirect
// pair of the next pointer (the final pointer has a nil leaf instead).
type budgetNode struct {
	V    int
	Next *budgetNode
}

func makeChain(n int) (*budgetNode, *budgetNode) {
	var xh, yh *budgetNode
	xp, yp := &xh, &yh
	for i := 0; i < n; i++ {
		*xp = &budgetNode{V: i}
		*yp = &budgetNode{V: i}
		xp, yp = &(*xp).Next, &(*yp).Next
	}
	return xh, yh
}

func chainCost(n int) int { return 3*n + 1 }

// indirectDepth counts how many pointer indirections precede the current
// position in the path.
func indirectDepth(p Path) int {
	depth := 0
	for _, s := range p {
		if _, ok := s.(Indirect); ok {
			depth++
		}
	}
	return depth
}

func TestStatusString(t *testing.T) {
	for _, tc := range []struct {
		status Status
		want   string
	}{
		{StatusEqual, "equal"},
		{StatusDifferent, "different"},
		{StatusIncomplete, "incomplete"},
		{Status(99), "incomplete"},
	} {
		if got := tc.status.String(); got != tc.want {
			t.Errorf("Status(%d).String() = %q, want %q", tc.status, got, tc.want)
		}
	}
	if !StatusEqual.Equal() || StatusEqual.Different() || StatusEqual.Incomplete() {
		t.Errorf("StatusEqual accessors wrong")
	}
	if StatusDifferent.Equal() || !StatusDifferent.Different() || StatusDifferent.Incomplete() {
		t.Errorf("StatusDifferent accessors wrong")
	}
	if StatusIncomplete.Equal() || StatusIncomplete.Different() || !StatusIncomplete.Incomplete() {
		t.Errorf("StatusIncomplete accessors wrong")
	}
}

// TestEqualBudgetedDeep covers the core requirement: the same deep object is
// reported as different when a difference lies within the budget, and as
// incomplete when it lies beyond the budget; the two must never be conflated.
func TestEqualBudgetedDeep(t *testing.T) {
	const n = 10
	cost := chainCost(n)

	x, y := makeChain(n)
	if !Equal(x, y) {
		t.Fatal("setup: equal chains should compare equal")
	}

	if got := EqualBudgeted(x, y, cost); got != (BudgetResult{Status: StatusEqual, Used: cost}) {
		t.Errorf("exact budget: got %+v, want {equal %d}", got, cost)
	}
	if got := EqualBudgeted(x, y, cost+50); got != (BudgetResult{Status: StatusEqual, Used: cost}) {
		t.Errorf("generous budget: got %+v, want {equal %d}", got, cost)
	}
	if got := EqualBudgeted(x, y, -1); got != (BudgetResult{Status: StatusEqual, Used: cost}) {
		t.Errorf("unlimited budget: got %+v, want {equal %d}", got, cost)
	}

	// Difference at the deepest leaf, within the exact budget: different.
	xd, yd := makeChain(n)
	yd.Next.Next.Next.Next.Next.Next.Next.Next.Next.V = -1
	if Equal(xd, yd) {
		t.Fatal("setup: modified chains should differ")
	}
	if got := EqualBudgeted(xd, yd, cost); got != (BudgetResult{Status: StatusDifferent, Used: cost}) {
		t.Errorf("difference within budget: got %+v, want {different %d}", got, cost)
	}

	// Same difference beyond smaller budgets: incomplete, never different.
	for _, budget := range []int{1, 2, 3, cost - 3, cost - 1} {
		got := EqualBudgeted(xd, yd, budget)
		if got != (BudgetResult{Status: StatusIncomplete, Used: budget}) {
			t.Errorf("budget %d: got %+v, want {incomplete %d}", budget, got, budget)
		}
		if got.Status.Equal() || got.Status.Different() || !got.Status.Incomplete() {
			t.Errorf("budget %d: incomplete status misclassified", budget)
		}
	}

	// A difference found early is still reported as incomplete if the budget
	// is later exhausted; an observed difference must not override incomplete.
	xs, ys := makeChain(n)
	xs.V, ys.V = 1, 2
	if got := EqualBudgeted(xs, ys, cost-1); got.Status != StatusIncomplete {
		t.Errorf("early difference then exhaustion: got %s, want incomplete", got.Status)
	}
	// The same shallow difference is definitively different once the budget
	// covers the complete traversal (the engine never short-circuits after
	// the first difference).
	if got := EqualBudgeted(xs, ys, cost); got.Status != StatusDifferent {
		t.Errorf("shallow difference within budget: got %s, want different", got.Status)
	}

	// A small standalone object whose only pair is different is reported as
	// different even with a single-unit budget.
	if got := EqualBudgeted(1, 2, 1); got != (BudgetResult{Status: StatusDifferent, Used: 1}) {
		t.Errorf("single-pair difference: got %+v, want {different 1}", got)
	}
}

// TestEqualBudgetedUnlimitedConsistentWithEqual checks that disabling the
// budget preserves the verdict of Equal across several shapes of values.
func TestEqualBudgetedUnlimitedConsistentWithEqual(t *testing.T) {
	deepEqX, deepEqY := makeChain(7)
	deepDiffX, deepDiffY := makeChain(7)
	deepDiffY.V = 99
	sx := budgetCycleSlice{nil}
	sx[0] = sx
	sy := budgetCycleSlice{nil}
	sy[0] = sy
	for _, tc := range []struct {
		name   string
		x, y   any
		wantEq bool
	}{
		{"ints equal", 1, 1, true},
		{"ints differ", 1, 2, false},
		{"nils", nil, nil, true},
		{"slices", []int{1, 2, 3}, []int{1, 2, 4}, false},
		{"maps", map[string]int{"a": 1}, map[string]int{"a": 1}, true},
		{"deep equal", deepEqX, deepEqY, true},
		{"deep differ", deepDiffX, deepDiffY, false},
		{"slice cycle", sx, sy, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Equal(tc.x, tc.y); got != tc.wantEq {
				t.Fatalf("Equal = %v, want %v", got, tc.wantEq)
			}
			r := EqualBudgeted(tc.x, tc.y, -1)
			if (r.Status == StatusEqual) != tc.wantEq {
				t.Errorf("EqualBudgeted(-1) = %s, want equal=%v (used=%d)", r.Status, tc.wantEq, r.Used)
			}
		})
	}
}

// TestEqualBudgetedZero proves that a zero budget never compares any values:
// even trivially equal or different inputs are reported as incomplete and
// user callbacks are not invoked.
func TestEqualBudgetedZero(t *testing.T) {
	calls := 0
	opt := Comparer(func(a, b int) bool {
		calls++
		return a == b
	})
	if got := EqualBudgeted(1, 1, 0); got != (BudgetResult{Status: StatusIncomplete, Used: 0}) {
		t.Errorf("equal ints: got %+v, want {incomplete 0}", got)
	}
	if got := EqualBudgeted(1, 2, 0, opt); got != (BudgetResult{Status: StatusIncomplete, Used: 0}) {
		t.Errorf("different ints: got %+v, want {incomplete 0}", got)
	}
	if calls != 0 {
		t.Errorf("comparer invoked %d times with zero budget", calls)
	}
	x, y := makeChain(100)
	if got := EqualBudgeted(x, y, 0); got != (BudgetResult{Status: StatusIncomplete, Used: 0}) {
		t.Errorf("deep object: got %+v, want {incomplete 0}", got)
	}
}

// TestEqualBudgetedComparerStops proves that after the budget is exhausted no
// further user Comparer callbacks run, and checks the charged pair count.
func TestEqualBudgetedComparerStops(t *testing.T) {
	const n = 8
	x, y := makeChain(n)

	var mu sync.Mutex
	var calls int
	maxDepth := 0
	var depth int
	countingComparer := FilterPath(func(p Path) bool {
		sf, ok := p.Last().(StructField)
		if ok && sf.Name() == "V" {
			mu.Lock()
			depth = indirectDepth(p)
			mu.Unlock()
			return true
		}
		return false
	}, comparerForDepth(&mu, &calls, &maxDepth, &depth, 0, nil))

	// Budget 3*k+1 charges the k-th Indirect pair and the k-th node struct;
	// the V leaf of node k is the last leaf pair charged before exhaustion.
	const k = 3
	budget := chainCost(k)
	got := EqualBudgeted(x, y, budget, countingComparer)
	if got != (BudgetResult{Status: StatusIncomplete, Used: budget}) {
		t.Fatalf("got %+v, want {incomplete %d}", got, budget)
	}
	if maxDepth > k {
		t.Fatalf("comparer reached leaf depth %d, want at most %d (calls=%d)", maxDepth, k, calls)
	}
	// Each reached leaf invokes the comparer at most twice (once plus the
	// periodic deterministic/symmetric validation), never beyond the budget.
	if want := 2 * k; calls > want {
		t.Fatalf("comparer invoked %d times, want at most %d", calls, want)
	}

	// A guard comparer that panics if ever reached for a deeper leaf proves
	// that no user callback runs beyond the exhaustion point.
	// Legitimate leaves reach depth k (their pair is the last charged one);
	// depth k+1 or deeper must never invoke a user callback.
	guard := FilterPath(func(p Path) bool {
		sf, ok := p.Last().(StructField)
		if ok && sf.Name() == "V" {
			mu.Lock()
			depth = indirectDepth(p)
			mu.Unlock()
			return depth > k
		}
		return false
	}, comparerForDepth(&mu, &calls, &maxDepth, &depth, k, fmt.Errorf("user comparer called after budget exhaustion")))
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("user callback ran after budget exhaustion: %v", r)
		}
	}()
	if r := EqualBudgeted(x, y, budget, Options{countingComparer, guard}); r.Status != StatusIncomplete {
		t.Fatalf("guarded run: got %s, want incomplete", r.Status)
	}
}

// TestEqualBudgetedCycles proves that revisited value pairs in reference
// cycles are neither charged nor recursively traversed.
func TestEqualBudgetedCycles(t *testing.T) {
	// Pointer self-cycle: node points to itself.
	cx := &budgetNode{}
	cx.Next = cx
	cy := &budgetNode{}
	cy.Next = cy
	for _, budget := range []int{3, 10, 1 << 20} {
		if got := EqualBudgeted(cx, cy, budget); got != (BudgetResult{Status: StatusEqual, Used: 3}) {
			t.Errorf("ptr cycle budget %d: got %+v, want {equal 3}", budget, got)
		}
	}
	if got := EqualBudgeted(cx, cy, 1); got != (BudgetResult{Status: StatusIncomplete, Used: 1}) {
		t.Errorf("ptr cycle budget 1: got %+v, want {incomplete 1}", got)
	}
	if got := EqualBudgeted(cx, cy, 2); got != (BudgetResult{Status: StatusIncomplete, Used: 2}) {
		t.Errorf("ptr cycle budget 2: got %+v, want {incomplete 2}", got)
	}

	// Inequal cycle shapes terminate and are reported as different.
	dy1, dy2 := &budgetNode{}, &budgetNode{}
	dy1.Next = dy2
	dy2.Next = dy1
	if got := EqualBudgeted(cx, dy1, 100); got.Status != StatusDifferent {
		t.Errorf("inequal cycles: got %s, want different", got.Status)
	}

	// Slice cycle: an element revisits the same slice on the path.
	sx := budgetCycleSlice{nil}
	sx[0] = sx
	sy := budgetCycleSlice{nil}
	sy[0] = sy
	if got := EqualBudgeted(sx, sy, 100); got != (BudgetResult{Status: StatusEqual, Used: 2}) {
		t.Errorf("slice cycle: got %+v, want {equal 2}", got)
	}

	// Map cycle: a map contains itself.
	mx := budgetCycleMap{0: nil}
	mx[0] = mx
	my := budgetCycleMap{0: nil}
	my[0] = my
	if got := EqualBudgeted(mx, my, 100); got != (BudgetResult{Status: StatusEqual, Used: 1}) {
		t.Errorf("map cycle: got %+v, want {equal 1}", got)
	}
}

type budgetCycleSlice []budgetCycleSlice
type budgetCycleMap map[int]budgetCycleMap

// TestEqualBudgetedIgnore proves the Ignore option is honored: the ignored
// subtree is not traversed or charged beyond the single ignored value pair.
func TestEqualBudgetedIgnore(t *testing.T) {
	x, y := makeChain(100)
	y.V = 42 // would otherwise differ
	ignoreNode := FilterPath(func(p Path) bool {
		_, ok := p.Last().(StructField)
		return !ok && indirectDepth(p) >= 1
	}, Ignore())
	// The first node (and, depending on the periodic option-validation pass,
	// the preceding Indirect) matches Ignore, so the long tail is skipped and
	// at most three primary pairs are charged.
	got := EqualBudgeted(x, y, 4, ignoreNode)
	if got.Status != StatusEqual || got.Used < 2 || got.Used > 3 {
		t.Errorf("ignore deep subtree: got %+v, want equal with 2 or 3 charged pairs", got)
	}
	// With a budget that does not reach the ignored node, comparison is
	// incomplete.
	if got := EqualBudgeted(x, y, 1, ignoreNode); got != (BudgetResult{Status: StatusIncomplete, Used: 1}) {
		t.Errorf("budget below ignore: got %+v, want {incomplete 1}", got)
	}
}

// TestEqualBudgetedTransformer proves Transformers integrate with the
// budget: both the Transform input and the transformed value pair are real
// comparisons and are charged.
func TestEqualBudgetedTransformer(t *testing.T) {
	x, y := makeChain(5)
	// Flatten each chain into a slice of its V values; the transformer is
	// invoked once per node and the resulting slices are fully compared.
	tr := Transformer("ChainVs", func(n *budgetNode) []int {
		var vs []int
		for c := n; c != nil; c = c.Next {
			vs = append(vs, c.V)
		}
		return vs
	})
	// The root is charged as the transformer input, the transformed slice and
	// each of its five elements is charged, for 7 primary pairs.
	got := EqualBudgeted(x, y, -1, tr)
	if got.Status != StatusEqual {
		t.Fatalf("transformed equal chains: got %+v, want equal", got)
	}
	if got.Used != 7 {
		t.Errorf("transformed traversal charged %d pairs, want 7", got.Used)
	}
	if got := EqualBudgeted(x, y, got.Used-1, tr); got.Status != StatusIncomplete {
		t.Errorf("transformer tight budget: got %s, want incomplete", got.Status)
	}

	// A difference survives the transformer and is reported as different.
	xd, yd := makeChain(5)
	yd.V = 7
	if got := EqualBudgeted(xd, yd, -1, tr); got.Status != StatusDifferent {
		t.Errorf("transformed differing chains: got %s, want different", got.Status)
	}
}

// TestEqualBudgetedReporter confirms reporters observe a balanced traversal
// even when it is truncated by the budget.
func TestEqualBudgetedReporter(t *testing.T) {
	x, y := makeChain(6)
	r := &budgetReporter{}
	got := EqualBudgeted(x, y, 5, Reporter(r))
	if got.Status != StatusIncomplete {
		t.Fatalf("got %s, want incomplete", got.Status)
	}
	if r.pushes != r.pops {
		t.Errorf("reporter unbalanced: %d pushes vs %d pops", r.pushes, r.pops)
	}
	if r.reports > got.Used {
		t.Errorf("reporter saw %d leaf reports for %d charged pairs", r.reports, got.Used)
	}
}

type budgetReporter struct {
	pushes, pops, reports int
}

func (r *budgetReporter) PushStep(PathStep) { r.pushes++ }
func (r *budgetReporter) Report(Result)     { r.reports++ }
func (r *budgetReporter) PopStep()          { r.pops++ }

// comparerForDepth returns a leaf Comparer that records every invocation and
// panics with panicAt if invoked at a depth greater than the deepest leaf the
// budget could reach. The mutex also covers the depth shared with the filter,
// which is read concurrently by the periodic validation goroutine.
func comparerForDepth(mu *sync.Mutex, calls, maxDepth, depth *int, panicAfterDepth int, panicAt error) Option {
	return Comparer(func(a, b int) bool {
		mu.Lock()
		defer mu.Unlock()
		*calls++
		if *depth > *maxDepth {
			*maxDepth = *depth
		}
		if panicAt != nil && *depth > panicAfterDepth {
			panic(panicAt)
		}
		return a == b
	})
}
