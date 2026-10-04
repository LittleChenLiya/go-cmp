// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package equivalence checks whether a set of cmp.Option values induces an
// equivalence relation (reflexive, symmetric, and transitive) on a finite
// set of sample values.
//
// Custom go-cmp options (Comparer, Transformer, filtered options, and so
// on) are not guaranteed to define an equivalence relation. An
// approximate-equality comparer, for example, is typically not transitive.
// This package offers a sample-based checker that searches for concrete
// counterexamples instead of merely trusting a handful of ad-hoc pairwise
// comparisons.
//
// Guarantees and scope:
//   - Each ordered pair of samples is passed to cmp.Equal at most once and
//     the outcome is cached in a verdict matrix; no pair is re-compared.
//     Note that cmp itself may invoke a Comparer function more than once
//     per cmp.Equal call for its own internal consistency spot-checks.
//   - A panic raised by cmp.Equal (for example from a faulty Comparer) is
//     caught and recorded as Unknown. Unknown is never treated as unequal.
//   - Transitivity of a chain (i, j, k) is only judged when all three
//     relevant comparisons (i,j), (j,k), and (i,k) are known.
//   - Reports reference samples by index only; sample values are never
//     serialized into a Report.
//   - This is a finite-sample check: a reported counterexample is a genuine
//     violation (assuming deterministic options), but the absence of
//     counterexamples proves nothing about inputs outside the provided
//     samples. It is not a formal verification of the options.
package equivalence

import (
	"fmt"
	"strings"

	"github.com/google/go-cmp/cmp"
)

// Verdict is the cached outcome of a single ordered comparison
// cmp.Equal(samples[i], samples[j], opts...).
type Verdict int

const (
	// Unknown means the comparison panicked and is undecidable.
	// It is never treated as inequality.
	Unknown Verdict = iota
	// Equal means cmp.Equal reported the ordered pair as equal.
	Equal
	// NotEqual means cmp.Equal reported the ordered pair as not equal.
	NotEqual
)

// String returns a short human-readable name for the verdict.
func (v Verdict) String() string {
	switch v {
	case Equal:
		return "equal"
	case NotEqual:
		return "not-equal"
	default:
		return "unknown"
	}
}

// Report is the result of a finite-sample equivalence check.
//
// All counterexamples are expressed purely in terms of sample indices;
// the sample values themselves are never serialized into the report.
type Report struct {
	// Samples is the number of samples that were checked.
	Samples int
	// Calls is the number of cmp.Equal calls performed. It equals
	// Samples*Samples: exactly one call per ordered pair of samples.
	Calls int

	// Reflexive lists indices i such that samples[i] != samples[i].
	Reflexive []int
	// Symmetric lists index pairs (i, j) with i < j such that the
	// comparison of (i, j) and (j, i) disagree.
	Symmetric [][2]int
	// Transitive lists relation chains (i, j, k) such that
	// samples[i] ~ samples[j] and samples[j] ~ samples[k], but
	// samples[i] !~ samples[k]. A chain is only reported when all
	// three relevant comparisons are known.
	Transitive [][3]int
	// Unknowns lists ordered index pairs (i, j) whose comparison
	// panicked and is therefore undecidable.
	Unknowns [][2]int

	verdicts [][]Verdict // cached verdict matrix, indexed [i][j]
}

// Verdict returns the cached verdict of the ordered comparison of
// samples[i] against samples[j].
func (r *Report) Verdict(i, j int) Verdict { return r.verdicts[i][j] }

// OK reports whether no counterexample to reflexivity, symmetry, or
// transitivity was found among the provided samples. Undecidable
// (panicking) comparisons do not affect OK; see Conclusive.
func (r *Report) OK() bool {
	return len(r.Reflexive) == 0 && len(r.Symmetric) == 0 && len(r.Transitive) == 0
}

// Conclusive reports whether every ordered pair was decidable,
// that is, no comparison panicked.
func (r *Report) Conclusive() bool { return len(r.Unknowns) == 0 }

// Check compares every ordered pair of samples with cmp.Equal and the
// given options, caches each verdict, and reports any counterexample to
// reflexivity, symmetry, or transitivity found among the samples.
//
// Each ordered pair is compared exactly once. A panicking comparison is
// recorded as Unknown rather than as inequality, and transitivity of a
// chain is only judged when all three of its comparisons are known.
//
// The check is exhaustive only over the provided samples: counterexamples
// are genuine violations, but finding none does not prove that the options
// define an equivalence relation on arbitrary inputs.
func Check[T any](samples []T, opts ...cmp.Option) *Report {
	n := len(samples)
	r := &Report{Samples: n, verdicts: make([][]Verdict, n)}
	for i := range r.verdicts {
		r.verdicts[i] = make([]Verdict, n)
	}

	// Compare each ordered pair exactly once and cache the verdict.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			r.Calls++
			v := compare(samples[i], samples[j], opts)
			r.verdicts[i][j] = v
			if v == Unknown {
				r.Unknowns = append(r.Unknowns, [2]int{i, j})
			}
		}
	}

	// Reflexivity: samples[i] must equal itself.
	for i := 0; i < n; i++ {
		if r.verdicts[i][i] == NotEqual {
			r.Reflexive = append(r.Reflexive, i)
		}
	}

	// Symmetry: the (i, j) and (j, i) verdicts must agree.
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			a, b := r.verdicts[i][j], r.verdicts[j][i]
			if a == Unknown || b == Unknown {
				continue // undecidable pairs cannot witness a violation
			}
			if a != b {
				r.Symmetric = append(r.Symmetric, [2]int{i, j})
			}
		}
	}

	// Transitivity: for samples[i] ~ samples[j] ~ samples[k] we must
	// have samples[i] ~ samples[k]. A chain is only judged when all
	// three relevant comparisons are known.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if r.verdicts[i][j] != Equal {
				continue
			}
			for k := 0; k < n; k++ {
				if r.verdicts[j][k] != Equal {
					continue
				}
				if r.verdicts[i][k] == NotEqual {
					r.Transitive = append(r.Transitive, [3]int{i, j, k})
				}
			}
		}
	}

	return r
}

// compare runs cmp.Equal once, converting any panic into Unknown.
func compare[T any](x, y T, opts []cmp.Option) (v Verdict) {
	defer func() {
		if recover() != nil {
			v = Unknown
		}
	}()
	if cmp.Equal(x, y, opts...) {
		return Equal
	}
	return NotEqual
}

// String renders the report using sample indices and relation chains only.
// Sample values are never serialized.
func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "finite-sample equivalence check: %d samples, %d comparisons\n", r.Samples, r.Calls)
	switch {
	case !r.OK():
		fmt.Fprintln(&b, "verdict: NOT an equivalence relation on the provided samples")
	case !r.Conclusive():
		fmt.Fprintln(&b, "verdict: no counterexample found, but some comparisons are undecidable")
	default:
		fmt.Fprintln(&b, "verdict: no counterexample found on the provided samples")
	}
	if len(r.Reflexive) > 0 {
		fmt.Fprintf(&b, "reflexivity violations (%d):\n", len(r.Reflexive))
		for _, i := range r.Reflexive {
			fmt.Fprintf(&b, "  samples[%d] !~ samples[%d]\n", i, i)
		}
	}
	if len(r.Symmetric) > 0 {
		fmt.Fprintf(&b, "symmetry violations (%d):\n", len(r.Symmetric))
		for _, p := range r.Symmetric {
			fmt.Fprintf(&b, "  samples[%d] %s samples[%d], but samples[%d] %s samples[%d]\n",
				p[0], rel(r.verdicts[p[0]][p[1]]), p[1],
				p[1], rel(r.verdicts[p[1]][p[0]]), p[0])
		}
	}
	if len(r.Transitive) > 0 {
		fmt.Fprintf(&b, "transitivity violations (%d):\n", len(r.Transitive))
		for _, t := range r.Transitive {
			fmt.Fprintf(&b, "  samples[%d] ~ samples[%d] and samples[%d] ~ samples[%d], but samples[%d] !~ samples[%d]\n",
				t[0], t[1], t[1], t[2], t[0], t[2])
		}
	}
	if len(r.Unknowns) > 0 {
		fmt.Fprintf(&b, "undecidable comparisons (%d, comparison panicked):\n", len(r.Unknowns))
		for _, p := range r.Unknowns {
			fmt.Fprintf(&b, "  samples[%d] vs samples[%d]: unknown\n", p[0], p[1])
		}
	}
	fmt.Fprintln(&b, "scope: counterexamples are definitive, but their absence proves nothing beyond these samples; this is a finite-sample check, not a proof")
	return b.String()
}

// rel renders a known verdict as a relation symbol.
func rel(v Verdict) string {
	if v == Equal {
		return "~"
	}
	return "!~"
}
