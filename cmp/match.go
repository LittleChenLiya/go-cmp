package cmp

import "fmt"

// BudgetExceededError is returned by MatchSlices when the number of element
// comparisons performed exceeds MatchSettings.MaxComparisons.
// It is a distinct error and is never reported as a mere mismatch.
type BudgetExceededError struct {
	// Limit is the configured maximum number of element comparisons.
	Limit int
}

// Error implements the error interface.
func (e BudgetExceededError) Error() string {
	return fmt.Sprintf("cmp: element comparison budget of %d exceeded", e.Limit)
}

// MatchSettings configures the behavior of MatchSlices.
// The zero value is valid and means an unlimited comparison budget with
// nil and empty slices treated as different.
type MatchSettings struct {
	// MaxComparisons limits the number of element-pair comparisons performed.
	// Each unordered pair of indices is compared at most once.
	// A value <= 0 means no limit.
	// If the budget is exceeded, MatchSlices returns a BudgetExceededError
	// instead of a matching result.
	MaxComparisons int

	// NilEqualsEmpty controls whether a nil slice is considered equal to an
	// empty, non-nil slice. By default they are considered different.
	NilEqualsEmpty bool
}

// MatchPair records a one-to-one correspondence between the element at
// index Left of the first slice and the element at index Right of the
// second slice passed to MatchSlices.
type MatchPair struct {
	Left  int
	Right int
}

// Matching is the report produced by MatchSlices.
// It describes a stable maximum bipartite matching between the elements of
// the two input slices. Indices always refer to the original, unmodified
// input slices, which are never mutated, sorted, or copied.
type Matching struct {
	// Pairs lists the matched index pairs, sorted by ascending Left index.
	// Each left and each right index appears at most once.
	Pairs []MatchPair

	// UnmatchedLeft lists the indices of the first slice with no match,
	// in ascending order.
	UnmatchedLeft []int

	// UnmatchedRight lists the indices of the second slice with no match,
	// in ascending order.
	UnmatchedRight []int

	// Comparisons is the number of element-pair comparisons performed.
	Comparisons int

	// nilMismatch reports that exactly one input was nil while
	// MatchSettings.NilEqualsEmpty was false.
	nilMismatch bool
}

// Perfect reports whether the matching covers every element of both slices,
// i.e. whether the two slices are equal as unordered multisets under the
// given options.
func (m Matching) Perfect() bool {
	return !m.nilMismatch && len(m.UnmatchedLeft) == 0 && len(m.UnmatchedRight) == 0
}

// RightOf returns the right index matched with left index i, if any.
func (m Matching) RightOf(i int) (int, bool) {
	for _, p := range m.Pairs {
		if p.Left == i {
			return p.Right, true
		}
	}
	return 0, false
}

// LeftOf returns the left index matched with right index j, if any.
func (m Matching) LeftOf(j int) (int, bool) {
	for _, p := range m.Pairs {
		if p.Right == j {
			return p.Left, true
		}
	}
	return 0, false
}

// MatchSlices compares two slices as unordered multisets.
//
// An element x[i] may be paired with y[j] exactly when
// Equal(x[i], y[j], opts...) reports true. The slices are considered equal
// (Matching.Perfect) exactly when there exists a one-to-one matching that
// covers every element of both slices; duplicate elements must each be
// matched by a distinct counterpart and are never reused.
//
// The pairwise relation is not assumed to be transitive or otherwise an
// equivalence relation, and no greedy pairing is performed. MatchSlices
// computes a maximum bipartite matching with the augmenting-path method:
// left vertices and their candidate edges are always visited in ascending
// original index order, so the resulting matching is deterministic and
// stable for a given input.
//
// MatchSlices performs at most len(x)*len(y) element comparisons (O(n*m))
// and the matching itself runs in O((n+m)*n*m) time with O(n*m) space,
// where n = len(x) and m = len(y).
//
// The input slices are never modified, sorted, or deep-copied; all indices
// in the returned Matching refer to the original slices.
//
// Panics raised by element comparisons (e.g. by a custom Comparer in opts)
// propagate to the caller unchanged; they are never recovered and converted
// into a mismatch or an error.
//
// MatchSlices must not be invoked, directly or indirectly, from a Comparer
// or Transformer in opts that applies to the same element types, as that
// would cause infinite recursion.
func MatchSlices[E any](x, y []E, settings MatchSettings, opts ...Option) (Matching, error) {
	if !settings.NilEqualsEmpty && (x == nil) != (y == nil) {
		// A nil slice differs from a non-nil (possibly empty) slice.
		var res Matching
		res.nilMismatch = true
		for i := range x {
			res.UnmatchedLeft = append(res.UnmatchedLeft, i)
		}
		for j := range y {
			res.UnmatchedRight = append(res.UnmatchedRight, j)
		}
		return res, nil
	}

	nx, ny := len(x), len(y)
	mt := &matcher[E]{
		x:     x,
		y:     y,
		opts:  opts,
		limit: settings.MaxComparisons,
		memo:  make(map[[2]int]bool, nx*ny),
	}

	matchL := make([]int, nx)
	matchR := make([]int, ny)
	for i := range matchL {
		matchL[i] = -1
	}
	for j := range matchR {
		matchR[j] = -1
	}

	// Kuhn's augmenting-path algorithm. Left vertices are processed in
	// ascending index order and each DFS visits right vertices in ascending
	// index order, yielding a deterministic maximum matching.
	for u := 0; u < nx; u++ {
		seen := make([]bool, ny)
		mt.augment(u, matchL, matchR, seen)
		if mt.err != nil {
			return Matching{Comparisons: mt.count}, mt.err
		}
	}

	res := Matching{Comparisons: mt.count}
	for i, j := range matchL {
		if j >= 0 {
			res.Pairs = append(res.Pairs, MatchPair{Left: i, Right: j})
		} else {
			res.UnmatchedLeft = append(res.UnmatchedLeft, i)
		}
	}
	for j := range matchR {
		if matchR[j] < 0 {
			res.UnmatchedRight = append(res.UnmatchedRight, j)
		}
	}
	return res, nil
}

// matcher evaluates and memoizes element-pair edges under a budget.
type matcher[E any] struct {
	x, y  []E
	opts  []Option
	memo  map[[2]int]bool
	count int
	limit int
	err   error
}

// edge reports whether x[i] may be paired with y[j].
// Each index pair is compared at most once; results are memoized.
func (m *matcher[E]) edge(i, j int) bool {
	k := [2]int{i, j}
	if b, ok := m.memo[k]; ok {
		return b
	}
	if m.err != nil {
		return false
	}
	if m.limit > 0 && m.count >= m.limit {
		m.err = BudgetExceededError{Limit: m.limit}
		return false
	}
	m.count++
	b := Equal(m.x[i], m.y[j], m.opts...)
	m.memo[k] = b
	return b
}

// augment searches for an augmenting path starting at left vertex u,
// visiting right vertices in ascending index order.
func (m *matcher[E]) augment(u int, matchL, matchR []int, seen []bool) bool {
	for v := 0; v < len(m.y); v++ {
		if seen[v] || !m.edge(u, v) {
			continue
		}
		seen[v] = true
		w := matchR[v]
		if w < 0 || m.augment(w, matchL, matchR, seen) {
			matchL[u] = v
			matchR[v] = u
			return true
		}
	}
	return false
}
