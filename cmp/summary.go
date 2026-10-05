// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/google/go-cmp/cmp/internal/value"
)

// ErrCountOverflow is returned by [Summarizer.Summary] when a difference
// counter would have overflowed. Instead of returning a misleading summary
// with wrapped-around counts, the summarizer reports this error and returns
// a zero [DiffSummary].
var ErrCountOverflow = errors.New("cmp: difference count overflow")

// DiffKind classifies a single reported difference as a deletion,
// an insertion, or a replacement relative to the x (old) value.
type DiffKind int

const (
	// Delete indicates a node present in x but missing from y.
	Delete DiffKind = iota
	// Insert indicates a node missing from x but present in y.
	Insert
	// Replace indicates a node present in both x and y with differing values.
	Replace
)

// String returns a short human-readable name for the difference kind.
func (k DiffKind) String() string {
	switch k {
	case Delete:
		return "delete"
	case Insert:
		return "insert"
	case Replace:
		return "replace"
	default:
		return "unknown"
	}
}

// DiffExample is a single sampled difference. It deliberately contains only
// the path and the node type; it never stores or formats the compared values,
// so constructing it never calls fmt.Stringer or error methods on user values
// and never leaks value contents into the summary.
type DiffExample struct {
	// Path is the concrete path to the differing node, built from step
	// metadata only (field names, slice indexes, and type names).
	// Map keys are rendered as their type, never their value.
	Path string
	// Type is the reflect type of the differing node.
	Type string
	// Kind classifies the difference.
	Kind DiffKind
}

// DiffGroup aggregates all differences that share the same group key.
// Counts are always exact, even when the number of retained Examples
// has been capped.
type DiffGroup struct {
	// Path is the normalized group path. Slice and map indexes are
	// normalized away so that all elements of the same collection
	// fall into a single group.
	Path     string
	Deletes  uint64
	Inserts  uint64
	Replaces uint64
	// Examples holds at most the configured maximum number of sampled
	// differences for this group.
	Examples []DiffExample
}

// Total returns the total number of differences in the group.
func (g DiffGroup) Total() uint64 { return g.Deletes + g.Inserts + g.Replaces }

// DiffSummary is the aggregated result collected by a [Summarizer].
type DiffSummary struct {
	// Groups lists the per-group statistics in first-seen order.
	Groups   []DiffGroup
	Deletes  uint64
	Inserts  uint64
	Replaces uint64
}

// Total returns the total number of differences across all groups.
func (s DiffSummary) Total() uint64 { return s.Deletes + s.Inserts + s.Replaces }

// Summarizer is a streaming Reporter that aggregates differences instead of
// materializing a full difference tree. Differences are grouped by a fixed
// grouping rule: the group key is derived from the caller path with slice and
// map indexes normalized away, so all elements of the same collection are
// counted together. The grouping rule is fixed at compile time and cannot be
// changed at runtime.
//
// Deletions, insertions, and replacements are counted separately. Only a
// bounded number of examples is retained per group, but the counters always
// reflect the exact totals. If any counter would overflow, Summary reports
// ErrCountOverflow rather than returning a misleading summary.
//
// The summarizer never formats compared values: examples contain only paths
// and types, so no fmt.Stringer or error methods of user values are called.
//
// Usage:
//
//	s := cmp.NewSummarizer(1)
//	cmp.Equal(x, y, cmp.Reporter(s))
//	summary, err := s.Summary()
type Summarizer struct {
	maxExamples int

	path   Path
	groups map[string]*diffGroup
	order  []*diffGroup // groups in first-seen order
	err    error
}

// diffGroup is the mutable, in-progress form of DiffGroup.
type diffGroup struct {
	key      string
	deletes  uint64
	inserts  uint64
	replaces uint64
	examples []DiffExample
}

// NewSummarizer returns a Summarizer that keeps at most maxExamples
// example differences per group. A negative maxExamples is treated as zero:
// counts are still exact, but no examples are retained.
func NewSummarizer(maxExamples int) *Summarizer {
	if maxExamples < 0 {
		maxExamples = 0
	}
	return &Summarizer{
		maxExamples: maxExamples,
		groups:      make(map[string]*diffGroup),
	}
}

// PushStep implements the reporter interface (see Reporter).
func (s *Summarizer) PushStep(ps PathStep) {
	s.path.push(ps)
}

// PopStep implements the reporter interface (see Reporter).
func (s *Summarizer) PopStep() {
	s.path.pop()
}

// Report implements the reporter interface (see Reporter).
// Equal and ignored results are not counted.
func (s *Summarizer) Report(rs Result) {
	if rs.Equal() || s.err != nil {
		return
	}
	kind := classifyDiff(s.path)
	key := groupKey(s.path)

	g, ok := s.groups[key]
	if !ok {
		g = &diffGroup{key: key}
		s.groups[key] = g
		s.order = append(s.order, g)
	}

	var c *uint64
	switch kind {
	case Delete:
		c = &g.deletes
	case Insert:
		c = &g.inserts
	default:
		c = &g.replaces
	}
	if *c == math.MaxUint64 {
		// Refuse to wrap the counter around; a wrapped count would
		// produce a misleadingly "complete" summary.
		s.err = ErrCountOverflow
		return
	}
	*c++

	if len(g.examples) < s.maxExamples {
		g.examples = append(g.examples, DiffExample{
			Path: examplePath(s.path),
			Type: typeString(s.path.Last().Type()),
			Kind: kind,
		})
	}
}

// Summary returns the aggregated differences collected so far.
// If any counter overflowed during collection, it returns a zero
// DiffSummary together with ErrCountOverflow instead of a misleading
// summary with wrapped-around counts.
func (s *Summarizer) Summary() (DiffSummary, error) {
	if s.err != nil {
		return DiffSummary{}, s.err
	}
	var sum DiffSummary
	sum.Groups = make([]DiffGroup, 0, len(s.order))
	for _, g := range s.order {
		var ok bool
		if sum.Deletes, ok = addUint64(sum.Deletes, g.deletes); !ok {
			return DiffSummary{}, ErrCountOverflow
		}
		if sum.Inserts, ok = addUint64(sum.Inserts, g.inserts); !ok {
			return DiffSummary{}, ErrCountOverflow
		}
		if sum.Replaces, ok = addUint64(sum.Replaces, g.replaces); !ok {
			return DiffSummary{}, ErrCountOverflow
		}
		examples := make([]DiffExample, len(g.examples))
		copy(examples, g.examples)
		sum.Groups = append(sum.Groups, DiffGroup{
			Path:     g.key,
			Deletes:  g.deletes,
			Inserts:  g.inserts,
			Replaces: g.replaces,
			Examples: examples,
		})
	}
	return sum, nil
}

// addUint64 returns a+b and whether the addition did not overflow.
func addUint64(a, b uint64) (uint64, bool) {
	c := a + b
	return c, c >= a
}

// classifyDiff determines whether the difference at the current path is a
// deletion, an insertion, or a replacement. Slice indexes are paired using
// their split keys: an index of -1 on either side means the element is
// missing from that side. For all other steps, the validity of the step
// values decides (e.g., a missing map entry).
func classifyDiff(pa Path) DiffKind {
	if si, ok := pa.Last().(SliceIndex); ok {
		ix, iy := si.SplitKeys()
		switch {
		case ix == -1:
			return Insert
		case iy == -1:
			return Delete
		default:
			return Replace
		}
	}
	vx, vy := pa.Last().Values()
	switch {
	case !vx.IsValid() && vy.IsValid():
		return Insert
	case vx.IsValid() && !vy.IsValid():
		return Delete
	default:
		return Replace
	}
}

// groupKey builds the normalized group path for the current path.
// The rule is fixed: struct field names, indirections, type assertions,
// and transforms are preserved; slice and map indexes are normalized to
// bare type placeholders so that all elements of the same collection
// share a single group. Only step metadata is used, never values.
func groupKey(pa Path) string {
	var sb strings.Builder
	for _, ps := range pa {
		switch s := ps.(type) {
		case StructField:
			sb.WriteString("." + s.Name())
		case SliceIndex:
			sb.WriteString("[]")
		case MapIndex:
			sb.WriteString("[" + mapKeyType(s) + "]")
		case Indirect:
			sb.WriteString("*")
		case TypeAssertion:
			sb.WriteString(".(" + typeString(s.Type()) + ")")
		case Transform:
			sb.WriteString(s.Name() + "()")
		default:
			// The operation-less root step.
			sb.WriteString("{" + typeString(ps.Type()) + "}")
		}
	}
	return sb.String()
}

// examplePath builds the concrete path for a single difference. It is
// identical to the group key except that slice indexes are rendered with
// their actual (integer) keys. Map keys are still rendered as their type
// only, so no value is ever formatted and no String method is called.
func examplePath(pa Path) string {
	var sb strings.Builder
	for _, ps := range pa {
		switch s := ps.(type) {
		case StructField:
			sb.WriteString("." + s.Name())
		case SliceIndex:
			ix, iy := s.SplitKeys()
			switch {
			case ix == iy:
				fmt.Fprintf(&sb, "[%d]", ix)
			case iy == -1:
				fmt.Fprintf(&sb, "[%d->?]", ix)
			case ix == -1:
				fmt.Fprintf(&sb, "[?->%d]", iy)
			default:
				fmt.Fprintf(&sb, "[%d->%d]", ix, iy)
			}
		case MapIndex:
			sb.WriteString("[" + mapKeyType(s) + "]")
		case Indirect:
			sb.WriteString("*")
		case TypeAssertion:
			sb.WriteString(".(" + typeString(s.Type()) + ")")
		case Transform:
			sb.WriteString(s.Name() + "()")
		default:
			sb.WriteString("{" + typeString(ps.Type()) + "}")
		}
	}
	return sb.String()
}

// mapKeyType returns the type of the map key as a string. The key value
// itself is never formatted, so a String method on the key type is never
// called and key values never leak into the summary.
func mapKeyType(mi MapIndex) string {
	k := mi.Key()
	if !k.IsValid() {
		return "?"
	}
	return typeString(k.Type())
}

// typeString renders a reflect.Type without ever touching a value.
func typeString(t reflect.Type) string {
	if t == nil {
		return "<nil>"
	}
	return value.TypeString(t, false)
}
