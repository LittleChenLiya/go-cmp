package cmp_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// nearInt is a symmetric comparer that is deliberately not transitive:
// values within an absolute difference of 1 are considered equal.
func nearInt() cmp.Option {
	return cmp.Comparer(func(a, b int) bool {
		d := a - b
		return d >= -1 && d <= 1
	})
}

func TestMatchSlicesNonTransitive(t *testing.T) {
	// A greedy left-to-right pairing would match left[0]=2 with right[0]=2
	// and then fail on left[1]=1 vs right[1]=3. A maximum matching must
	// instead pair 2 with 3 and 1 with 2.
	left := []int{2, 1}
	right := []int{2, 3}
	m, err := cmp.MatchSlices(left, right, cmp.MatchSettings{}, nearInt())
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if !m.Perfect() {
		t.Fatalf("MatchSlices: not perfect: %+v", m)
	}
	if j, ok := m.RightOf(0); !ok || j != 1 {
		t.Errorf("left[0]=2 must match right[1]=3, got right[%d] (ok=%v)", j, ok)
	}
	if j, ok := m.RightOf(1); !ok || j != 0 {
		t.Errorf("left[1]=1 must match right[0]=2, got right[%d] (ok=%v)", j, ok)
	}
	if i, ok := m.LeftOf(0); !ok || i != 1 {
		t.Errorf("right[0]=2 must be matched from left[1]=1, got left[%d] (ok=%v)", i, ok)
	}
	want := []cmp.MatchPair{{Left: 0, Right: 1}, {Left: 1, Right: 0}}
	if !cmp.Equal(m.Pairs, want, cmp.AllowUnexported(cmp.MatchPair{})) {
		t.Errorf("Pairs = %v, want %v", m.Pairs, want)
	}
	// Inputs must be left untouched.
	if !cmp.Equal(left, []int{2, 1}) || !cmp.Equal(right, []int{2, 3}) {
		t.Errorf("inputs modified: left=%v right=%v", left, right)
	}
}

func TestMatchSlicesDuplicatesNotReused(t *testing.T) {
	// The single 1 on the right may only be consumed once.
	m, err := cmp.MatchSlices([]int{1, 1}, []int{1, 2}, cmp.MatchSettings{})
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if m.Perfect() {
		t.Fatalf("MatchSlices: unexpectedly perfect: %+v", m)
	}
	want := []cmp.MatchPair{{Left: 0, Right: 0}}
	if !cmp.Equal(m.Pairs, want, cmp.AllowUnexported(cmp.MatchPair{})) {
		t.Errorf("Pairs = %v, want %v", m.Pairs, want)
	}
	if !cmp.Equal(m.UnmatchedLeft, []int{1}) {
		t.Errorf("UnmatchedLeft = %v, want [1]", m.UnmatchedLeft)
	}
	if !cmp.Equal(m.UnmatchedRight, []int{1}) {
		t.Errorf("UnmatchedRight = %v, want [1]", m.UnmatchedRight)
	}
}

type matchNested struct {
	ID   int
	Name string
	Tags []string
}

func TestMatchSlicesNestedWithFilter(t *testing.T) {
	left := []matchNested{
		{ID: 1, Name: "a", Tags: []string{"x", "y"}},
		{ID: 2, Name: "b", Tags: nil},
	}
	right := []matchNested{
		{ID: 2, Name: "B", Tags: nil},
		{ID: 1, Name: "A", Tags: []string{"x", "y"}},
	}
	m, err := cmp.MatchSlices(left, right, cmp.MatchSettings{},
		cmpopts.IgnoreFields(matchNested{}, "Name"))
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if !m.Perfect() {
		t.Fatalf("MatchSlices: not perfect: %+v", m)
	}
	want := []cmp.MatchPair{{Left: 0, Right: 1}, {Left: 1, Right: 0}}
	if !cmp.Equal(m.Pairs, want, cmp.AllowUnexported(cmp.MatchPair{})) {
		t.Errorf("Pairs = %v, want %v", m.Pairs, want)
	}
}

type matchNode struct {
	V    int
	Next *matchNode
}

func matchCycle(v int) *matchNode {
	n := &matchNode{V: v}
	n.Next = n
	return n
}

func TestMatchSlicesCyclicPointers(t *testing.T) {
	left := []*matchNode{matchCycle(1), matchCycle(2)}
	right := []*matchNode{matchCycle(2), matchCycle(1)}
	m, err := cmp.MatchSlices(left, right, cmp.MatchSettings{})
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if !m.Perfect() {
		t.Fatalf("MatchSlices: not perfect: %+v", m)
	}
	want := []cmp.MatchPair{{Left: 0, Right: 1}, {Left: 1, Right: 0}}
	if !cmp.Equal(m.Pairs, want, cmp.AllowUnexported(cmp.MatchPair{})) {
		t.Errorf("Pairs = %v, want %v", m.Pairs, want)
	}
}

func TestMatchSlicesBudget(t *testing.T) {
	// [1,1] vs [1,2] needs exactly 4 distinct element-pair comparisons.
	m, err := cmp.MatchSlices([]int{1, 1}, []int{1, 2}, cmp.MatchSettings{MaxComparisons: 4})
	if err != nil {
		t.Fatalf("MatchSlices: budget exactly exhausted must not error, got %v", err)
	}
	if m.Comparisons != 4 {
		t.Errorf("Comparisons = %d, want 4", m.Comparisons)
	}
	if m.Perfect() {
		t.Errorf("MatchSlices: unexpectedly perfect: %+v", m)
	}

	// One less than required must yield a distinct budget error, not false.
	_, err = cmp.MatchSlices([]int{1, 1}, []int{1, 2}, cmp.MatchSettings{MaxComparisons: 3})
	var berr cmp.BudgetExceededError
	if !errors.As(err, &berr) {
		t.Fatalf("err = %v (%T), want cmp.BudgetExceededError", err, err)
	}
	if berr.Limit != 3 {
		t.Errorf("BudgetExceededError.Limit = %d, want 3", berr.Limit)
	}

	// A budget of 1 suffices for a single matching pair.
	m, err = cmp.MatchSlices([]int{7}, []int{7}, cmp.MatchSettings{MaxComparisons: 1})
	if err != nil || !m.Perfect() {
		t.Errorf("MatchSlices: got (%+v, %v), want perfect match", m, err)
	}
}

func TestMatchSlicesComparerPanicPropagates(t *testing.T) {
	boom := cmp.Comparer(func(a, b int) bool {
		if a == 13 {
			panic("boom")
		}
		return a == b
	})
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic to propagate, got none")
		}
		if s, ok := r.(string); !ok || s != "boom" {
			t.Fatalf("panic = %#v, want unwrapped \"boom\"", r)
		}
	}()
	cmp.MatchSlices([]int{13}, []int{13}, cmp.MatchSettings{}, boom)
}

func TestMatchSlicesNilVsEmpty(t *testing.T) {
	// nil and empty differ by default.
	m, err := cmp.MatchSlices([]int(nil), []int{}, cmp.MatchSettings{})
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if m.Perfect() {
		t.Errorf("nil vs empty: Perfect() = true, want false")
	}

	// They can explicitly be equated.
	m, err = cmp.MatchSlices([]int(nil), []int{}, cmp.MatchSettings{NilEqualsEmpty: true})
	if err != nil {
		t.Fatalf("MatchSlices: unexpected error: %v", err)
	}
	if !m.Perfect() {
		t.Errorf("nil vs empty with NilEqualsEmpty: Perfect() = false, want true")
	}

	// nil vs nil and empty vs empty are always perfect.
	for _, tc := range [][2][]int{{nil, nil}, {{}, {}}} {
		m, err := cmp.MatchSlices(tc[0], tc[1], cmp.MatchSettings{})
		if err != nil || !m.Perfect() {
			t.Errorf("MatchSlices(%v, %v) = (%+v, %v), want perfect", tc[0], tc[1], m, err)
		}
	}
}
