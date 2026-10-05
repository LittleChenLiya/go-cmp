// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/google/go-cmp/cmp/internal/value"
)

// AuditStatus describes how the comparison engine treated a single node
// in the value tree during a traversal observed by an [AuditReporter].
type AuditStatus int

const (
	// AuditUnvisited indicates that the engine never made a comparison
	// decision at this node itself. This is the state of interior nodes
	// (structs, slices, maps, pointers, interfaces) that the engine only
	// descended through: their children, if any, carry the actual outcomes.
	//
	// Branches that the engine never entered at all (for example, the
	// children of a node masked by an [Ignore] option or handled by a
	// [Comparer] function) do not even appear in the audit tree, so an
	// unvisited node must never be interpreted as "compared" or "equal".
	AuditUnvisited AuditStatus = iota

	// AuditCompared indicates that the engine directly compared this node
	// as a leaf and reached an equality decision for it.
	AuditCompared

	// AuditIgnored indicates that this node was masked by an [Ignore]
	// option. The node is reported as equal for the purpose of [Equal],
	// but none of its content was actually compared.
	AuditIgnored
)

// String returns a human-readable name for the status.
func (s AuditStatus) String() string {
	switch s {
	case AuditCompared:
		return "compared"
	case AuditIgnored:
		return "ignored"
	default:
		return "unvisited"
	}
}

// AuditNode is a single node in the audit tree built by an [AuditReporter].
// It records only path metadata and the comparison state of the node;
// it never stores a copy of the compared values themselves.
type AuditNode struct {
	// Step is the string form of this single path step,
	// e.g. ".Secret", "[2]", `["key"]`, "*", ".(T)", or "Name()".
	Step string

	// Type is the type of the node at this step.
	// This is type metadata only, not a compared value.
	Type reflect.Type

	// Status reports whether this node was compared, ignored,
	// or never directly decided upon (see [AuditStatus]).
	Status AuditStatus

	// Equal is the comparison outcome. It is only meaningful
	// when Status is [AuditCompared].
	Equal bool

	// ByFunc reports whether a [Comparer] function determined equality.
	ByFunc bool

	// ByMethod reports whether an Equal method determined equality.
	ByMethod bool

	// ByCycle reports whether a reference cycle was detected at this node.
	ByCycle bool

	// Cycle, if non-nil, points to the ancestor node that first visited
	// the same pointer identity. It correlates a cyclic reference back to
	// the node it refers to, mirroring the cycle detection of [Equal].
	Cycle *AuditNode

	// Children are the nodes visited directly beneath this node,
	// in the order the comparison engine visited them.
	// A node masked by an Ignore option or decided by a Comparer
	// has no children because its subtree was never visited.
	Children []*AuditNode

	parent *AuditNode
	kind   auditStepKind
	pairs  []auditPtrPair
}

// auditPtrPair is a pair of pointer identities that the comparison
// engine pushed onto its pointer stack for a single node.
// Only addresses are recorded, never values.
type auditPtrPair struct {
	px, py value.Pointer
}

type auditStepKind byte

const (
	auditSuffix auditStepKind = iota // root, SliceIndex, MapIndex, TypeAssertion
	auditField
	auditIndirect
	auditTransform
)

// Path returns the real path from the root to this node in Go syntax,
// preserving anonymous struct fields and slice indices,
// e.g. "{pkg.Config}.Tags[1].Name".
func (n *AuditNode) Path() string {
	var chain []*AuditNode
	for m := n; m != nil; m = m.parent {
		chain = append(chain, m)
	}
	// Reverse so that the chain is ordered from root to this node.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	// Render the chain in the same manner as Path.GoString.
	var ssPre, ssPost []string
	var numIndirect int
	for i, s := range chain {
		var next *AuditNode
		if i+1 < len(chain) {
			next = chain[i+1]
		}
		switch s.kind {
		case auditIndirect:
			numIndirect++
			pPre, pPost := "(", ")"
			switch {
			case next == nil:
				pPre, pPost = "", "" // Last step; no need for parenthesis
			case next.kind == auditIndirect:
				continue // Next step is indirection, so let them batch up
			case next.kind == auditField:
				numIndirect-- // Automatic indirection on struct fields
			}
			if numIndirect > 0 {
				ssPre = append(ssPre, pPre+strings.Repeat("*", numIndirect))
				ssPost = append(ssPost, pPost)
			}
			numIndirect = 0
		case auditTransform:
			name := strings.TrimSuffix(s.Step, "()")
			ssPre = append(ssPre, name+"(")
			ssPost = append(ssPost, ")")
		default:
			ssPost = append(ssPost, s.Step)
		}
	}
	for i, j := 0, len(ssPre)-1; i < j; i, j = i+1, j-1 {
		ssPre[i], ssPre[j] = ssPre[j], ssPre[i]
	}
	return strings.Join(ssPre, "") + strings.Join(ssPost, "")
}

// Find returns the first node in the subtree rooted at n whose
// [AuditNode.Path] exactly matches the given path, or nil if there is none.
func (n *AuditNode) Find(path string) *AuditNode {
	if n == nil {
		return nil
	}
	if n.Path() == path {
		return n
	}
	for _, c := range n.Children {
		if m := c.Find(path); m != nil {
			return m
		}
	}
	return nil
}

// describe returns a one-line human-readable summary of the node.
func (n *AuditNode) describe() string {
	switch n.Status {
	case AuditIgnored:
		return "IGNORED (masked by an Ignore option; subtree not visited)"
	case AuditCompared:
		eq := "equal"
		if !n.Equal {
			eq = "unequal"
		}
		var how string
		switch {
		case n.ByCycle:
			if n.Cycle != nil {
				how = fmt.Sprintf("by cycle, identity-matched to %s", n.Cycle.Path())
			} else {
				how = "by cycle"
			}
		case n.ByFunc:
			how = "by Comparer"
		case n.ByMethod:
			how = "by Equal method"
		default:
			how = "by built-in comparison"
		}
		return fmt.Sprintf("compared %s (%s)", eq, how)
	default:
		return "unvisited (traversed, but never directly compared)"
	}
}

// AuditReporter is a [Reporter] that audits a single comparison pass,
// recording which real paths the comparison engine actually visited,
// which of them were masked by [Ignore] options, and which were
// genuinely compared.
//
// Its purpose is to prevent a final "equal" verdict from being
// misinterpreted as "all fields are identical": nodes masked by Ignore
// are explicitly listed as ignored, and branches the engine never
// entered are absent from the tree rather than marked as compared.
//
// The reporter observes the same single traversal that computes the
// result; it does not re-run the comparison, so [Comparer] functions
// are invoked exactly as often as in a comparison without the reporter.
// Cyclic references are correlated by pointer identity, mirroring the
// cycle detection performed by [Equal]. The report contains only path
// metadata (steps, types, and map keys as path components); it never
// stores copies of the compared values.
//
// The zero value is ready to use. If the reporter is reused for another
// comparison after a previous traversal has completed, it resets itself.
// An AuditReporter is not safe for concurrent use.
type AuditReporter struct {
	root  *AuditNode
	stack []*AuditNode
}

// NewAuditReporter returns a ready-to-use [AuditReporter].
// Pass it to [Equal] or [Diff] via [Reporter]:
//
//	ar := cmp.NewAuditReporter()
//	eq := cmp.Equal(x, y, cmp.Reporter(ar), opt1, opt2)
func NewAuditReporter() *AuditReporter {
	return new(AuditReporter)
}

// PushStep implements the reporter interface (see [Reporter]).
func (r *AuditReporter) PushStep(ps PathStep) {
	if len(r.stack) == 0 {
		r.root = nil // Reset any previous completed traversal.
	}
	n := &AuditNode{Step: ps.String(), Type: ps.Type(), Status: AuditUnvisited}
	switch ps.(type) {
	case StructField:
		n.kind = auditField
	case Indirect:
		n.kind = auditIndirect
	case Transform:
		n.kind = auditTransform
	}
	if len(r.stack) > 0 {
		p := r.stack[len(r.stack)-1]
		n.parent = p
		p.Children = append(p.Children, n)
	} else {
		r.root = n
	}

	// Track pointer identity exactly as the comparison engine does,
	// so that cyclic references can be correlated back to the node
	// that first visited the same address.
	n.pairs = auditPointerPairs(ps)
	for _, np := range n.pairs {
		for i := len(r.stack) - 1; i >= 0 && n.Cycle == nil; i-- {
			for _, mp := range r.stack[i].pairs {
				if mp.px == np.px || mp.py == np.py {
					n.Cycle = r.stack[i]
					break
				}
			}
		}
	}
	r.stack = append(r.stack, n)
}

// Report implements the reporter interface (see [Reporter]).
func (r *AuditReporter) Report(rs Result) {
	n := r.stack[len(r.stack)-1]
	if rs.ByIgnore() {
		n.Status = AuditIgnored
	} else {
		n.Status = AuditCompared
		n.Equal = rs.Equal()
	}
	n.ByFunc = rs.ByFunc()
	n.ByMethod = rs.ByMethod()
	n.ByCycle = rs.ByCycle()
}

// PopStep implements the reporter interface (see [Reporter]).
func (r *AuditReporter) PopStep() {
	r.stack = r.stack[:len(r.stack)-1]
}

// Root returns the root of the audit tree recorded during the last
// traversal, or nil if no traversal has occurred yet.
func (r *AuditReporter) Root() *AuditNode {
	return r.root
}

// Ignored returns the real paths of all nodes that were masked by an
// [Ignore] option during the last traversal, in the order they were
// visited. A non-empty result means the final equality verdict must not
// be read as "all fields are identical".
func (r *AuditReporter) Ignored() []string {
	var paths []string
	var walk func(n *AuditNode)
	walk = func(n *AuditNode) {
		if n.Status == AuditIgnored {
			paths = append(paths, n.Path())
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	if r.root != nil {
		walk(r.root)
	}
	return paths
}

// String returns a human-readable audit report of the last traversal
// as an indented tree. Each line shows a path step and how the
// comparison engine treated that node.
func (r *AuditReporter) String() string {
	if r.root == nil {
		return ""
	}
	var b strings.Builder
	var walk func(n *AuditNode, depth int)
	walk = func(n *AuditNode, depth int) {
		fmt.Fprintf(&b, "%s%s: %s\n", strings.Repeat("  ", depth), n.Step, n.describe())
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(r.root, 0)
	return b.String()
}

// auditPointerPairs extracts the pointer identities that the comparison
// engine pushes onto its pointer stack while comparing the node for the
// given step, mirroring the cycle-detection logic in state.compareAny,
// state.comparePtr, and state.compareMap. Only addresses are recorded,
// never values.
func auditPointerPairs(ps PathStep) (pairs []auditPtrPair) {
	vx, vy := ps.Values()
	if si, isSliceIndex := ps.(SliceIndex); isSliceIndex {
		// Slice elements are tracked by their address (see compareAny).
		if !si.isSlice || !vx.IsValid() || !vy.IsValid() {
			return nil
		}
		pairs = append(pairs, auditPtrPair{value.PointerOf(vx.Addr()), value.PointerOf(vy.Addr())})
	}
	// A node holding a pointer or map (including a slice element of
	// pointer type) is also tracked by its value (see comparePtr and
	// compareMap, which run within the same compareAny frame).
	switch ps.Type().Kind() {
	case reflect.Ptr, reflect.Map:
		if vx.IsValid() && vy.IsValid() && !vx.IsNil() && !vy.IsNil() {
			pairs = append(pairs, auditPtrPair{value.PointerOf(vx), value.PointerOf(vy)})
		}
	}
	return pairs
}
