// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import "errors"

// ErrMatchBudgetExceeded is returned by MatchSlices when the configured
// comparison budget is exhausted before the matching could be decided.
// It is a distinct error and is never reported as a plain mismatch.
var ErrMatchBudgetExceeded = errors.New("cmp: element comparison budget exceeded")

// MatchConfig configures the behavior of MatchSlices.
// The zero value is valid and means an unlimited comparison budget and
// that a nil slice is never equal to an empty (non-nil) slice.
type MatchConfig struct {
	// MaxComparisons limits the number of element-pair comparisons
	// performed via Equal. Each unordered pair of indices is compared at
	// most once. A zero or negative value means no limit.
	// If the budget is exhausted, MatchSlices returns ErrMatchBudgetExceeded.
	MaxComparisons int

	// NilEqualsEmpty reports whether a nil slice and an empty non-nil
	// slice are considered equal. By default they are different.
	NilEqualsEmpty bool
}

// MatchEdge records a one-to-one pairing between the element at index Left
// of the first slice and the element at index Right of the second slice.
type MatchEdge struct {
	Left  int
	Right int
}

// MatchReport describes the result of MatchSlices.
//
// The report retains references to the original input slices so that every
// index can be traced back to its source element one-to-one. The inputs are
// never modified, sorted, or deep-copied.
//
// If MatchSlices returns an error, the matching was aborted and the report
// is incomplete: no edges are recorded and every index is reported as
// unmatched, so Matched reports false.
type MatchReport[E any] struct {
	x, y []E

	// failed is set when the comparison was aborted with an error, in
	// which case Matched always reports false.
	failed bool

	// Edges is the computed maximum matching, sorted by ascending Left
	// index. Each left and each right index appears at most once.
	Edges []MatchEdge

	// UnmatchedLeft lists the indices into the first slice that could not
	// be paired, in ascending order.
	UnmatchedLeft []int

	// UnmatchedRight lists the indices into the second slice that could not
	// be paired, in ascending order.
	UnmatchedRight []int

	// Comparisons is the number of element-pair comparisons performed.
	Comparisons int
}

// Matched reports whether a perfect matching was found, that is, every
// element of both slices is covered by exactly one edge.
func (r MatchReport[E]) Matched() bool {
	return !r.failed && len(r.UnmatchedLeft) == 0 && len(r.UnmatchedRight) == 0
}

// LeftValue returns the element of the first slice at index i.
func (r MatchReport[E]) LeftValue(i int) E { return r.x[i] }

// RightValue returns the element of the second slice at index j.
func (r MatchReport[E]) RightValue(j int) E { return r.y[j] }

// MatchSlices compares two unordered slices of the same element type by
// computing a maximum bipartite matching: an edge between x[i] and y[j]
// exists exactly when Equal(x[i], y[j], opts...) reports equality, and the
// slices are considered equal when a one-to-one matching covering every
// element exists. Duplicate elements cannot be reused; each element takes
// part in at most one edge.
//
// The edge relation induced by opts is not assumed to be transitive or
// otherwise an equivalence relation, and no greedy pairing is performed.
// The matching is deterministic: left vertices are visited in ascending
// index order and candidate edges and augmenting paths are explored in
// ascending index order of the right-hand indices.
//
// MatchSlices performs at most len(x)*len(y) element comparisons, i.e.
// O(n*m) comparisons, and the augmenting-path (Kuhn's) matching algorithm
// runs in O(n*m*(n+m)) time with O(n*m) auxiliary space for the cached
// edge relation. The inputs are retained by reference in the report and
// are never modified, sorted, or deep-copied.
//
// A nil slice is different from an empty non-nil slice unless
// cfg.NilEqualsEmpty is set. cfg.MaxComparisons bounds the number of
// element comparisons; exceeding the budget returns ErrMatchBudgetExceeded
// rather than a negative result. Panics raised by element comparisons
// (including failing custom Comparer options) propagate to the caller
// unchanged and are never converted into a mismatch.
//
// MatchSlices is a standalone helper and must not be wrapped in a Comparer
// that applies to the same slice type it is called on, as that would cause
// infinite recursion.
func MatchSlices[S ~[]E, E any](x, y S, cfg MatchConfig, opts ...Option) (MatchReport[E], error) {
	rep := MatchReport[E]{x: x, y: y}
	n, m := len(x), len(y)

	if n == 0 && m == 0 {
		if !cfg.NilEqualsEmpty && ((x == nil) != (y == nil)) {
			// A nil slice differs from an empty non-nil slice by default.
			// There are no elements to leave unmatched; the slices simply
			// do not compare equal.
			rep.failed = true
			return rep, errNilVsEmpty
		}
		return rep, nil
	}

	matcher := &bipartiteMatcher[E]{
		x:    x,
		y:    y,
		opts: opts,
		left: cfg.MaxComparisons,
	}
	if matcher.left <= 0 {
		matcher.left = -1 // unlimited
	}

	// matchR[j] is the left index matched to right index j, or -1.
	matchR := make([]int, m)
	for j := range matchR {
		matchR[j] = -1
	}
	matchL := make([]int, n)
	for i := range matchL {
		matchL[i] = -1
	}

	for i := 0; i < n; i++ {
		seen := make([]bool, m)
		ok, err := matcher.augment(i, seen, matchR, matchL)
		if err != nil {
			return abortedReport[E](x, y, n, m), err
		}
		_ = ok
	}

	for i := 0; i < n; i++ {
		if matchL[i] >= 0 {
			rep.Edges = append(rep.Edges, MatchEdge{Left: i, Right: matchL[i]})
		} else {
			rep.UnmatchedLeft = append(rep.UnmatchedLeft, i)
		}
	}
	for j := 0; j < m; j++ {
		if matchR[j] < 0 {
			rep.UnmatchedRight = append(rep.UnmatchedRight, j)
		}
	}
	rep.Comparisons = matcher.used
	return rep, nil
}

// abortedReport builds the report returned alongside an error, in which no
// element is considered matched.
func abortedReport[E any](x, y []E, n, m int) MatchReport[E] {
	rep := MatchReport[E]{x: x, y: y, failed: true}
	for i := 0; i < n; i++ {
		rep.UnmatchedLeft = append(rep.UnmatchedLeft, i)
	}
	for j := 0; j < m; j++ {
		rep.UnmatchedRight = append(rep.UnmatchedRight, j)
	}
	return rep
}

// errNilVsEmpty reports that one slice is nil and the other is an empty
// non-nil slice, which are different unless MatchConfig.NilEqualsEmpty.
var errNilVsEmpty = errors.New("cmp: nil slice differs from empty slice")

// IsNilVsEmpty reports whether err indicates that MatchSlices was given a
// nil slice on one side and an empty non-nil slice on the other while
// MatchConfig.NilEqualsEmpty was unset.
func IsNilVsEmpty(err error) bool { return errors.Is(err, errNilVsEmpty) }

// bipartiteMatcher lazily computes and caches the edge relation while
// running Kuhn's augmenting-path algorithm.
type bipartiteMatcher[E any] struct {
	x, y []E
	opts []Option

	edges map[[2]int]bool // cached results of Equal(x[i], y[j], opts...)
	left  int             // remaining comparison budget; <=0 means unlimited
	used  int             // number of comparisons performed
}

// edge reports whether x[i] and y[j] are equal, caching the result so each
// pair is compared at most once. It returns ErrMatchBudgetExceeded when the
// comparison budget is exhausted.
func (b *bipartiteMatcher[E]) edge(i, j int) (bool, error) {
	key := [2]int{i, j}
	if b.edges != nil {
		if v, ok := b.edges[key]; ok {
			return v, nil
		}
	}
	if b.left > 0 {
		b.left--
	} else if b.left == 0 {
		// A zero remaining budget means no further comparison is allowed;
		// a negative budget means unlimited.
		return false, ErrMatchBudgetExceeded
	}
	v := Equal(b.x[i], b.y[j], b.opts...)
	b.used++
	if b.edges == nil {
		b.edges = make(map[[2]int]bool)
	}
	b.edges[key] = v
	return v, nil
}

// augment tries to find an augmenting path starting at left vertex i,
// visiting right vertices in ascending index order for determinism.
func (b *bipartiteMatcher[E]) augment(i int, seen []bool, matchR, matchL []int) (bool, error) {
	for j := 0; j < len(b.y); j++ {
		if seen[j] {
			continue
		}
		ok, err := b.edge(i, j)
		if err != nil {
			return false, err
		}
		if !ok {
			continue
		}
		seen[j] = true
		if matchR[j] < 0 {
			matchR[j] = i
			matchL[i] = j
			return true, nil
		}
		// Try to relocate the left vertex currently matched to j.
		prev := matchR[j]
		ok, err = b.augment(prev, seen, matchR, matchL)
		if err != nil {
			return false, err
		}
		if ok {
			matchR[j] = i
			matchL[i] = j
			return true, nil
		}
	}
	return false, nil
}
