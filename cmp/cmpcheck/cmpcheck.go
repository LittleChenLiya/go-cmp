// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cmpcheck performs finite-sample checks on the equality relation
// induced by go-cmp options.
//
// Custom [cmp.Option] values (such as a [cmp.Comparer]) determine an equality
// relation that is not necessarily an equivalence relation: it may fail to be
// reflexive, symmetric, or transitive. This package examines the relation over
// a finite set of samples and reports concrete counterexamples (identified by
// sample index and the chain of relation links), so that the author of the
// options can see exactly which law is broken.
//
// # Finite-sample guarantee
//
// This is a finite-sample check only. It examines nothing beyond the given
// samples and therefore cannot prove that the relation is an equivalence
// relation in general. A report with no counterexamples means only that no
// counterexample exists among the examined samples; it does not establish
// global correctness. The check can refute equivalence (by exhibiting a
// counterexample) but can never confirm it.
//
// Comparisons that panic (for example, because a value has an unexported
// field) are captured and reported as undetermined, not as unequal.
// Transitivity is judged only when all three of the relevant comparisons are
// determined; a single undetermined link makes the triple undetermined.
package cmpcheck

import (
	"fmt"
	"strings"

	"github.com/google/go-cmp/cmp"
)

// Relation is a binary relation over the indices of a finite sample set.
//
// It reports whether the samples at indices i and j are equal. The ok result
// is false when the comparison could not be determined (for example, because
// it panicked); such a pair is treated as undetermined rather than unequal.
type Relation func(i, j int) (equal bool, ok bool)

// Status describes whether a law (reflexivity, symmetry, or transitivity)
// holds over the examined samples.
type Status int

const (
	// StatusOK means that every determined comparison examined satisfies
	// the law.
	StatusOK Status = iota
	// StatusViolated means that at least one determined comparison is a
	// counterexample to the law.
	StatusViolated
	// StatusUnknown means that no counterexample was found, but one or more
	// comparisons were undetermined, so the law could not be fully judged.
	StatusUnknown
)

// String returns the name of the status.
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusViolated:
		return "VIOLATED"
	case StatusUnknown:
		return "UNKNOWN"
	default:
		return fmt.Sprintf("Status(%d)", int(s))
	}
}

// ReflexivityCounterexample is a sample index i for which the self-comparison
// R(i,i) is determined to be false.
type ReflexivityCounterexample struct {
	// I is the index of the sample.
	I int
	// Equal is the determined value of R(i,i) (always false in a
	// counterexample).
	Equal bool
}

// SymmetryCounterexample is a pair of sample indices (i, j) for which the two
// directions of comparison R(i,j) and R(j,i) are determined but differ.
type SymmetryCounterexample struct {
	I, J int
	// Rij is the determined value of R(i,j).
	Rij bool
	// Rji is the determined value of R(j,i).
	Rji bool
}

// TransitivityCounterexample is a triple of sample indices (i, j, k) for which
// R(i,j) and R(j,k) are determined to be true but R(i,k) is determined to be
// false, breaking the chain R(i,j) and R(j,k) imply R(i,k).
type TransitivityCounterexample struct {
	I, J, K int
	// Rij is the determined value of R(i,j).
	Rij bool
	// Rjk is the determined value of R(j,k).
	Rjk bool
	// Rik is the determined value of R(i,k) (always false in a
	// counterexample).
	Rik bool
}

// Report is the result of a finite-sample equivalence check over a set of
// samples.
//
// The report identifies samples by index only; it never serializes the
// original sample values.
type Report struct {
	// Samples is the number of samples examined.
	Samples int
	// Reflexivity reports whether R(i,i) holds for every i.
	Reflexivity ReflexivityReport
	// Symmetry reports whether R(i,j) == R(j,i) for every i and j.
	Symmetry SymmetryReport
	// Transitivity reports whether R(i,j) and R(j,k) imply R(i,k) for all i, j, k.
	Transitivity TransitivityReport
	// UnknownPairs lists the index pairs (i, j) whose comparison was
	// undetermined (for example, because it panicked).
	UnknownPairs [][2]int
}

// ReflexivityReport is the reflexivity check result.
type ReflexivityReport struct {
	Status          Status
	Counterexamples []ReflexivityCounterexample
}

// SymmetryReport is the symmetry check result.
type SymmetryReport struct {
	Status          Status
	Counterexamples []SymmetryCounterexample
}

// TransitivityReport is the transitivity check result.
type TransitivityReport struct {
	Status          Status
	Counterexamples []TransitivityCounterexample
}

// Check examines the relation rel over the finite sample set and reports
// whether it is reflexive, symmetric, and transitive.
//
// The relation is evaluated exactly once for each ordered pair of indices
// (i, j) with i and j in [0, len(samples)), and the result is cached. A panic
// from rel is captured and treated as undetermined.
//
// This is a finite-sample check: it cannot prove that rel is an equivalence
// relation in general. See the package documentation for the full guarantee.
func Check(samples []any, rel Relation) *Report {
	n := len(samples)
	m := buildMatrix(n, rel)
	r := &Report{Samples: n}
	r.Reflexivity = checkReflexivity(n, m)
	r.Symmetry = checkSymmetry(n, m)
	r.Transitivity = checkTransitivity(n, m)
	r.UnknownPairs = collectUnknownPairs(n, m)
	return r
}

// CheckWithOptions builds the equality relation from [cmp.Equal] using the
// given options and then behaves like [Check].
//
// [cmp.Equal] is called exactly once for each ordered pair of samples and the
// result is cached. Any panic from the comparison (for example, due to an
// unexported field) is captured and reported as undetermined, not as unequal.
//
// This is a finite-sample check: it cannot prove that the options induce an
// equivalence relation in general. See the package documentation for the full
// guarantee.
func CheckWithOptions(samples []any, opts ...cmp.Option) *Report {
	rel := func(i, j int) (bool, bool) {
		return cmpEqual(samples[i], samples[j], opts...)
	}
	return Check(samples, rel)
}

// cmpEqual calls cmp.Equal and captures any panic as undetermined.
func cmpEqual(x, y any, opts ...cmp.Option) (equal bool, ok bool) {
	defer func() {
		if recover() != nil {
			equal, ok = false, false
		}
	}()
	return cmp.Equal(x, y, opts...), true
}

// cell is the cached result of one comparison.
type cell struct {
	equal bool
	ok    bool
}

// matrix caches the result of each ordered pair (i, j).
type matrix struct {
	n     int
	cells []cell
}

func (m *matrix) at(i, j int) cell {
	return m.cells[i*m.n+j]
}

// buildMatrix evaluates rel exactly once for each ordered pair and caches the
// result.
func buildMatrix(n int, rel Relation) *matrix {
	m := &matrix{n: n, cells: make([]cell, n*n)}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			eq, ok := safeCall(rel, i, j)
			m.cells[i*n+j] = cell{eq, ok}
		}
	}
	return m
}

// safeCall invokes rel and captures any panic as undetermined.
func safeCall(rel Relation, i, j int) (equal bool, ok bool) {
	defer func() {
		if recover() != nil {
			equal, ok = false, false
		}
	}()
	equal, ok = rel(i, j)
	return equal, ok
}

// checkReflexivity verifies R(i,i) for every i.
func checkReflexivity(n int, m *matrix) ReflexivityReport {
	var rep ReflexivityReport
	rep.Status = StatusOK
	for i := 0; i < n; i++ {
		c := m.at(i, i)
		if !c.ok {
			if rep.Status == StatusOK {
				rep.Status = StatusUnknown
			}
			continue
		}
		if !c.equal {
			rep.Status = StatusViolated
			rep.Counterexamples = append(rep.Counterexamples, ReflexivityCounterexample{I: i, Equal: c.equal})
		}
	}
	return rep
}

// checkSymmetry verifies R(i,j) == R(j,i) for every pair i < j.
func checkSymmetry(n int, m *matrix) SymmetryReport {
	var rep SymmetryReport
	rep.Status = StatusOK
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			cij, cji := m.at(i, j), m.at(j, i)
			if !cij.ok || !cji.ok {
				if rep.Status == StatusOK {
					rep.Status = StatusUnknown
				}
				continue
			}
			if cij.equal != cji.equal {
				rep.Status = StatusViolated
				rep.Counterexamples = append(rep.Counterexamples, SymmetryCounterexample{
					I: i, J: j, Rij: cij.equal, Rji: cji.equal,
				})
			}
		}
	}
	return rep
}

// checkTransitivity verifies that R(i,j) and R(j,k) imply R(i,k) for every
// ordered triple (i, j, k). The law is judged only when all three comparisons
// are determined; an undetermined link makes the triple undetermined.
func checkTransitivity(n int, m *matrix) TransitivityReport {
	var rep TransitivityReport
	rep.Status = StatusOK
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			for k := 0; k < n; k++ {
				cij, cjk, cik := m.at(i, j), m.at(j, k), m.at(i, k)
				if !cij.ok || !cjk.ok || !cik.ok {
					// Transitivity is judged only when all three comparisons
					// are determined; an undetermined link makes the triple
					// undetermined.
					if rep.Status == StatusOK {
						rep.Status = StatusUnknown
					}
					continue
				}
				if cij.equal && cjk.equal && !cik.equal {
					rep.Status = StatusViolated
					rep.Counterexamples = append(rep.Counterexamples, TransitivityCounterexample{
						I: i, J: j, K: k, Rij: cij.equal, Rjk: cjk.equal, Rik: cik.equal,
					})
				}
			}
		}
	}
	return rep
}

// collectUnknownPairs returns the ordered pairs (i, j) whose comparison was
// undetermined.
func collectUnknownPairs(n int, m *matrix) [][2]int {
	var pairs [][2]int
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if !m.at(i, j).ok {
				pairs = append(pairs, [2]int{i, j})
			}
		}
	}
	return pairs
}

// String renders a human-readable report. It identifies samples by index and
// relation links only; it never prints the original sample values.
func (r *Report) String() string {
	var b strings.Builder
	last := r.Samples - 1
	if r.Samples == 0 {
		last = 0
	}
	fmt.Fprintf(&b, "Finite-sample equivalence check over %d sample(s) (indices 0..%d).\n", r.Samples, last)
	b.WriteString("This is a finite-sample check: it examines only the given samples and cannot\n")
	b.WriteString("prove that the relation is an equivalence relation in general. A panicked\n")
	b.WriteString("comparison is treated as undetermined, not unequal; transitivity is judged\n")
	b.WriteString("only when all three relevant comparisons are determined.\n")
	b.WriteString("\n")
	fmt.Fprintf(&b, "Reflexivity: %s\n", r.Reflexivity.Status)
	for _, c := range r.Reflexivity.Counterexamples {
		fmt.Fprintf(&b, "  counterexample: R(%d,%d)=%t (expected true)\n", c.I, c.I, c.Equal)
	}
	fmt.Fprintf(&b, "Symmetry: %s\n", r.Symmetry.Status)
	for _, c := range r.Symmetry.Counterexamples {
		fmt.Fprintf(&b, "  counterexample: R(%d,%d)=%t but R(%d,%d)=%t (expected equal)\n",
			c.I, c.J, c.Rij, c.J, c.I, c.Rji)
	}
	fmt.Fprintf(&b, "Transitivity: %s\n", r.Transitivity.Status)
	for n, c := range r.Transitivity.Counterexamples {
		fmt.Fprintf(&b, "  counterexample %d: R(%d,%d)=%t, R(%d,%d)=%t, but R(%d,%d)=%t (expected true)\n",
			n+1, c.I, c.J, c.Rij, c.J, c.K, c.Rjk, c.I, c.K, c.Rik)
	}
	if len(r.UnknownPairs) == 0 {
		b.WriteString("Undetermined (panicked) pairs: none\n")
	} else {
		fmt.Fprintf(&b, "Undetermined (panicked) pairs: %v\n", r.UnknownPairs)
	}
	return b.String()
}
