// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"fmt"
	"reflect"
)

// Default limits for [MachineDiff].
const (
	defaultMaxEntries      = 1000
	defaultMaxSummaryBytes = 1024
)

// machineConfig configures a [MachineDiff] report. It is populated through
// [MachineOption] values and never collides with equality [Option] values.
type machineConfig struct {
	maxEntries      int
	maxSummaryBytes int
}

// MachineOption configures [MachineDiff]. MachineOption values are not
// equality [Option] values: they only control the shape of the machine
// report and never affect comparison semantics.
type MachineOption interface {
	applyMachine(*machineConfig)
}

type machineOptionFunc func(*machineConfig)

func (f machineOptionFunc) applyMachine(c *machineConfig) { f(c) }

// WithMaxEntries limits the number of difference entries retained in the
// report. When more unequal leaf nodes are found, the report retains the
// first n entries in deterministic traversal order, sets
// MachineReport.Truncated, and still records the full count in
// MachineReport.NumDifferences. The comparison always runs to completion,
// so truncation of entries does not imply the comparison is incomplete.
// A non-positive value means unlimited. If WithMaxEntries is not provided,
// a default of 1000 entries is used.
func WithMaxEntries(n int) MachineOption {
	return machineOptionFunc(func(c *machineConfig) { c.maxEntries = n })
}

// WithMaxSummaryBytes limits the byte length of each value summary text
// produced for MachineDifference.X and MachineDifference.Y. When rendering
// exceeds the limit, the summary ends with "..." and its Truncated field is
// set. A non-positive value means unlimited. If WithMaxSummaryBytes is not
// provided, a default of 1024 bytes is used.
func WithMaxSummaryBytes(n int) MachineOption {
	return machineOptionFunc(func(c *machineConfig) { c.maxSummaryBytes = n })
}

// MachineDiff compares x and y using exactly the same engine and equality
// [Option] values as [Equal] and [Diff], but returns a machine-readable
// [MachineReport] instead of textual output. The textual contract of [Diff]
// is unchanged.
//
// The report records each unequal leaf node with its structural path,
// difference kind, the left and right types, whether a [Transformer] was
// applied on the path, and length-limited redacted value summaries. The
// same [Ignore], [Comparer], Equal-method, and [Transformer] semantics
// apply, and unexported fields remain protected.
//
// MachineOption values configure the report (for example, [WithMaxEntries]
// and [WithMaxSummaryBytes]); any other value must be an equality [Option]
// and is forwarded to the comparison engine.
func MachineDiff(x, y any, opts ...any) (report *MachineReport) {
	cfg := machineConfig{maxEntries: defaultMaxEntries, maxSummaryBytes: defaultMaxSummaryBytes}
	var eqOpts []Option
	for _, opt := range opts {
		switch opt := opt.(type) {
		case nil:
		case machineOptionFunc:
			opt.applyMachine(&cfg)
		case Option:
			eqOpts = append(eqOpts, opt)
		default:
			panic(fmt.Sprintf("unknown option %T", opt))
		}
	}
	if cfg.maxEntries == 0 {
		cfg.maxEntries = defaultMaxEntries
	}
	if cfg.maxSummaryBytes == 0 {
		cfg.maxSummaryBytes = defaultMaxSummaryBytes
	}

	rep := &machineReporter{maxEntries: cfg.maxEntries, maxSummaryBytes: cfg.maxSummaryBytes}
	s := newState(eqOpts)
	s.reporters = append(s.reporters, reporter{rep})

	report = &MachineReport{}
	defer func() {
		if r := recover(); r != nil {
			report.Complete = false
			report.Panic = fmt.Sprint(r)
			report.Truncated = report.Truncated || rep.truncated
			report.NumDifferences = rep.numDiffs
			report.Differences = rep.diffs
			report.Equal = false
		}
	}()

	s.compareAny(rootStep(x, y))
	report.Complete = true
	report.Truncated = rep.truncated
	report.NumDifferences = rep.numDiffs
	report.Differences = rep.diffs
	report.Equal = rep.numDiffs == 0
	return report
}

// MachineReport is a machine-readable report of the differences between two
// values. It is produced by [MachineDiff] and can be marshaled directly as
// JSON, so callers that need to react to differences do not have to parse the
// textual output of [Diff].
//
// The equality semantics are exactly those of [Equal] and [Diff]: the same
// [Comparer], [Transformer], [Ignore], and Equal-method rules apply and
// unexported fields remain protected. Only the output representation differs.
//
// Value summaries are redacted and length-limited: they never invoke a
// value's String or GoString method, never print pointer addresses, and
// stop traversing when a reference cycle is encountered.
type MachineReport struct {
	// Equal reports whether the compared values are equal according to the
	// provided options, exactly as [Equal] would report. Equal must only be
	// trusted when Complete is true; an incomplete comparison that observed
	// no differences must not be interpreted as "all values are equal".
	Equal bool `json:"equal"`
	// Complete reports whether the comparison ran to completion and every
	// observed difference is accounted for. It is false if the comparison
	// panicked, for example because an unexported field was neither ignored
	// nor made accessible with an [Exporter].
	Complete bool `json:"complete"`
	// Panic records the recovered panic value when Complete is false;
	// it is empty otherwise.
	Panic string `json:"panic,omitempty"`
	// Truncated reports whether at least one difference entry was omitted
	// because the [WithMaxEntries] limit was reached.
	Truncated bool `json:"truncated"`
	// NumDifferences is the total number of unequal leaf nodes observed,
	// including entries omitted due to truncation. The comparison still
	// runs to completion when entries are truncated, so this count remains
	// accurate in that case.
	NumDifferences int `json:"num_differences"`
	// Differences contains the collected difference entries in the engine's
	// deterministic traversal order. Map entries are visited in sorted-key
	// order, so the sequence is stable across runs.
	Differences []MachineDifference `json:"differences"`
}

// DifferenceKind classifies why a leaf node is unequal.
type DifferenceKind string

const (
	// KindMismatch indicates that both sides have a value, but the values
	// differ. This covers unequal primitives, leaves decided by a [Comparer]
	// or Equal method, non-nil interface values with different dynamic
	// types, and reference cycles compared as structurally different.
	KindMismatch DifferenceKind = "mismatch"
	// KindMissing indicates a slice element or map entry present in x that
	// is absent from y (a removal).
	KindMissing DifferenceKind = "missing"
	// KindExtra indicates a slice element or map entry absent from x that
	// is present in y (an insertion).
	KindExtra DifferenceKind = "extra"
	// KindNil indicates that one side is nil and the other is not.
	KindNil DifferenceKind = "nil_mismatch"
)

// DifferenceDecision records which equality mechanism decided a leaf node.
type DifferenceDecision struct {
	// ByMethod reports whether the type's Equal method decided the node.
	ByMethod bool `json:"by_method,omitempty"`
	// ByComparer reports whether a [Comparer] option decided the node.
	ByComparer bool `json:"by_comparer,omitempty"`
	// ByCycle reports whether a reference cycle was detected at the node.
	ByCycle bool `json:"by_cycle,omitempty"`
}

// MachineDifference describes a single unequal leaf node.
type MachineDifference struct {
	// Path is the structural location of the difference, preserving the
	// StructField, SliceIndex, MapIndex, TypeAssertion, Indirect, and
	// Transform steps taken by the comparison engine.
	Path []MachinePathStep `json:"path"`
	// Kind classifies the difference.
	Kind DifferenceKind `json:"kind"`
	// XType and YType are the Go type names of the x and y values. A type
	// is empty when that side has no value (a missing or extra entry).
	XType string `json:"x_type,omitempty"`
	YType string `json:"y_type,omitempty"`
	// X and Y are length-limited, redacted value summaries. A nil summary
	// indicates that the side has no value.
	X *MachineValueSummary `json:"x,omitempty"`
	Y *MachineValueSummary `json:"y,omitempty"`
	// Transformed reports whether a [Transformer] was applied somewhere on
	// the path to this difference.
	Transformed bool `json:"transformed,omitempty"`
	// Decision records the equality mechanism that decided this node.
	Decision DifferenceDecision `json:"decision"`
}

// MachineValueSummary is a length-limited textual summary of a value.
// The text is produced without calling the value's String or GoString
// methods and without printing pointer addresses. Struct fields whose names
// look sensitive (for example "Password") are redacted before rendering.
type MachineValueSummary struct {
	// Text is the rendered summary.
	Text string `json:"text"`
	// Truncated reports whether rendering stopped early because the byte
	// limit was reached or because of the depth or cycle limit.
	Truncated bool `json:"truncated,omitempty"`
}

// PathStepKind identifies the kind of a [MachinePathStep].
type PathStepKind string

const (
	StepRoot          PathStepKind = "root"
	StepStructField   PathStepKind = "struct_field"
	StepSliceIndex    PathStepKind = "slice_index"
	StepMapIndex      PathStepKind = "map_index"
	StepIndirect      PathStepKind = "indirect"
	StepTypeAssertion PathStepKind = "type_assertion"
	StepTransform     PathStepKind = "transform"
)

// MachinePathStep is one element of a [MachineDifference] path. Only the
// fields relevant to Kind are populated.
type MachinePathStep struct {
	// Kind is the kind of the path step.
	Kind PathStepKind `json:"kind"`
	// Type is the resulting Go type after the step.
	Type string `json:"type,omitempty"`

	// FieldName and FieldIndex describe a struct field.
	FieldName  string `json:"field,omitempty"`
	FieldIndex *int   `json:"field_index,omitempty"`

	// XIndex and YIndex describe a slice or array element position. An
	// index is nil when the element is absent on that side.
	XIndex *int `json:"x_index,omitempty"`
	YIndex *int `json:"y_index,omitempty"`

	// Key carries the stable textual identity of a map key. When a key
	// cannot have a stable textual identity (a pointer, channel, map,
	// slice, or function), Key is empty and KeyType with KeyOrdinal
	// identifies the entry: KeyOrdinal is the zero-based position of the
	// key within the stable sorted traversal of its map. Pointer addresses
	// are never emitted.
	Key        string `json:"key,omitempty"`
	KeyType    string `json:"key_type,omitempty"`
	KeyOrdinal *int   `json:"key_ordinal,omitempty"`

	// TransformName is the name of the applied [Transformer].
	TransformName string `json:"transform,omitempty"`
}

// machineReporter implements reporterIface, collecting unequal leaf nodes
// as MachineDifference values while the comparison engine runs.
type machineReporter struct {
	maxEntries      int
	maxSummaryBytes int

	path      []MachinePathStep
	steps     []PathStep
	mapDepths []int // path length at which each visited map's keys occur
	mapCounts []int // per visited map, count of MapIndex steps seen

	transformDepth int
	sensitiveDepth int

	numDiffs  int
	diffs     []MachineDifference
	truncated bool
}

func (r *machineReporter) PushStep(ps PathStep) {
	step := MachinePathStep{Kind: StepRoot, Type: typeName(ps.Type())}
	switch ps := ps.(type) {
	case StructField:
		step.Kind = StepStructField
		step.FieldName = ps.Name()
		idx := ps.Index()
		step.FieldIndex = &idx
		if isSensitiveFieldName(ps.Name()) {
			r.sensitiveDepth++
		}
	case SliceIndex:
		step.Kind = StepSliceIndex
		ix, iy := ps.SplitKeys()
		if ix >= 0 {
			step.XIndex = &ix
		}
		if iy >= 0 {
			step.YIndex = &iy
		}
	case MapIndex:
		step.Kind = StepMapIndex
		k := ps.Key()
		step.KeyType = typeName(k.Type())
		ord := r.mapIndexOrdinal()
		if key, ok := stableKeyText(k, r.maxSummaryBytes); ok {
			step.Key = key
		} else {
			step.KeyOrdinal = &ord
		}
	case Indirect:
		step.Kind = StepIndirect
	case TypeAssertion:
		step.Kind = StepTypeAssertion
	case Transform:
		step.Kind = StepTransform
		step.TransformName = ps.Name()
		r.transformDepth++
	}
	r.path = append(r.path, step)
	r.steps = append(r.steps, ps)
}

// mapIndexOrdinal returns the ordinal of the current map key within the map
// currently being visited. A new frame starts when a MapIndex step follows a
// step that is not itself a MapIndex of the same map; that is exact because
// the engine visits all keys of one map consecutively in sorted order.
func (r *machineReporter) mapIndexOrdinal() int {
	depth := len(r.path)
	if len(r.mapDepths) == 0 || r.mapDepths[len(r.mapDepths)-1] != depth {
		r.mapDepths = append(r.mapDepths, depth)
		r.mapCounts = append(r.mapCounts, 0)
	}
	i := len(r.mapCounts) - 1
	ord := r.mapCounts[i]
	r.mapCounts[i]++
	return ord
}

func (r *machineReporter) Report(rs Result) {
	if rs.Equal() { // Includes nodes ignored by an Ignore option.
		return
	}
	r.numDiffs++
	if len(r.diffs) >= r.maxEntries {
		r.truncated = true
		return
	}

	vx, vy := r.steps[len(r.steps)-1].Values()
	last := r.path[len(r.path)-1]
	transformed := r.transformDepth > 0
	if last.Kind == StepTransform {
		transformed = true
	}
	diff := MachineDifference{
		Path:        append([]MachinePathStep(nil), r.path...),
		Kind:        classifyKind(last.Kind, vx, vy),
		Transformed: transformed,
		Decision: DifferenceDecision{
			ByMethod:   rs.ByMethod(),
			ByComparer: rs.ByFunc(),
			ByCycle:    rs.ByCycle(),
		},
	}
	if vx.IsValid() {
		diff.XType = typeName(vx.Type())
		sum := r.summarizeLeaf(vx)
		diff.X = &sum
	}
	if vy.IsValid() {
		diff.YType = typeName(vy.Type())
		sum := r.summarizeLeaf(vy)
		diff.Y = &sum
	}
	r.diffs = append(r.diffs, diff)
}

// summarizeLeaf renders a leaf value, redacting it up front when the leaf
// is a sensitive struct field, before any value is inspected. This ensures
// sensitive values never reach the renderer and can never trigger a
// Stringer.
func (r *machineReporter) summarizeLeaf(v reflect.Value) MachineValueSummary {
	if r.sensitiveDepth > 0 {
		return MachineValueSummary{Text: redactedPlaceholder}
	}
	return summarizeValue(v, r.maxSummaryBytes)
}

func (r *machineReporter) PopStep() {
	last := r.path[len(r.path)-1]
	if last.Kind == StepTransform {
		r.transformDepth--
	}
	if last.Kind == StepStructField && isSensitiveFieldName(last.FieldName) {
		r.sensitiveDepth--
	}
	r.path = r.path[:len(r.path)-1]
	r.steps = r.steps[:len(r.steps)-1]
	// Pop frames for maps whose traversal has ended. A frame stored at
	// depth d belongs to the map node at path index d-1 and stays live
	// across consecutive key pops (path length d) but dies when the map
	// node itself pops (path length drops below d).
	for len(r.mapDepths) > 0 && r.mapDepths[len(r.mapDepths)-1] > len(r.path) {
		r.mapDepths = r.mapDepths[:len(r.mapDepths)-1]
		r.mapCounts = r.mapCounts[:len(r.mapCounts)-1]
	}
}

var _ reporterIface = (*machineReporter)(nil)

// classifyKind derives the difference kind from the leaf step and values.
func classifyKind(kind PathStepKind, vx, vy reflect.Value) DifferenceKind {
	switch kind {
	case StepSliceIndex, StepMapIndex:
		switch {
		case !vx.IsValid():
			return KindExtra
		case !vy.IsValid():
			return KindMissing
		}
	}
	xNil, yNil := isNilValue(vx), isNilValue(vy)
	switch {
	case xNil != yNil:
		return KindNil
	default:
		return KindMismatch
	}
}

// isNilValue reports whether v is invalid or the nil value of a nillable
// kind. Invalid values represent absent slice/map sides, which callers
// classify separately.
func isNilValue(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map,
		reflect.Chan, reflect.Func:
		return v.IsNil()
	default:
		return false
	}
}
