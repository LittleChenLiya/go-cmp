// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// absDiffLE1 is a symmetric equality that is not transitive and therefore
// not an equivalence relation: |x-y| <= 1.
func absDiffLE1() Option {
	return Comparer(func(x, y int) bool {
		d := x - y
		if d < 0 {
			d = -d
		}
		return d <= 1
	})
}

func TestMatchSlicesNonTransitive(t *testing.T) {
	// A greedy left-to-right pairing would match 2 with 2 and then fail
	// to pair 1 with 3; the maximum matching must succeed instead.
	rep, err := MatchSlices([]int{2, 1}, []int{2, 3}, MatchConfig{}, absDiffLE1())
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if !rep.Matched() {
		t.Fatalf("MatchSlices: not matched: %+v", rep)
	}
	want := []MatchEdge{{Left: 0, Right: 1}, {Left: 1, Right: 0}}
	if !reflect.DeepEqual(rep.Edges, want) {
		t.Errorf("edges mismatch: got %v, want %v", rep.Edges, want)
	}
	// The report must map indices back to source elements one-to-one:
	// left 2 corresponds to right 3, and left 1 corresponds to right 2.
	for _, e := range rep.Edges {
		l, r := rep.LeftValue(e.Left), rep.RightValue(e.Right)
		switch {
		case l == 2 && r != 3:
			t.Errorf("left 2 matched to right %d, want 3", r)
		case l == 1 && r != 2:
			t.Errorf("left 1 matched to right %d, want 2", r)
		}
	}
}

func TestMatchSlicesDuplicatesNotReusable(t *testing.T) {
	rep, err := MatchSlices([]int{1, 1}, []int{1, 2}, MatchConfig{})
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if rep.Matched() {
		t.Fatalf("MatchSlices: unexpectedly matched: %+v", rep)
	}
	// The right-hand 1 may be consumed only once.
	wantEdges := []MatchEdge{{Left: 0, Right: 0}}
	if !reflect.DeepEqual(rep.Edges, wantEdges) {
		t.Errorf("edges mismatch: got %v, want %v", rep.Edges, wantEdges)
	}
	if !reflect.DeepEqual(rep.UnmatchedLeft, []int{1}) {
		t.Errorf("unmatched left = %v, want [1]", rep.UnmatchedLeft)
	}
	if !reflect.DeepEqual(rep.UnmatchedRight, []int{1}) {
		t.Errorf("unmatched right = %v, want [1]", rep.UnmatchedRight)
	}
}

func TestMatchSlicesNestedStructs(t *testing.T) {
	type Inner struct {
		Name string
		Tags []string
	}
	type Outer struct {
		ID    int
		Inner Inner
	}
	x := []Outer{
		{ID: 1, Inner: Inner{Name: "a", Tags: []string{"x", "y"}}},
		{ID: 2, Inner: Inner{Name: "b"}},
	}
	y := []Outer{
		{ID: 2, Inner: Inner{Name: "b"}},
		{ID: 1, Inner: Inner{Name: "a", Tags: []string{"x", "y"}}},
	}
	rep, err := MatchSlices(x, y, MatchConfig{})
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if !rep.Matched() {
		t.Fatalf("MatchSlices: not matched: %+v", rep)
	}
	want := []MatchEdge{{Left: 0, Right: 1}, {Left: 1, Right: 0}}
	if !reflect.DeepEqual(rep.Edges, want) {
		t.Errorf("edges mismatch: got %v, want %v", rep.Edges, want)
	}
}

func TestMatchSlicesCyclicPointers(t *testing.T) {
	type Node struct {
		V    int
		Next *Node
	}
	cycle := func(vs ...int) *Node {
		head := &Node{V: vs[0]}
		cur := head
		for _, v := range vs[1:] {
			cur.Next = &Node{V: v}
			cur = cur.Next
		}
		cur.Next = head
		return head
	}
	x := []*Node{cycle(1, 2, 3), cycle(7, 8)}
	y := []*Node{cycle(8, 7), cycle(2, 3, 1)}
	// Rotations of a cycle are not pointer-identical but are equal under
	// a comparer that walks the cycle from the entry node.
	walk := Comparer(func(a, b *Node) bool {
		seen := map[*Node]bool{}
		aa, bb := []*Node{}, []*Node{}
		for n := a; !seen[n]; n = n.Next {
			seen[n] = true
			aa = append(aa, n)
		}
		seen = map[*Node]bool{}
		for n := b; !seen[n]; n = n.Next {
			seen[n] = true
			bb = append(bb, n)
		}
		if len(aa) != len(bb) {
			return false
		}
		as, bs := map[int]int{}, map[int]int{}
		for _, n := range aa {
			as[n.V]++
		}
		for _, n := range bb {
			bs[n.V]++
		}
		return reflect.DeepEqual(as, bs)
	})
	rep, err := MatchSlices(x, y, MatchConfig{}, walk)
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if !rep.Matched() {
		t.Fatalf("MatchSlices: not matched: %+v", rep)
	}
	want := []MatchEdge{{Left: 0, Right: 1}, {Left: 1, Right: 0}}
	if !reflect.DeepEqual(rep.Edges, want) {
		t.Errorf("edges mismatch: got %v, want %v", rep.Edges, want)
	}
}

func TestMatchSlicesFilterOption(t *testing.T) {
	type Rec struct {
		Name    string
		Ignored int
	}
	ignoreIgnored := FilterPath(func(p Path) bool {
		return p.Last().String() == ".Ignored"
	}, Ignore())
	x := []Rec{{Name: "a", Ignored: 1}, {Name: "b", Ignored: 2}}
	y := []Rec{{Name: "b", Ignored: 99}, {Name: "a", Ignored: -5}}
	rep, err := MatchSlices(x, y, MatchConfig{}, ignoreIgnored)
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if !rep.Matched() {
		t.Fatalf("MatchSlices: not matched: %+v", rep)
	}
	want := []MatchEdge{{Left: 0, Right: 1}, {Left: 1, Right: 0}}
	if !reflect.DeepEqual(rep.Edges, want) {
		t.Errorf("edges mismatch: got %v, want %v", rep.Edges, want)
	}
}

func TestMatchSlicesBudget(t *testing.T) {
	x := []int{3, 1, 4, 1, 5}
	y := []int{5, 4, 3, 1, 1}

	full, err := MatchSlices(x, y, MatchConfig{})
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if !full.Matched() {
		t.Fatalf("MatchSlices: not matched: %+v", full)
	}
	if full.Comparisons <= 0 {
		t.Fatalf("Comparisons = %d, want > 0", full.Comparisons)
	}
	if full.Comparisons > len(x)*len(y) {
		t.Fatalf("Comparisons = %d, exceeds n*m = %d", full.Comparisons, len(x)*len(y))
	}

	// A budget exactly equal to the number of comparisons needed suffices.
	exact, err := MatchSlices(x, y, MatchConfig{MaxComparisons: full.Comparisons})
	if err != nil {
		t.Fatalf("MatchSlices with exact budget: unexpected error: %v", err)
	}
	if !exact.Matched() {
		t.Fatalf("MatchSlices with exact budget: not matched: %+v", exact)
	}

	// One comparison fewer must fail with the dedicated budget error,
	// not with a plain mismatch.
	rep, err := MatchSlices(x, y, MatchConfig{MaxComparisons: full.Comparisons - 1})
	if !errors.Is(err, ErrMatchBudgetExceeded) {
		t.Fatalf("MatchSlices over budget: err = %v, want ErrMatchBudgetExceeded", err)
	}
	if rep.Matched() {
		t.Fatalf("MatchSlices over budget: unexpectedly matched")
	}
}

func TestMatchSlicesNilVsEmpty(t *testing.T) {
	var nilSlice []int
	empty := []int{}

	if _, err := MatchSlices(nilSlice, empty, MatchConfig{}); !IsNilVsEmpty(err) {
		t.Errorf("nil vs empty: err = %v, want IsNilVsEmpty", err)
	}
	rep, err := MatchSlices(nilSlice, empty, MatchConfig{NilEqualsEmpty: true})
	if err != nil {
		t.Errorf("nil vs empty with NilEqualsEmpty: unexpected error: %v", err)
	}
	if !rep.Matched() {
		t.Errorf("nil vs empty with NilEqualsEmpty: not matched")
	}
	if rep, err := MatchSlices(nilSlice, []int(nil), MatchConfig{}); err != nil || !rep.Matched() {
		t.Errorf("nil vs nil: rep.Matched() = %v, err = %v; want matched, nil", rep.Matched(), err)
	}
	if rep, err := MatchSlices(empty, []int{}, MatchConfig{}); err != nil || !rep.Matched() {
		t.Errorf("empty vs empty: rep.Matched() = %v, err = %v; want matched, nil", rep.Matched(), err)
	}
}

func TestMatchSlicesComparerPanicPropagates(t *testing.T) {
	boom := errors.New("boom")
	panicker := Comparer(func(x, y int) bool { panic(boom) })
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MatchSlices: panic did not propagate")
		}
		// The panic must propagate unchanged, not be wrapped into a
		// plain mismatch or some other error value.
		if err, ok := r.(error); !ok || !errors.Is(err, boom) {
			t.Fatalf("recovered %v (%T), want the original panic value", r, r)
		}
	}()
	MatchSlices([]int{1}, []int{1}, MatchConfig{}, panicker)
}

func TestMatchSlicesDoesNotMutateInput(t *testing.T) {
	x := []int{3, 1, 2}
	y := []int{2, 3, 1}
	xCopy, yCopy := append([]int(nil), x...), append([]int(nil), y...)
	rep, err := MatchSlices(x, y, MatchConfig{})
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if !rep.Matched() {
		t.Fatalf("MatchSlices: not matched: %+v", rep)
	}
	if !reflect.DeepEqual(x, xCopy) || !reflect.DeepEqual(y, yCopy) {
		t.Errorf("inputs modified: x = %v, y = %v", x, y)
	}
}

func ExampleMatchSlices() {
	// Equality here is "differ by at most one", which is not transitive,
	// so a plain sort-and-compare or greedy pairing would be wrong.
	near := Comparer(func(x, y int) bool {
		d := x - y
		if d < 0 {
			d = -d
		}
		return d <= 1
	})
	left := []int{2, 1, 10}
	right := []int{3, 2, 9}
	rep, err := MatchSlices(left, right, MatchConfig{}, near)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("left:", left)
	fmt.Println("right:", right)
	for _, e := range rep.Edges {
		fmt.Printf("edge: left[%d]=%d <-> right[%d]=%d\n",
			e.Left, rep.LeftValue(e.Left), e.Right, rep.RightValue(e.Right))
	}
	fmt.Println("unmatched left:", rep.UnmatchedLeft)
	fmt.Println("unmatched right:", rep.UnmatchedRight)

	// Output:
	// left: [2 1 10]
	// right: [3 2 9]
	// edge: left[0]=2 <-> right[0]=3
	// edge: left[1]=1 <-> right[1]=2
	// edge: left[2]=10 <-> right[2]=9
	// unmatched left: []
	// unmatched right: []
}
