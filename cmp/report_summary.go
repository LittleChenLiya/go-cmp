// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// SummaryReporter is a streaming [Reporter] that groups detected differences
// by the caller path and counts them by kind (deletion, insertion, and
// replacement). It retains a bounded number of representative examples per
// group while always tracking the full, unbounded counts.
//
// The grouping key is the caller path: the sequence of struct field
// accesses from the root to the differing node. The grouping rules are fixed
// after compilation and cannot be reconfigured at runtime.
//
// Examples contain only the path and the type of the differing node. The
// String method is never called on values, so types with side-effecting
// Stringer implementations are safe to compare.
type SummaryReporter struct {
	maxExamples int
	maxGroups   int

	groups     map[string]*summaryGroup
	groupOrder []string
	path       Path
	overflow   bool
}

type summaryGroup struct {
	path        string
	numDeleted  int
	numInserted int
	numReplaced int
	examples    []SummaryExample
}

// SummaryExample is a retained representative of a difference detected at a
// given path. It contains only the path and the type of the differing node;
// no values are stored.
type SummaryExample struct {
	// Path is the caller path to the differing node, including slice index
	// pairing (e.g., "Clients[5->?]").
	Path string

	// Type is the type of the differing node. It is derived from
	// reflect.Type and never from a value's String method.
	Type reflect.Type
}

// SummaryOption configures a [SummaryReporter].
type SummaryOption func(*SummaryReporter)

// WithMaxExamples sets the maximum number of examples retained per group.
// The default is 10. The value must be at least 1.
func WithMaxExamples(n int) SummaryOption {
	if n < 1 {
		panic("cmp: max examples must be at least 1")
	}
	return func(r *SummaryReporter) { r.maxExamples = n }
}

// WithMaxGroups sets the maximum number of groups retained. The default is
// 1<<20. The value must be at least 1.
func WithMaxGroups(n int) SummaryOption {
	if n < 1 {
		panic("cmp: max groups must be at least 1")
	}
	return func(r *SummaryReporter) { r.maxGroups = n }
}

// NewSummaryReporter returns a [SummaryReporter] configured with the given
// options.
func NewSummaryReporter(opts ...SummaryOption) *SummaryReporter {
	r := &SummaryReporter{
		maxExamples: 10,
		maxGroups:   1 << 20,
		groups:      make(map[string]*summaryGroup),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// PushStep implements [Reporter].
func (r *SummaryReporter) PushStep(ps PathStep) {
	r.path = append(r.path, ps)
}

// Report implements [Reporter].
func (r *SummaryReporter) Report(rs Result) {
	if rs.Equal() || rs.ByIgnore() {
		return
	}
	r.record()
}

// PopStep implements [Reporter].
func (r *SummaryReporter) PopStep() {
	r.path = r.path[:len(r.path)-1]
}

func (r *SummaryReporter) record() {
	key := callerPath(r.path)
	g, ok := r.groups[key]
	if !ok {
		if len(r.groups) >= r.maxGroups {
			r.overflow = true
			return
		}
		g = &summaryGroup{path: key}
		r.groups[key] = g
		r.groupOrder = append(r.groupOrder, key)
	}

	deleted, inserted, replaced := diffKind(r.path)
	if deleted {
		incCount(&g.numDeleted, &r.overflow)
	}
	if inserted {
		incCount(&g.numInserted, &r.overflow)
	}
	if replaced {
		incCount(&g.numReplaced, &r.overflow)
	}

	if len(g.examples) < r.maxExamples {
		g.examples = append(g.examples, SummaryExample{
			Path: examplePath(r.path),
			Type: r.path.Last().Type(),
		})
	}
}

// incCount increments n, setting overflow if n would wrap past MaxInt.
func incCount(n *int, overflow *bool) {
	if *n == math.MaxInt {
		*overflow = true
		return
	}
	*n++
}

// callerPath builds the caller path (the sequence of struct field accesses)
// from the current path. It never invokes String on any value.
func callerPath(p Path) string {
	var b strings.Builder
	for _, s := range p {
		if sf, ok := s.(StructField); ok {
			b.WriteString(".")
			b.WriteString(sf.Name())
		}
	}
	return strings.TrimPrefix(b.String(), ".")
}

// examplePath builds the path to a differing node, including slice index
// pairing. It never invokes String on any value.
func examplePath(p Path) string {
	var b strings.Builder
	for _, s := range p {
		switch s := s.(type) {
		case StructField:
			b.WriteString(".")
			b.WriteString(s.Name())
		case SliceIndex:
			ix, iy := s.SplitKeys()
			b.WriteString("[")
			if ix < 0 {
				b.WriteString("?")
			} else {
				b.WriteString(strconv.Itoa(ix))
			}
			if ix != iy {
				b.WriteString("->")
				if iy < 0 {
					b.WriteString("?")
				} else {
					b.WriteString(strconv.Itoa(iy))
				}
			}
			b.WriteString("]")
		case MapIndex:
			// Map keys are values; use a placeholder so that values are never
			// leaked and String is never called on a key.
			b.WriteString("[]")
		}
	}
	return strings.TrimPrefix(b.String(), ".")
}

// diffKind classifies a difference as a deletion, insertion, or replacement
// based on the innermost slice or map step. A missing element in y is a
// deletion; a missing element in x is an insertion; anything else is a
// replacement.
func diffKind(p Path) (deleted, inserted, replaced bool) {
	for i := len(p) - 1; i >= 0; i-- {
		switch s := p[i].(type) {
		case SliceIndex:
			ix, iy := s.SplitKeys()
			switch {
			case ix < 0:
				return false, true, false
			case iy < 0:
				return true, false, false
			default:
				return false, false, true
			}
		case MapIndex:
			vx, vy := s.Values()
			switch {
			case !vx.IsValid():
				return false, true, false
			case !vy.IsValid():
				return true, false, false
			default:
				return false, false, true
			}
		}
	}
	return false, false, true
}

// Summary is the accumulated summary of differences grouped by caller path.
type Summary struct {
	// Groups are ordered by first occurrence.
	Groups []*SummaryGroup
}

// SummaryGroup is a group of differences sharing the same caller path.
type SummaryGroup struct {
	// Path is the caller path shared by all differences in this group.
	Path string

	// NumDeleted is the number of elements removed from x.
	NumDeleted int

	// NumInserted is the number of elements added to y.
	NumInserted int

	// NumReplaced is the number of elements that differ but are present in
	// both x and y.
	NumReplaced int

	// Examples contains up to the configured maximum number of
	// representatives. The counts are always total; examples are bounded.
	Examples []SummaryExample
}

// Summary returns the accumulated summary. It returns an error if any count
// overflowed or if the number of groups exceeded the configured maximum. In
// those cases the summary is incomplete and must not be used.
func (r *SummaryReporter) Summary() (*Summary, error) {
	if r.overflow {
		return nil, errors.New("cmp: summary reporter: count overflow: summary is incomplete")
	}
	s := &Summary{}
	for _, key := range r.groupOrder {
		g := r.groups[key]
		sg := &SummaryGroup{
			Path:        g.path,
			NumDeleted:  g.numDeleted,
			NumInserted: g.numInserted,
			NumReplaced: g.numReplaced,
		}
		sg.Examples = make([]SummaryExample, len(g.examples))
		copy(sg.Examples, g.examples)
		s.Groups = append(s.Groups, sg)
	}
	return s, nil
}
