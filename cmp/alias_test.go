// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// aliasPair contains two pointer fields that may or may not alias
// the same object.
type aliasPair struct {
	A *int
	B *int
}

func sharedPair(v int) aliasPair {
	return aliasPair{A: &v, B: &v}
}

func distinctPair(v int) aliasPair {
	v2 := v
	return aliasPair{A: &v, B: &v2}
}

// TestAliasTopologyShared verifies the primary acceptance scenario:
// both sides have identical field values, but one side shares a pointer
// while the other side uses two independent objects. The default
// comparison reports equal, while AliasTopology reports unequal.
func TestAliasTopologyShared(t *testing.T) {
	tests := []struct {
		name string
		x, y aliasPair
	}{{
		name: "SharedXDistinctY",
		x:    sharedPair(1),
		y:    distinctPair(1),
	}, {
		name: "DistinctXSharedY",
		x:    distinctPair(1),
		y:    sharedPair(1),
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !cmp.Equal(tt.x, tt.y) {
				t.Errorf("default Equal = false, want true")
			}
			if diff := cmp.Diff(tt.x, tt.y); diff != "" {
				t.Errorf("default Diff = %q, want empty", diff)
			}
			if cmp.Equal(tt.x, tt.y, cmp.AliasTopology()) {
				t.Errorf("Equal with AliasTopology = true, want false")
			}
			diff := cmp.Diff(tt.x, tt.y, cmp.AliasTopology())
			if diff == "" {
				t.Fatalf("Diff with AliasTopology = empty, want non-empty")
			}
			// The diff must mark the path that breaks the sharing
			// relationship, which is the second field B.
			if !strings.Contains(diff, "B:") {
				t.Errorf("Diff with AliasTopology does not mark field B:\n%s", diff)
			}
		})
	}
}

// TestAliasTopologyIdentical verifies that values with the same aliasing
// topology remain equal when the option is enabled.
func TestAliasTopologyIdentical(t *testing.T) {
	tests := []struct {
		name string
		x, y aliasPair
	}{{
		name: "BothShared",
		x:    sharedPair(1),
		y:    sharedPair(1),
	}, {
		name: "BothDistinct",
		x:    distinctPair(1),
		y:    distinctPair(1),
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !cmp.Equal(tt.x, tt.y, cmp.AliasTopology()) {
				t.Errorf("Equal with AliasTopology = false, want true\nDiff:\n%s", cmp.Diff(tt.x, tt.y, cmp.AliasTopology()))
			}
		})
	}
}

// aliasNode is a node in a possibly cyclic linked list.
type aliasNode struct {
	Value int
	Next  *aliasNode
}

// TestAliasTopologyCycles verifies that isomorphic cyclic graphs are equal
// and that cycles terminate through paired-identity visits.
func TestAliasTopologyCycles(t *testing.T) {
	selfLoop := func(v int) *aliasNode {
		n := &aliasNode{Value: v}
		n.Next = n
		return n
	}

	t.Run("IsomorphicSelfLoops", func(t *testing.T) {
		x, y := selfLoop(1), selfLoop(1)
		if !cmp.Equal(x, y, cmp.AliasTopology()) {
			t.Errorf("Equal with AliasTopology = false, want true\nDiff:\n%s", cmp.Diff(x, y, cmp.AliasTopology()))
		}
	})

	t.Run("IsomorphicLongerCycles", func(t *testing.T) {
		newCycle := func() *aliasNode {
			a := &aliasNode{Value: 1}
			b := &aliasNode{Value: 2}
			a.Next = b
			b.Next = a
			return a
		}
		x, y := newCycle(), newCycle()
		if !cmp.Equal(x, y, cmp.AliasTopology()) {
			t.Errorf("Equal with AliasTopology = false, want true\nDiff:\n%s", cmp.Diff(x, y, cmp.AliasTopology()))
		}
	})

	t.Run("NonIsomorphicCycles", func(t *testing.T) {
		// x is a self-loop; y loops through a second node.
		x := selfLoop(1)
		y1 := &aliasNode{Value: 1}
		y2 := &aliasNode{Value: 1}
		y1.Next = y2
		y2.Next = y2
		if cmp.Equal(x, y1, cmp.AliasTopology()) {
			t.Errorf("Equal with AliasTopology = true, want false")
		}
	})

	t.Run("SharedCycleEntry", func(t *testing.T) {
		// Both fields point at the same cyclic list on both sides.
		type pair struct {
			A, B *aliasNode
		}
		nx, ny := selfLoop(1), selfLoop(1)
		x := pair{A: nx, B: nx}
		y := pair{A: ny, B: ny}
		if !cmp.Equal(x, y, cmp.AliasTopology()) {
			t.Errorf("Equal with AliasTopology = false, want true\nDiff:\n%s", cmp.Diff(x, y, cmp.AliasTopology()))
		}
	})
}

// TestAliasTopologyNil verifies that nil pointers establish no mapping and
// retain the existing nil semantics.
func TestAliasTopologyNil(t *testing.T) {
	v := 1
	tests := []struct {
		name string
		x, y aliasPair
		want bool
	}{{
		name: "BothNil",
		x:    aliasPair{},
		y:    aliasPair{},
		want: true,
	}, {
		name: "NilAndNonNilShared",
		x:    aliasPair{A: nil, B: &v},
		y:    sharedPair(1),
		want: false,
	}, {
		name: "MatchingNil",
		x:    aliasPair{A: nil, B: &v},
		y:    aliasPair{A: nil, B: &v},
		want: true,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cmp.Equal(tt.x, tt.y, cmp.AliasTopology()); got != tt.want {
				t.Errorf("Equal with AliasTopology = %v, want %v\nDiff:\n%s", got, tt.want, cmp.Diff(tt.x, tt.y, cmp.AliasTopology()))
			}
		})
	}
}

// TestAliasTopologyIgnore verifies that paths skipped by an Ignore option
// establish no identity mapping.
func TestAliasTopologyIgnore(t *testing.T) {
	x := sharedPair(1)
	y := distinctPair(1)
	ignoreB := cmp.FilterPath(func(p cmp.Path) bool {
		return p.String() == "B"
	}, cmp.Ignore())
	if !cmp.Equal(x, y, cmp.AliasTopology(), ignoreB) {
		t.Errorf("Equal with AliasTopology and ignored B = false, want true\nDiff:\n%s",
			cmp.Diff(x, y, cmp.AliasTopology(), ignoreB))
	}
}

// TestAliasTopologyComparer verifies that a Comparer takes over the subtree
// as a leaf and establishes no identity mappings within it.
func TestAliasTopologyComparer(t *testing.T) {
	x := sharedPair(1)
	y := distinctPair(1)
	byValue := cmp.Comparer(func(x, y *int) bool {
		if x == nil || y == nil {
			return x == y
		}
		return *x == *y
	})
	if !cmp.Equal(x, y, cmp.AliasTopology(), byValue) {
		t.Errorf("Equal with AliasTopology and Comparer = false, want true\nDiff:\n%s",
			cmp.Diff(x, y, cmp.AliasTopology(), byValue))
	}
}

// TestAliasTopologyTransformer verifies that a Transformer takes over the
// subtree and only the transformed output participates in the topology.
func TestAliasTopologyTransformer(t *testing.T) {
	x := sharedPair(1)
	y := distinctPair(1)
	deref := cmp.Transformer("Deref", func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	})
	if !cmp.Equal(x, y, cmp.AliasTopology(), deref) {
		t.Errorf("Equal with AliasTopology and Transformer = false, want true\nDiff:\n%s",
			cmp.Diff(x, y, cmp.AliasTopology(), deref))
	}
}

// TestAliasTopologySlice verifies that pointer elements within slices are
// tracked, while the slice backing array itself is not part of the topology.
// This also exercises the rollback of mappings established by the
// speculative comparisons performed by the slice differencing logic.
func TestAliasTopologySlice(t *testing.T) {
	p := 1
	q1, q2 := 1, 1
	q := 1

	t.Run("SharedElements", func(t *testing.T) {
		x := []*int{&p, &p, &p}
		y := []*int{&q, &q, &q}
		if !cmp.Equal(x, y, cmp.AliasTopology()) {
			t.Errorf("Equal with AliasTopology = false, want true\nDiff:\n%s", cmp.Diff(x, y, cmp.AliasTopology()))
		}
	})

	t.Run("SharedVsDistinctElements", func(t *testing.T) {
		x := []*int{&p, &p}
		y := []*int{&q1, &q2}
		if !cmp.Equal(x, y) {
			t.Errorf("default Equal = false, want true")
		}
		if cmp.Equal(x, y, cmp.AliasTopology()) {
			t.Errorf("Equal with AliasTopology = true, want false")
		}
	})

	t.Run("DistinctElements", func(t *testing.T) {
		p2 := 1
		x := []*int{&p, &p2}
		y := []*int{&q1, &q2}
		if !cmp.Equal(x, y, cmp.AliasTopology()) {
			t.Errorf("Equal with AliasTopology = false, want true\nDiff:\n%s", cmp.Diff(x, y, cmp.AliasTopology()))
		}
	})
}

// TestAliasTopologyMap verifies that pointer values within maps are tracked,
// while the internal addresses of the map itself are not part of the
// topology.
func TestAliasTopologyMap(t *testing.T) {
	p := 1
	q := 1
	q1, q2 := 1, 1

	t.Run("SharedValues", func(t *testing.T) {
		x := map[string]*int{"a": &p, "b": &p}
		y := map[string]*int{"a": &q, "b": &q}
		if !cmp.Equal(x, y, cmp.AliasTopology()) {
			t.Errorf("Equal with AliasTopology = false, want true\nDiff:\n%s", cmp.Diff(x, y, cmp.AliasTopology()))
		}
	})

	t.Run("SharedVsDistinctValues", func(t *testing.T) {
		x := map[string]*int{"a": &p, "b": &p}
		y := map[string]*int{"a": &q1, "b": &q2}
		if !cmp.Equal(x, y) {
			t.Errorf("default Equal = false, want true")
		}
		if cmp.Equal(x, y, cmp.AliasTopology()) {
			t.Errorf("Equal with AliasTopology = true, want false")
		}
	})
}

// TestAliasTopologyInterface verifies that pointers hidden behind interfaces
// participate in the topology.
func TestAliasTopologyInterface(t *testing.T) {
	p := 1
	q1, q2 := 1, 1
	x := struct{ V any }{V: &p}
	y := struct{ V any }{V: &q1}
	if !cmp.Equal(x, y, cmp.AliasTopology()) {
		t.Errorf("Equal with AliasTopology = false, want true")
	}

	type pair struct {
		A, B any
	}
	x2 := pair{A: &p, B: &p}
	y2 := pair{A: &q1, B: &q2}
	if cmp.Equal(x2, y2, cmp.AliasTopology()) {
		t.Errorf("Equal with AliasTopology = true, want false")
	}
}

// TestAliasTopologyDiff verifies that Diff marks the exact path that breaks
// the sharing relationship.
func TestAliasTopologyDiff(t *testing.T) {
	type nested struct {
		Inner aliasPair
	}
	x := nested{Inner: sharedPair(1)}
	y := nested{Inner: distinctPair(1)}

	diff := cmp.Diff(x, y, cmp.AliasTopology())
	if diff == "" {
		t.Fatalf("Diff with AliasTopology = empty, want non-empty")
	}
	if !strings.Contains(diff, "B:") {
		t.Errorf("Diff does not mark the field breaking sharing (Inner.B):\n%s", diff)
	}
	if diff := cmp.Diff(x, y); diff != "" {
		t.Errorf("default Diff = %q, want empty", diff)
	}
}
