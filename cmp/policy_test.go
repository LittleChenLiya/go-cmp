package cmp_test

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type policyItem struct {
	SKU   string
	Price float64
}

type policyOrder struct {
	ID    string
	Items []policyItem
}

func compilePolicy(t *testing.T, p *cmp.Policy) cmp.Option {
	t.Helper()
	opt, err := p.Compile()
	if err != nil {
		t.Fatalf("Compile() unexpected error: %v", err)
	}
	return opt
}

func TestPolicyTolerancePrecedence(t *testing.T) {
	opt := compilePolicy(t, cmp.NewPolicy(reflect.TypeOf(policyOrder{})).
		Tolerance("Items[*].Price", 0.01).
		Tolerance("Items[0].Price", 0))

	x := policyOrder{ID: "o1", Items: []policyItem{{SKU: "a", Price: 1.000}, {SKU: "b", Price: 2.000}}}

	// The first item differs by 0.001, exceeding the exact (zero) tolerance
	// of the concrete Items[0].Price rule, even though the wildcard rule
	// would allow it.
	y := policyOrder{ID: "o1", Items: []policyItem{{SKU: "a", Price: 1.001}, {SKU: "b", Price: 2.001}}}
	if cmp.Equal(x, y, opt) {
		t.Errorf("Equal() = true, want false: Items[0].Price rule (tolerance 0) must override Items[*].Price rule")
	}

	// Only the second item differs, by the same 0.001, which the wildcard
	// rule tolerates.
	z := policyOrder{ID: "o1", Items: []policyItem{{SKU: "a", Price: 1.000}, {SKU: "b", Price: 2.001}}}
	if !cmp.Equal(x, z, opt) {
		t.Errorf("Equal() = false, want true: Items[*].Price rule (tolerance 0.01) must accept 0.001")
	}
}

func TestPolicyIgnore(t *testing.T) {
	opt := compilePolicy(t, cmp.NewPolicy(reflect.TypeOf(policyOrder{})).
		Ignore("ID").
		Ignore("Items[*].SKU"))

	x := policyOrder{ID: "o1", Items: []policyItem{{SKU: "a", Price: 1}, {SKU: "b", Price: 2}}}
	y := policyOrder{ID: "o2", Items: []policyItem{{SKU: "x", Price: 1}, {SKU: "y", Price: 2}}}
	if !cmp.Equal(x, y, opt) {
		t.Errorf("Equal() = false, want true: ID and Items[*].SKU are ignored")
	}

	z := policyOrder{ID: "o1", Items: []policyItem{{SKU: "a", Price: 1}, {SKU: "b", Price: 3}}}
	if cmp.Equal(x, z, opt) {
		t.Errorf("Equal() = true, want false: Price is not ignored")
	}
}

func TestPolicyFieldWildcard(t *testing.T) {
	type pair struct {
		A, B policyItem
	}
	opt := compilePolicy(t, cmp.NewPolicy(reflect.TypeOf(pair{})).
		Tolerance("*.Price", 0.01))

	x := pair{A: policyItem{Price: 1.000}, B: policyItem{Price: 2.000}}
	y := pair{A: policyItem{Price: 1.005}, B: policyItem{Price: 2.005}}
	if !cmp.Equal(x, y, opt) {
		t.Errorf("Equal() = false, want true: *.Price applies to both fields")
	}
	z := pair{A: policyItem{Price: 1.005}, B: policyItem{Price: 2.02}}
	if cmp.Equal(x, z, opt) {
		t.Errorf("Equal() = true, want false: B.Price differs by 0.02")
	}
}

func TestPolicyNestedPointers(t *testing.T) {
	type inner struct {
		Items []*policyItem
	}
	type nested struct {
		Inner **inner
	}
	opt := compilePolicy(t, cmp.NewPolicy(reflect.TypeOf(nested{})).
		Tolerance("Inner.Items[*].Price", 0.01))

	make := func(p0, p1 float64) nested {
		in := &inner{Items: []*policyItem{{Price: p0}, {Price: p1}}}
		return nested{Inner: &in}
	}
	if !cmp.Equal(make(1.000, 2.000), make(1.005, 2.005), opt) {
		t.Errorf("Equal() = false, want true: tolerance applies through nested pointers")
	}
	if cmp.Equal(make(1.000, 2.000), make(1.005, 2.02), opt) {
		t.Errorf("Equal() = true, want false: second element differs by 0.02")
	}
}

func TestPolicyRecursiveStructure(t *testing.T) {
	type node struct {
		Value float64
		Next  *node
	}
	opt := compilePolicy(t, cmp.NewPolicy(reflect.TypeOf(node{})).
		Tolerance("Next.Value", 0.01).
		Tolerance("Next.Next.Value", 0))

	list := func(vs ...float64) *node {
		var head *node
		for i := len(vs) - 1; i >= 0; i-- {
			head = &node{Value: vs[i], Next: head}
		}
		return head
	}
	x := list(1.0, 2.0, 3.0)
	if !cmp.Equal(*x, *list(1.0, 2.005, 3.0), opt) {
		t.Errorf("Equal() = false, want true: Next.Value within tolerance")
	}
	if cmp.Equal(*x, *list(1.0, 2.005, 3.001), opt) {
		t.Errorf("Equal() = true, want false: Next.Next.Value has zero tolerance")
	}
}

func TestPolicyNamedFloatType(t *testing.T) {
	type celsius float64
	type reading struct {
		Temp celsius
	}
	opt := compilePolicy(t, cmp.NewPolicy(reflect.TypeOf(reading{})).
		Tolerance("Temp", 0.5))

	if !cmp.Equal(reading{Temp: 20.0}, reading{Temp: 20.4}, opt) {
		t.Errorf("Equal() = false, want true: 0.4 within tolerance 0.5")
	}
	if cmp.Equal(reading{Temp: 20.0}, reading{Temp: 20.6}, opt) {
		t.Errorf("Equal() = true, want false: 0.6 exceeds tolerance 0.5")
	}
}

func TestPolicyRootBinding(t *testing.T) {
	// A policy compiled for policyOrder must not affect other root types.
	opt := compilePolicy(t, cmp.NewPolicy(reflect.TypeOf(policyOrder{})).
		Ignore("ID"))

	type other struct {
		ID string
	}
	if cmp.Equal(other{ID: "x"}, other{ID: "y"}, opt) {
		t.Errorf("Equal() = true, want false: policy must be inert on unbound root types")
	}
}

func TestPolicyNegativeTolerance(t *testing.T) {
	for _, tol := range []float64{-0.1, math.NaN()} {
		_, err := cmp.NewPolicy(reflect.TypeOf(policyOrder{})).
			Tolerance("Items[*].Price", tol).
			Compile()
		if err == nil {
			t.Fatalf("Compile() with tolerance %v: got nil error, want non-nil", tol)
		}
		if got := err.Error(); !strings.Contains(got, "non-negative") {
			t.Errorf("Compile() with tolerance %v: error %q does not mention non-negative", tol, got)
		}
	}
}

func TestPolicyConflict(t *testing.T) {
	// Equal-specificity tolerance rules that overlap must both be diagnosed.
	_, err := cmp.NewPolicy(reflect.TypeOf(policyOrder{})).
		Tolerance("Items[*].Price", 0.01).
		Tolerance("Items[*].Price", 0.02).
		Compile()
	if err == nil {
		t.Fatal("Compile() = nil error, want conflict")
	}
	if got := err.Error(); !strings.Contains(got, `"Items[*].Price" (tolerance 0.01)`) || !strings.Contains(got, `"Items[*].Price" (tolerance 0.02)`) {
		t.Errorf("Compile() error %q does not diagnose both conflicting rules", got)
	}

	// Ignore versus tolerance at equal specificity is also a conflict.
	_, err = cmp.NewPolicy(reflect.TypeOf(policyOrder{})).
		Ignore("Items[*].Price").
		Tolerance("Items[*].Price", 0.01).
		Compile()
	if err == nil {
		t.Fatal("Compile() = nil error, want ignore/tolerance conflict")
	}
	if got := err.Error(); !strings.Contains(got, `"Items[*].Price" (ignore)`) || !strings.Contains(got, `"Items[*].Price" (tolerance 0.01)`) {
		t.Errorf("Compile() error %q does not diagnose both conflicting rules", got)
	}

	// A concrete rule overriding a wildcard rule is not a conflict.
	_ = compilePolicy(t, cmp.NewPolicy(reflect.TypeOf(policyOrder{})).
		Tolerance("Items[*].Price", 0.01).
		Tolerance("Items[0].Price", 0))

	// Identical duplicate rules do not conflict.
	_ = compilePolicy(t, cmp.NewPolicy(reflect.TypeOf(policyOrder{})).
		Tolerance("Items[*].Price", 0.01).
		Tolerance("Items[*].Price", 0.01))
}

func TestPolicyPathErrors(t *testing.T) {
	tests := []struct {
		path    string
		tol     float64
		wantSub []string
	}{
		{"Items[0].Nope", 0.1, []string{"segment 1", `has no field "Nope"`}},
		{"Items[0].Price[3]", 0.1, []string{"segment 1", "cannot index type float64"}},
		{"ID", 0.1, []string{"segment 0", "floating-point"}},
		{"ID.Name", 0.1, []string{"segment 1", "not a struct"}},
		{"Items.Price", 0.1, []string{"segment 1", "not a struct"}},
	}
	for _, tt := range tests {
		_, err := cmp.NewPolicy(reflect.TypeOf(policyOrder{})).
			Tolerance(tt.path, tt.tol).
			Compile()
		if err == nil {
			t.Errorf("Compile() with path %q = nil error, want non-nil", tt.path)
			continue
		}
		for _, sub := range tt.wantSub {
			if !strings.Contains(err.Error(), sub) {
				t.Errorf("Compile() with path %q: error %q missing %q", tt.path, err.Error(), sub)
			}
		}
	}
}

func TestPolicyInvalidSyntax(t *testing.T) {
	for _, path := range []string{"", "Items..Price", "Items[x].Price", "Items[-1].Price", "Items[0", "0Bad.Price"} {
		_, err := cmp.NewPolicy(reflect.TypeOf(policyOrder{})).
			Ignore(path).
			Compile()
		if err == nil {
			t.Errorf("Compile() with path %q = nil error, want non-nil", path)
		}
	}
}
