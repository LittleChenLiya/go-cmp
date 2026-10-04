// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmpcheck_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpcheck"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// approxInt is an approximate-equality comparer on ints: two values are
// equal when they differ by at most one. It is reflexive and symmetric but
// not transitive (0 ~ 1 and 1 ~ 2, but 0 !~ 2).
func approxInt(x, y int) bool {
	d := x - y
	if d < 0 {
		d = -d
	}
	return d <= 1
}

// findTransitivity returns the first transitivity counterexample with the
// given indices, or nil.
func findTransitivity(r *cmpcheck.Report, i, j, k int) *cmpcheck.TransitivityCounterexample {
	for _, c := range r.Transitivity.Counterexamples {
		if c.I == i && c.J == j && c.K == k {
			cc := c
			return &cc
		}
	}
	return nil
}

// TestApproxEqualityTransitivity verifies that an approximate-equality
// comparer which makes 0 ~ 1 and 1 ~ 2 but 0 !~ 2 is reported as a
// transitivity violation, with the full three-link chain preserved.
func TestApproxEqualityTransitivity(t *testing.T) {
	// Use samples 0,1,2 with an approximate-equality comparer that tolerates
	// a difference of at most one: 0 ~ 1 and 1 ~ 2, but 0 !~ 2.
	samples := []any{0, 1, 2}
	opts := cmp.Options{cmp.Comparer(approxInt)}
	report := cmpcheck.CheckWithOptions(samples, opts...)

	if got, want := report.Reflexivity.Status, cmpcheck.StatusOK; got != want {
		t.Errorf("Reflexivity: got %s, want %s", got, want)
	}
	if got, want := report.Symmetry.Status, cmpcheck.StatusOK; got != want {
		t.Errorf("Symmetry: got %s, want %s", got, want)
	}
	if got, want := report.Transitivity.Status, cmpcheck.StatusViolated; got != want {
		t.Errorf("Transitivity: got %s, want %s", got, want)
	}

	// The broken chains are (0,1,2) and (2,1,0): the two links through the
	// middle sample hold, but the direct link between the endpoints does not.
	wantChains := [][3]int{{0, 1, 2}, {2, 1, 0}}
	if len(report.Transitivity.Counterexamples) != len(wantChains) {
		t.Fatalf("Transitivity counterexamples: got %d, want %d: %v",
			len(report.Transitivity.Counterexamples), len(wantChains), report.Transitivity.Counterexamples)
	}
	for _, want := range wantChains {
		c := findTransitivity(report, want[0], want[1], want[2])
		if c == nil {
			t.Errorf("missing transitivity counterexample for chain %v", want)
			continue
		}
		if !c.Rij || !c.Rjk || c.Rik {
			t.Errorf("chain %v: got R(%d,%d)=%t, R(%d,%d)=%t, R(%d,%d)=%t; want true, true, false",
				want, c.I, c.J, c.Rij, c.J, c.K, c.Rjk, c.I, c.K, c.Rik)
		}
	}
}

// panicType is a type whose comparison panics because it has an unexported
// field.
type panicType struct {
	x int
}

// TestPanicTypeUnknown verifies that a type whose comparison panics is
// reported as undetermined, not as unequal, and produces no counterexamples.
func TestPanicTypeUnknown(t *testing.T) {
	samples := []any{panicType{1}, panicType{2}}
	report := cmpcheck.CheckWithOptions(samples)

	if len(report.UnknownPairs) != 4 {
		t.Errorf("UnknownPairs: got %v, want all 4 pairs", report.UnknownPairs)
	}
	if report.Reflexivity.Status != cmpcheck.StatusUnknown {
		t.Errorf("Reflexivity: got %s, want UNKNOWN", report.Reflexivity.Status)
	}
	if report.Symmetry.Status != cmpcheck.StatusUnknown {
		t.Errorf("Symmetry: got %s, want UNKNOWN", report.Symmetry.Status)
	}
	if report.Transitivity.Status != cmpcheck.StatusUnknown {
		t.Errorf("Transitivity: got %s, want UNKNOWN", report.Transitivity.Status)
	}
	if len(report.Reflexivity.Counterexamples) != 0 ||
		len(report.Symmetry.Counterexamples) != 0 ||
		len(report.Transitivity.Counterexamples) != 0 {
		t.Errorf("panicked comparisons must not be counted as counterexamples")
	}
}

// TestComparerCallCount verifies that each ordered pair of samples is
// evaluated exactly once: cmp.Equal is called once per pair, and the
// comparer is invoked a bounded number of times per call (cmp internally
// re-invokes the comparer with swapped arguments to check symmetry).
func TestComparerCallCount(t *testing.T) {
	samples := []any{0, 1, 2}
	index := map[int]int{0: 0, 1: 1, 2: 2}
	calls := make(map[[2]int]int)
	opts := cmp.Options{cmp.Comparer(func(x, y int) bool {
		calls[[2]int{index[x], index[y]}]++
		return approxInt(x, y)
	})}
	report := cmpcheck.CheckWithOptions(samples, opts...)

	// Every pair must be determined (no panics in this scenario).
	if len(report.UnknownPairs) != 0 {
		t.Errorf("UnknownPairs: got %v, want none", report.UnknownPairs)
	}

	// cmp.Equal is called exactly once per ordered pair (9 pairs). Each call
	// invokes the root comparer exactly twice (once forward, once with swapped
	// arguments), so every ordered pair records exactly 2 comparer calls and
	// the total is 2 * 9 = 18. If any pair were evaluated twice, the total
	// would exceed 18.
	const n = 3
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if got, want := calls[[2]int{i, j}], 2; got != want {
				t.Errorf("comparer calls for pair (%d,%d): got %d, want %d", i, j, got, want)
			}
		}
	}
	total := 0
	for _, c := range calls {
		total += c
	}
	if got, want := total, 2*n*n; got != want {
		t.Errorf("total comparer calls: got %d, want %d (one cmp.Equal per ordered pair)", got, want)
	}
}

// TestRelationMemoization verifies that the Relation API evaluates the
// relation exactly once per ordered pair and caches the result.
func TestRelationMemoization(t *testing.T) {
	samples := []any{0, 1, 2}
	calls := make(map[[2]int]int)
	rel := func(i, j int) (bool, bool) {
		calls[[2]int{i, j}]++
		return true, true
	}
	cmpcheck.Check(samples, rel)

	const n = 3
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if got, want := calls[[2]int{i, j}], 1; got != want {
				t.Errorf("relation calls for pair (%d,%d): got %d, want %d", i, j, got, want)
			}
		}
	}
}

// TestAsymmetricRelation verifies that an asymmetric relation is reported as
// a symmetry violation with both directions of the pair preserved.
func TestAsymmetricRelation(t *testing.T) {
	samples := []any{0, 1, 2}
	// R(i,j) holds exactly when i < j (and R(i,i) holds). This is asymmetric:
	// R(0,1) is true but R(1,0) is false.
	rel := func(i, j int) (bool, bool) {
		if i == j {
			return true, true
		}
		return i < j, true
	}
	report := cmpcheck.Check(samples, rel)

	if report.Symmetry.Status != cmpcheck.StatusViolated {
		t.Fatalf("Symmetry: got %s, want VIOLATED", report.Symmetry.Status)
	}
	var found bool
	for _, c := range report.Symmetry.Counterexamples {
		if c.I == 0 && c.J == 1 {
			found = true
			if !c.Rij || c.Rji {
				t.Errorf("pair (0,1): got R(0,1)=%t, R(1,0)=%t; want true, false", c.Rij, c.Rji)
			}
		}
	}
	if !found {
		t.Errorf("missing symmetry counterexample for pair (0,1): %v", report.Symmetry.Counterexamples)
	}
}

// TestNonReflexiveRelation verifies that an irreflexive relation is reported
// as a reflexivity violation, with the self-comparison value preserved.
func TestNonReflexiveRelation(t *testing.T) {
	samples := []any{0, 1, 2}
	// R(i,j) holds exactly when i != j, so R(i,i) is always false.
	rel := func(i, j int) (bool, bool) {
		return i != j, true
	}
	report := cmpcheck.Check(samples, rel)

	if report.Reflexivity.Status != cmpcheck.StatusViolated {
		t.Fatalf("Reflexivity: got %s, want VIOLATED", report.Reflexivity.Status)
	}
	if len(report.Reflexivity.Counterexamples) != 3 {
		t.Fatalf("Reflexivity counterexamples: got %d, want 3", len(report.Reflexivity.Counterexamples))
	}
	for i, c := range report.Reflexivity.Counterexamples {
		if c.I != i || c.Equal {
			t.Errorf("counterexample %d: got R(%d,%d)=%t; want R(%d,%d)=false", i, c.I, c.I, c.Equal, i, i)
		}
	}
}

// TestEquivalenceRelation is a positive control: the identity relation is an
// equivalence relation, so all three laws hold.
func TestEquivalenceRelation(t *testing.T) {
	samples := []any{0, 1, 2}
	rel := func(i, j int) (bool, bool) {
		return i == j, true
	}
	report := cmpcheck.Check(samples, rel)

	if report.Reflexivity.Status != cmpcheck.StatusOK ||
		report.Symmetry.Status != cmpcheck.StatusOK ||
		report.Transitivity.Status != cmpcheck.StatusOK {
		t.Errorf("identity relation: got Reflexivity=%s Symmetry=%s Transitivity=%s; want all OK",
			report.Reflexivity.Status, report.Symmetry.Status, report.Transitivity.Status)
	}
}

// TestUnknownLinkSkipsTransitivity verifies that a transitivity triple with
// an undetermined link is not judged (and so produces no counterexample but
// leaves the status unknown).
func TestUnknownLinkSkipsTransitivity(t *testing.T) {
	samples := []any{0, 1, 2}
	// Approx equality, except the pair (0,2) and (2,0) is undetermined.
	rel := func(i, j int) (bool, bool) {
		if (i == 0 && j == 2) || (i == 2 && j == 0) {
			return false, false
		}
		return approxInt(i, j), true
	}
	report := cmpcheck.Check(samples, rel)

	if report.Transitivity.Status != cmpcheck.StatusUnknown {
		t.Fatalf("Transitivity: got %s, want UNKNOWN", report.Transitivity.Status)
	}
	if len(report.Transitivity.Counterexamples) != 0 {
		t.Errorf("undetermined triple must not be a counterexample: %v", report.Transitivity.Counterexamples)
	}
}

// TestReportDoesNotSerializeSamples verifies that the report identifies
// samples by index and never prints the original sample values.
func TestReportDoesNotSerializeSamples(t *testing.T) {
	samples := []any{1000, 2000, 3000}
	// Approx equality with a tolerance of 1000: 1000 ~ 2000 and 2000 ~ 3000,
	// but 1000 !~ 3000.
	opts := cmp.Options{cmp.Comparer(func(x, y int) bool {
		d := x - y
		if d < 0 {
			d = -d
		}
		return d <= 1000
	})}
	report := cmpcheck.CheckWithOptions(samples, opts...)
	out := report.String()

	for _, v := range []string{"1000", "2000", "3000"} {
		if strings.Contains(out, v) {
			t.Errorf("report must not contain sample value %q:\n%s", v, out)
		}
	}
	if !strings.Contains(out, "finite-sample check") {
		t.Errorf("report must state it is a finite-sample check:\n%s", out)
	}
}

// TestEquateApproxCompatibility verifies the checker works with cmpopts
// options (here EquateApprox), not just a bare cmp.Comparer.
func TestEquateApproxCompatibility(t *testing.T) {
	samples := []any{0.0, 0.1, 0.2}
	opts := cmp.Options{cmpopts.EquateApprox(0, 0.15)}
	report := cmpcheck.CheckWithOptions(samples, opts...)

	if report.Transitivity.Status != cmpcheck.StatusViolated {
		t.Fatalf("Transitivity: got %s, want VIOLATED", report.Transitivity.Status)
	}
	if len(report.Transitivity.Counterexamples) == 0 {
		t.Errorf("expected transitivity counterexamples for EquateApprox")
	}
}

// TestEmptySamples verifies the checker handles an empty sample set.
func TestEmptySamples(t *testing.T) {
	report := cmpcheck.Check(nil, func(i, j int) (bool, bool) {
		t.Fatal("relation must not be called for empty samples")
		return false, false
	})
	if report.Samples != 0 {
		t.Errorf("Samples: got %d, want 0", report.Samples)
	}
	if report.Reflexivity.Status != cmpcheck.StatusOK ||
		report.Symmetry.Status != cmpcheck.StatusOK ||
		report.Transitivity.Status != cmpcheck.StatusOK {
		t.Errorf("empty samples: got Reflexivity=%s Symmetry=%s Transitivity=%s; want all OK",
			report.Reflexivity.Status, report.Symmetry.Status, report.Transitivity.Status)
	}
}
