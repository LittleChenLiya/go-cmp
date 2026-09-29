// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type policyItem struct {
	Name  string
	Price float64
}

type policyOrder struct {
	ID    string
	Items []policyItem
}

func TestPolicyTolerancePrecedence(t *testing.T) {
	opt, err := cmp.NewPolicy[policyOrder]().
		Tolerance("Items[*].Price", 0.01).
		Tolerance("Items[0].Price", 0).
		Compile()
	if err != nil {
		t.Fatalf("Compile: unexpected error: %v", err)
	}

	x := policyOrder{ID: "A", Items: []policyItem{
		{Name: "first", Price: 1.000},
		{Name: "second", Price: 2.000},
	}}
	y := policyOrder{ID: "A", Items: []policyItem{
		{Name: "first", Price: 1.001},
		{Name: "second", Price: 2.001},
	}}

	// The first item falls under the exact Items[0].Price rule with zero
	// tolerance, so a difference of 0.001 must be reported as unequal.
	if cmp.Equal(x, y, opt) {
		t.Errorf("Equal: got true, want false; Items[0].Price has zero tolerance")
	}

	// The second item falls under the wildcard Items[*].Price rule with a
	// tolerance of 0.01, so a difference of 0.001 must be accepted.
	x.Items[0].Price = y.Items[0].Price
	if !cmp.Equal(x, y, opt) {
		t.Errorf("Equal: got false, want true; Items[1].Price within 0.01 tolerance")
	}
	if diff := cmp.Diff(x, y, opt); diff != "" {
		t.Errorf("Diff: got %q, want empty", diff)
	}
}

func TestPolicyIgnore(t *testing.T) {
	opt, err := cmp.NewPolicy[policyOrder]().
		Ignore("ID").
		Ignore("Items[*].Name").
		Compile()
	if err != nil {
		t.Fatalf("Compile: unexpected error: %v", err)
	}
	x := policyOrder{ID: "A", Items: []policyItem{{Name: "x", Price: 1}}}
	y := policyOrder{ID: "B", Items: []policyItem{{Name: "y", Price: 1}}}
	if !cmp.Equal(x, y, opt) {
		t.Errorf("Equal: got false, want true; ID and Names are ignored")
	}
	y.Items[0].Price = 2
	if cmp.Equal(x, y, opt) {
		t.Errorf("Equal: got true, want false; Price is not ignored")
	}
}

func TestPolicyNestedPointers(t *testing.T) {
	type Inner struct {
		Value float64
	}
	type Middle struct {
		Inner *Inner
	}
	type Outer struct {
		Middle **Middle
	}

	opt, err := cmp.NewPolicy[Outer]().
		Tolerance("Middle.Inner.Value", 0.5).
		Compile()
	if err != nil {
		t.Fatalf("Compile: unexpected error: %v", err)
	}

	mk := func(v float64) Outer {
		m := &Middle{Inner: &Inner{Value: v}}
		return Outer{Middle: &m}
	}
	if !cmp.Equal(mk(1.0), mk(1.4), opt) {
		t.Errorf("Equal: got false, want true; difference within tolerance")
	}
	if cmp.Equal(mk(1.0), mk(1.6), opt) {
		t.Errorf("Equal: got true, want false; difference exceeds tolerance")
	}
}

func TestPolicyRecursiveStruct(t *testing.T) {
	type Node struct {
		Value float64
		Next  *Node
	}

	opt, err := cmp.NewPolicy[Node]().
		Tolerance("Value", 0).
		Tolerance("Next.Next.Value", 0.5).
		Compile()
	if err != nil {
		t.Fatalf("Compile: unexpected error: %v", err)
	}

	x := Node{Value: 1, Next: &Node{Value: 2, Next: &Node{Value: 3.0}}}
	y := Node{Value: 1, Next: &Node{Value: 2, Next: &Node{Value: 3.4}}}
	if !cmp.Equal(x, y, opt) {
		t.Errorf("Equal: got false, want true; nested difference within tolerance")
	}
	y.Next.Value = 2.1
	if cmp.Equal(x, y, opt) {
		t.Errorf("Equal: got true, want false; Next.Value has no tolerance rule")
	}
}

func TestPolicyNamedFloatType(t *testing.T) {
	type Celsius float64
	type Reading struct {
		Temp Celsius
	}

	opt, err := cmp.NewPolicy[Reading]().
		Tolerance("Temp", 0.25).
		Compile()
	if err != nil {
		t.Fatalf("Compile: unexpected error: %v", err)
	}
	if !cmp.Equal(Reading{Temp: 36.6}, Reading{Temp: 36.8}, opt) {
		t.Errorf("Equal: got false, want true; named float within tolerance")
	}
	if cmp.Equal(Reading{Temp: 36.6}, Reading{Temp: 37.0}, opt) {
		t.Errorf("Equal: got true, want false; named float exceeds tolerance")
	}
}

func TestPolicyConflictingRules(t *testing.T) {
	_, err := cmp.NewPolicy[policyOrder]().
		Tolerance("Items[*].Price", 0.01).
		Ignore("Items[*].Price").
		Compile()
	if err == nil {
		t.Fatal("Compile: got nil error, want conflict")
	}
	for _, want := range []string{
		`Tolerance("Items[*].Price", 0.01)`,
		`Ignore("Items[*].Price")`,
		"rule 1",
		"rule 2",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Compile error %q does not mention %q", err, want)
		}
	}
}

func TestPolicySpecificOverridesWildcard(t *testing.T) {
	// A concrete index rule and a wildcard rule overlap, but the concrete
	// rule is strictly more specific, so this is not a conflict.
	if _, err := cmp.NewPolicy[policyOrder]().
		Tolerance("Items[*].Price", 0.01).
		Tolerance("Items[0].Price", 0).
		Compile(); err != nil {
		t.Fatalf("Compile: unexpected error: %v", err)
	}
}

func TestPolicyInvalidPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
		tol  float64
		want []string
	}{{
		name: "unknown field",
		path: "Items[*].Cost",
		tol:  0.1,
		want: []string{"segment 2", "no field \"Cost\""},
	}, {
		name: "index into non-slice",
		path: "Items[0].Price[1]",
		tol:  0.1,
		want: []string{"segment 2", "cannot index"},
	}, {
		name: "field of non-struct",
		path: "Items.Price",
		tol:  0.1,
		want: []string{"segment 2", "non-struct"},
	}, {
		name: "tolerance on non-float",
		path: "ID",
		tol:  0.1,
		want: []string{"segment 1", "requires a float type"},
	}, {
		name: "negative tolerance",
		path: "Items[*].Price",
		tol:  -0.5,
		want: []string{"segment 2", "non-negative"},
	}, {
		name: "malformed syntax",
		path: "Items[].Price",
		tol:  0.1,
		want: []string{"segment 1", "malformed"},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cmp.NewPolicy[policyOrder]().
				Tolerance(tt.path, tt.tol).
				Compile()
			if err == nil {
				t.Fatalf("Compile: got nil error, want error containing %q", tt.want)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Compile error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestPolicyArrayAndMultiIndex(t *testing.T) {
	type Matrix struct {
		Grid [2][2]float64
	}
	opt, err := cmp.NewPolicy[Matrix]().
		Tolerance("Grid[*][1]", 0.5).
		Compile()
	if err != nil {
		t.Fatalf("Compile: unexpected error: %v", err)
	}
	x := Matrix{Grid: [2][2]float64{{1, 2}, {3, 4}}}
	y := Matrix{Grid: [2][2]float64{{1, 2.4}, {3, 4.4}}}
	if !cmp.Equal(x, y, opt) {
		t.Errorf("Equal: got false, want true; column 1 within tolerance")
	}
	y.Grid[0][0] = 1.1
	if cmp.Equal(x, y, opt) {
		t.Errorf("Equal: got true, want false; column 0 has no tolerance")
	}

	if _, err := cmp.NewPolicy[Matrix]().
		Tolerance("Grid[2][0]", 0.1).
		Compile(); err == nil {
		t.Errorf("Compile: got nil error, want out-of-bounds error")
	}
}
