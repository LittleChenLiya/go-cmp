// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// intPair contains two pointers that may or may not share the same object.
type intPair struct {
	A *int
	B *int
}

func sharedPair(v int) intPair {
	return intPair{A: &v, B: &v}
}

func independentPair(v int) intPair {
	a, b := v, v
	return intPair{A: &a, B: &b}
}

func TestAliasTopologySharedVsIndependent(t *testing.T) {
	tests := []struct {
		name        string
		x, y        intPair
		wantDefault bool // expected result without AliasTopology
		want        bool // expected result with AliasTopology
	}{
		{"SharedVsShared", sharedPair(1), sharedPair(1), true, true},
		{"IndependentVsIndependent", independentPair(1), independentPair(1), true, true},
		{"SharedVsIndependent", sharedPair(1), independentPair(1), true, false},
		{"IndependentVsShared", independentPair(1), sharedPair(1), true, false},
		{"SharedVsSharedDifferentValue", sharedPair(1), sharedPair(2), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The default comparison only compares values and must be
			// unaffected by the presence of the new option.
			if got := cmp.Equal(tt.x, tt.y); got != tt.wantDefault {
				t.Errorf("default Equal(%v, %v) = %v, want %v", tt.x, tt.y, got, tt.wantDefault)
			}
			if got := cmp.Equal(tt.x, tt.y, cmp.AliasTopology()); got != tt.want {
				t.Errorf("Equal(%v, %v, AliasTopology()) = %v, want %v", tt.x, tt.y, got, tt.want)
			}
			if diff := cmp.Diff(tt.x, tt.y, cmp.AliasTopology()); (diff == "") == !tt.want {
				t.Errorf("Diff(%v, %v, AliasTopology()) = %q, want non-empty: %v", tt.x, tt.y, diff, !tt.want)
			}
		})
	}
}

// loopNode is a self-referential linked list node.
type loopNode struct {
	Val  int
	Next *loopNode
}

func selfLoop(v int) *loopNode {
	n := &loopNode{Val: v}
	n.Next = n
	return n
}

func TestAliasTopologySelfLoop(t *testing.T) {
	// Isomorphic self-loops must compare equal; cycles terminate through
	// paired identity lookup rather than address comparison.
	x, y := selfLoop(1), selfLoop(1)
	if !cmp.Equal(x, y, cmp.AliasTopology()) {
		t.Errorf("Equal(selfLoop, selfLoop, AliasTopology()) = false, want true")
	}
	if diff := cmp.Diff(x, y, cmp.AliasTopology()); diff != "" {
		t.Errorf("Diff(selfLoop, selfLoop, AliasTopology()) = %q, want empty", diff)
	}

	// Non-isomorphic loops: x loops back to the root, while y loops back
	// to a distinct inner node with the same contents.
	inner := &loopNode{Val: 1}
	inner.Next = inner
	z := &loopNode{Val: 1, Next: inner}
	if cmp.Equal(x, z, cmp.AliasTopology()) {
		t.Errorf("Equal(selfLoop, shiftedLoop, AliasTopology()) = true, want false")
	}
	// The default comparison already detects the structural difference
	// through its path-based cycle detection.
	if cmp.Equal(x, z) {
		t.Errorf("default Equal(selfLoop, shiftedLoop) = true, want false")
	}
}

// branchPair holds the same pointers reachable through different branches.
type branchPair struct {
	L, R intPair
}

func TestAliasTopologyCrossBranch(t *testing.T) {
	// The identity mapping is global: a pointer pair recorded in one
	// branch is re-checked when encountered again in another branch.
	shared := sharedPair(1)
	indep := independentPair(1)

	x := branchPair{L: shared, R: intPair{A: shared.A, B: shared.B}}
	y := branchPair{L: indep, R: intPair{A: indep.A, B: indep.B}}
	if cmp.Equal(x, y, cmp.AliasTopology()) {
		t.Errorf("Equal(crossBranchShared, crossBranchIndependent, AliasTopology()) = true, want false")
	}
	if cmp.Equal(y, x, cmp.AliasTopology()) {
		t.Errorf("Equal(crossBranchIndependent, crossBranchShared, AliasTopology()) = true, want false")
	}
	if !cmp.Equal(x, y) {
		t.Errorf("default Equal(crossBranchShared, crossBranchIndependent) = false, want true")
	}

	// Consistent sharing across branches is accepted.
	y2shared := sharedPair(1)
	x2 := branchPair{L: shared, R: intPair{A: shared.A, B: shared.B}}
	y2 := branchPair{L: y2shared, R: intPair{A: y2shared.A, B: y2shared.B}}
	if !cmp.Equal(x2, y2, cmp.AliasTopology()) {
		t.Errorf("Equal(consistent, consistent, AliasTopology()) = false, want true")
	}
}

func TestAliasTopologyNil(t *testing.T) {
	v := 1
	tests := []struct {
		name string
		x, y intPair
		want bool
	}{
		{"BothNil", intPair{}, intPair{}, true},
		{"NilVsNonNil", intPair{}, intPair{A: &v, B: &v}, false},
		{"NonNilVsNil", intPair{A: &v, B: &v}, intPair{}, false},
		{"PartialNil", intPair{A: &v}, intPair{A: &v}, true},
		{"PartialNilMismatch", intPair{A: &v}, intPair{B: &v}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cmp.Equal(tt.x, tt.y, cmp.AliasTopology()); got != tt.want {
				t.Errorf("Equal(%v, %v, AliasTopology()) = %v, want %v", tt.x, tt.y, got, tt.want)
			}
		})
	}
}

func TestAliasTopologyIgnore(t *testing.T) {
	// Paths skipped by Ignore never establish an identity mapping, so
	// inconsistent sharing beneath an ignored field is not detected.
	x := sharedPair(1)
	y := independentPair(1)
	ignoreB := cmp.FilterPath(func(p cmp.Path) bool {
		return p.String() == "B"
	}, cmp.Ignore())
	if !cmp.Equal(x, y, cmp.AliasTopology(), ignoreB) {
		t.Errorf("Equal(shared, independent, AliasTopology(), Ignore(.B)) = false, want true")
	}
	// Ignoring .A also hides the mismatch: the sharing relation between
	// .A and .B can only be checked when both sides are compared.
	ignoreA := cmp.FilterPath(func(p cmp.Path) bool {
		return p.String() == "A"
	}, cmp.Ignore())
	if !cmp.Equal(x, y, cmp.AliasTopology(), ignoreA) {
		t.Errorf("Equal(shared, independent, AliasTopology(), Ignore(.A)) = false, want true")
	}
	// Without any Ignore, the inconsistent sharing is detected.
	if cmp.Equal(x, y, cmp.AliasTopology()) {
		t.Errorf("Equal(shared, independent, AliasTopology()) = true, want false")
	}
}

func TestAliasTopologyComparer(t *testing.T) {
	// A Comparer takes over the sub-tree rooted at *int; no identity
	// mappings are established for pointers compared by it.
	x := sharedPair(1)
	y := independentPair(1)
	byValue := cmp.Comparer(func(x, y *int) bool {
		if x == nil || y == nil {
			return x == y
		}
		return *x == *y
	})
	if !cmp.Equal(x, y, cmp.AliasTopology(), byValue) {
		t.Errorf("Equal(shared, independent, AliasTopology(), Comparer) = false, want true")
	}
}

func TestAliasTopologyTransformer(t *testing.T) {
	// A Transformer takes over the sub-tree; the transformed output is
	// compared as a fresh tree and establishes no mapping for the
	// original pointers.
	x := sharedPair(1)
	y := independentPair(1)
	deref := cmp.Transformer("Deref", func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	})
	if !cmp.Equal(x, y, cmp.AliasTopology(), deref) {
		t.Errorf("Equal(shared, independent, AliasTopology(), Transformer) = false, want true")
	}
}

func TestAliasTopologyDiff(t *testing.T) {
	// The Diff output must mark the path where the sharing relation breaks.
	x := sharedPair(1)
	y := independentPair(1)
	diff := cmp.Diff(x, y, cmp.AliasTopology())
	if diff == "" {
		t.Fatalf("Diff(shared, independent, AliasTopology()) = empty, want non-empty")
	}
	var marked bool
	for _, line := range strings.Split(diff, "\n") {
		trimmed := strings.TrimSpace(line)
		if (strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "+")) && strings.Contains(trimmed, "B:") {
			marked = true
		}
	}
	if !marked {
		t.Errorf("Diff output does not mark field B as differing:\n%s", diff)
	}

	// Default Diff remains empty for the same values.
	if diff := cmp.Diff(x, y); diff != "" {
		t.Errorf("default Diff(shared, independent) = %q, want empty", diff)
	}
}

// aliasResultRecorder records the results reported for each leaf node.
type aliasResultRecorder struct {
	byAlias int
	total   int
}

func (r *aliasResultRecorder) PushStep(cmp.PathStep) {}
func (r *aliasResultRecorder) PopStep()              {}
func (r *aliasResultRecorder) Report(res cmp.Result) {
	r.total++
	if res.ByAlias() {
		r.byAlias++
	}
}

func TestAliasTopologyByAliasResult(t *testing.T) {
	// The second visit of a consistently shared pointer pair is reported
	// with ByAlias set, since equality is determined by identity.
	x := sharedPair(1)
	y := sharedPair(1)
	rec := new(aliasResultRecorder)
	if !cmp.Equal(x, y, cmp.AliasTopology(), cmp.Reporter(rec)) {
		t.Fatalf("Equal(shared, shared, AliasTopology()) = false, want true")
	}
	if rec.byAlias == 0 {
		t.Errorf("no results reported with ByAlias, want at least one")
	}

	// Without the option, ByAlias is never set.
	rec = new(aliasResultRecorder)
	if !cmp.Equal(x, y, cmp.Reporter(rec)) {
		t.Fatalf("default Equal(shared, shared) = false, want true")
	}
	if rec.byAlias != 0 {
		t.Errorf("default comparison reported %d ByAlias results, want zero", rec.byAlias)
	}
}

// ExampleAliasTopology demonstrates the difference between the default
// value-based comparison and the AliasTopology option, which additionally
// requires both values to share the same pointer aliasing structure.
func ExampleAliasTopology() {
	shared := func(v int) intPair { return intPair{A: &v, B: &v} }
	independent := func(v int) intPair {
		a, b := v, v
		return intPair{A: &a, B: &b}
	}

	x := shared(1)      // x.A and x.B point to the same object
	y := independent(1) // y.A and y.B point to distinct objects with value 1

	fmt.Println("default Equal:", cmp.Equal(x, y))
	fmt.Println("AliasTopology Equal:", cmp.Equal(x, y, cmp.AliasTopology()))
	fmt.Println("Diff marks the broken sharing:")
	fmt.Print(cmp.Diff(x, y, cmp.AliasTopology()))

	// Output:
	// default Equal: true
	// AliasTopology Equal: false
	// Diff marks the broken sharing:
	//   cmp_test.intPair{
	//   	A: &1,
	// - 	B: &⟪0xdeadf00f⟫1,
	// + 	B: &⟪0xdeadf00f⟫1,
	//   }
}
