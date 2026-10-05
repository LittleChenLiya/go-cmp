// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"fmt"
	"reflect"
	"strings"
)

// AuditState is the comparison state of a leaf node recorded by an
// AuditReporter.
type AuditState int

const (
	// AuditIgnored indicates that the node was obscured by an Ignore option.
	// The node itself was not compared and none of its descendants were
	// visited by the comparison engine.
	AuditIgnored AuditState = iota
	// AuditEqual indicates that the node was compared and determined equal.
	AuditEqual
	// AuditUnequal indicates that the node was compared and determined
	// unequal.
	AuditUnequal
	// AuditCycle indicates that a reference cycle was detected at the node.
	// The node was not compared by traversal; instead it is associated by
	// identity with the earlier occurrence recorded in AuditRecord.Link.
	AuditCycle
)

// String returns the name of the audit state.
func (s AuditState) String() string {
	switch s {
	case AuditIgnored:
		return "ignored"
	case AuditEqual:
		return "equal"
	case AuditUnequal:
		return "unequal"
	case AuditCycle:
		return "cycle"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// AuditRecord describes a single leaf node that the comparison engine
// visited while an AuditReporter was attached.
type AuditRecord struct {
	// Path is the real path from the root to the node. It preserves
	// anonymous (embedded) fields and slice indices, for example
	// "{pkg.Root}.Inner.Items[2].Secret". It never contains the compared
	// field values.
	Path string
	// State is the comparison state of the node.
	State AuditState
	// Equal reports whether the node is equal. It is only meaningful for
	// compared nodes (AuditEqual, AuditUnequal) and cycles (AuditCycle).
	Equal bool
	// Link is the path of the earlier occurrence that a cycle node is
	// associated with by identity. It is empty when State is not
	// AuditCycle or the earlier occurrence was not recorded.
	Link string
}

// AuditReporter is a [Reporter] that records an audit trail of a comparison:
// which real paths were obscured by [Ignore] options, which paths were
// actually compared, and where reference cycles were detected.
//
// The reporter is purely observational: it does not influence the
// comparison, does not re-run any [Comparer], and does not retain the
// values being compared. It only records paths and states along the
// comparison engine's actual traversal. As a consequence, branches that the
// engine never visits (for example, the children of an ignored node) are
// absent from the report and must not be interpreted as compared.
//
// The final comparison result being equal must not be interpreted as every
// field being equal: ignored paths were never compared at all.
type AuditReporter struct {
	records []AuditRecord
	path    Path
	seenX   map[uintptr]string
	seenY   map[uintptr]string
}

// NewAuditReporter returns a new AuditReporter.
func NewAuditReporter() *AuditReporter {
	return &AuditReporter{
		seenX: make(map[uintptr]string),
		seenY: make(map[uintptr]string),
	}
}

// PushStep implements Reporter.
func (r *AuditReporter) PushStep(ps PathStep) {
	if r.seenX == nil {
		r.seenX = make(map[uintptr]string)
	}
	if r.seenY == nil {
		r.seenY = make(map[uintptr]string)
	}
	r.path.push(ps)

	// Record the identity of the values at this node so that cycles can be
	// associated by identity. This mirrors the cycle detection performed by
	// the comparison engine (see pointerPath).
	vx, vy := ps.Values()
	if key := identityKey(vx, ps); key != 0 {
		if _, ok := r.seenX[key]; !ok {
			r.seenX[key] = r.pathString()
		}
	}
	if key := identityKey(vy, ps); key != 0 {
		if _, ok := r.seenY[key]; !ok {
			r.seenY[key] = r.pathString()
		}
	}
}

// Report implements Reporter.
func (r *AuditReporter) Report(rs Result) {
	rec := AuditRecord{Path: r.pathString()}
	vx, vy := r.path.Last().Values()
	switch {
	case rs.ByIgnore():
		rec.State = AuditIgnored
	case rs.ByCycle():
		rec.State = AuditCycle
		rec.Equal = rs.Equal()
		// Associate the cycle with the earlier occurrence by identity.
		// The earlier occurrence may be on the x side, the y side, or both.
		if key := identityKey(vx, r.path.Last()); key != 0 {
			if link, ok := r.seenX[key]; ok {
				rec.Link = link
			}
		}
		if rec.Link == "" {
			if key := identityKey(vy, r.path.Last()); key != 0 {
				if link, ok := r.seenY[key]; ok {
					rec.Link = link
				}
			}
		}
	case rs.Equal():
		rec.State = AuditEqual
	default:
		rec.State = AuditUnequal
	}
	r.records = append(r.records, rec)
}

// PopStep implements Reporter.
func (r *AuditReporter) PopStep() {
	r.path.pop()
}

// Records returns a copy of the audit records, one for every leaf node the
// comparison engine visited.
func (r *AuditReporter) Records() []AuditRecord {
	return append([]AuditRecord(nil), r.records...)
}

// IgnoredPaths returns the paths of all nodes obscured by Ignore options.
func (r *AuditReporter) IgnoredPaths() []string {
	var paths []string
	for _, rec := range r.records {
		if rec.State == AuditIgnored {
			paths = append(paths, rec.Path)
		}
	}
	return paths
}

// String returns a human-readable audit report.
func (r *AuditReporter) String() string {
	var sb strings.Builder
	for _, rec := range r.records {
		fmt.Fprintf(&sb, "%s: %s", rec.Path, rec.State)
		if rec.State == AuditCycle && rec.Link != "" {
			fmt.Fprintf(&sb, " (same identity as %s)", rec.Link)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// pathString builds the real path from the current path steps. Unlike
// Path.String, it preserves slice indices, map keys, and other step
// information in addition to struct field names.
func (r *AuditReporter) pathString() string {
	var sb strings.Builder
	for _, s := range r.path {
		sb.WriteString(s.String())
	}
	return sb.String()
}

// identityKey returns a key identifying the underlying object of v for
// cycle association. It returns 0 if the value has no identity that the
// comparison engine would track.
func identityKey(v reflect.Value, ps PathStep) uintptr {
	if !v.IsValid() {
		return 0
	}
	if si, ok := ps.(SliceIndex); ok && si.isSlice {
		// The engine tracks per-element addresses for slices.
		if v.CanAddr() {
			return v.Addr().Pointer()
		}
		return 0
	}
	switch v.Kind() {
	case reflect.Ptr:
		if !v.IsNil() {
			return v.Pointer()
		}
	case reflect.Map:
		if !v.IsNil() {
			return v.Pointer()
		}
	}
	return 0
}
