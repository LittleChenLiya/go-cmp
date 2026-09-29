// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// Policy is a declarative comparison policy bound to a root type T.
// Rules are declared in terms of field paths relative to the root type
// and are compiled into a regular [Option] with [Policy.Compile].
//
// A path is a dot-separated sequence of segments, where each segment is
// a struct field name optionally followed by one or more slice or array
// index operations. An index operation is either a concrete index ([0])
// or a wildcard ([*]) that matches any element. Pointers along the path
// are traversed transparently. For example:
//
//	Items[*].Price
//	Items[0].Price
//	Meta.Tags[*]
//
// When multiple rules match the same node, the most specific rule wins:
// a concrete index is more specific than a wildcard. Rules that overlap
// with equal specificity and disagree on the action are rejected at
// compile time with a diagnostic naming both rules.
type Policy[T any] struct {
	root  reflect.Type
	rules []*policyRule
	err   error
}

// NewPolicy returns an empty [Policy] bound to the root type T.
func NewPolicy[T any]() *Policy[T] {
	return &Policy[T]{root: reflect.TypeOf((*T)(nil)).Elem()}
}

// Ignore declares that the node addressed by path (and its subtree)
// is excluded from comparison.
func (p *Policy[T]) Ignore(path string) *Policy[T] {
	return p.add(path, policyIgnore, 0)
}

// Tolerance declares that the float node addressed by path compares
// equal when the absolute difference is at most tol.
// The tolerance must be a non-negative number.
func (p *Policy[T]) Tolerance(path string, tol float64) *Policy[T] {
	return p.add(path, policyTolerance, tol)
}

// Compile validates the declared rules and compiles them into a regular
// [Option] usable with [Equal] and [Diff]. It reports an error if any
// path is invalid for the root type, if a tolerance is negative or NaN,
// or if two rules conflict with equal specificity.
func (p *Policy[T]) Compile() (Option, error) {
	if p.err != nil {
		return nil, p.err
	}
	if err := checkPolicyConflicts(p.rules); err != nil {
		return nil, err
	}
	if len(p.rules) == 0 {
		return Options{}, nil
	}
	cp := compiledPolicy{rules: p.rules}
	opts := make(Options, 0, len(p.rules))
	for i, r := range p.rules {
		i := i
		f := FilterPath(func(pa Path) bool { return cp.winner(pa) == i }, r.opt)
		opts = append(opts, f)
	}
	return opts, nil
}

type policyAction int

const (
	policyIgnore policyAction = iota
	policyTolerance
)

// policyStep is a single step in a parsed policy path:
// either a struct field access or a slice/array index operation.
type policyStep struct {
	field    string // Struct field name; empty for an index step
	wildcard bool   // Index step matching any element
	index    int    // Concrete index (valid only for index steps)
}

func (s policyStep) isIndex() bool { return s.field == "" }

type policyRule struct {
	num    int // 1-based rule number for diagnostics
	raw    string
	action policyAction
	tol    float64
	steps  []policyStep
	opt    Option // Ignore or a typed comparer
}

func (r *policyRule) String() string {
	if r.action == policyIgnore {
		return fmt.Sprintf("Ignore(%q)", r.raw)
	}
	return fmt.Sprintf("Tolerance(%q, %v)", r.raw, r.tol)
}

func (p *Policy[T]) add(path string, action policyAction, tol float64) *Policy[T] {
	if p.err != nil {
		return p
	}
	r := &policyRule{num: len(p.rules) + 1, raw: path, action: action, tol: tol}
	if p.err = r.parse(); p.err != nil {
		return p
	}
	if p.err = r.validate(p.root); p.err != nil {
		return p
	}
	p.rules = append(p.rules, r)
	return p
}

var policySegRx = regexp.MustCompile(`^(` + identRx + `)?((\[\*])|(\[[0-9]+\]))*$`)

// parse parses the raw path into a sequence of policy steps.
func (r *policyRule) parse() error {
	if r.raw == "" {
		return fmt.Errorf("cmp: invalid policy path: path is empty")
	}
	for segNum, seg := range strings.Split(r.raw, ".") {
		segNum++ // 1-based
		if !policySegRx.MatchString(seg) {
			return fmt.Errorf("cmp: invalid policy path %q: segment %d %q: malformed syntax", r.raw, segNum, seg)
		}
		rest := seg
		if i := strings.IndexByte(rest, '['); i >= 0 {
			if i > 0 {
				r.steps = append(r.steps, policyStep{field: rest[:i]})
			}
			rest = rest[i:]
		} else {
			r.steps = append(r.steps, policyStep{field: rest})
			continue
		}
		for len(rest) > 0 {
			j := strings.IndexByte(rest, ']')
			op := rest[1:j]
			if op == "*" {
				r.steps = append(r.steps, policyStep{wildcard: true})
			} else {
				n, _ := strconv.Atoi(op) // Validated by policySegRx
				r.steps = append(r.steps, policyStep{index: n})
			}
			rest = rest[j+1:]
		}
	}
	if len(r.steps) == 0 {
		return fmt.Errorf("cmp: invalid policy path %q: path is empty", r.raw)
	}
	return nil
}

// validate checks that the path is applicable to the root type and,
// for tolerance rules, constructs the typed comparer.
func (r *policyRule) validate(root reflect.Type) error {
	// segs maps each step to its 1-based segment number and text.
	type segInfo struct {
		num  int
		text string
	}
	var segs []segInfo
	for segNum, seg := range strings.Split(r.raw, ".") {
		n := 1 + strings.Count(seg, "[")
		if strings.HasPrefix(seg, "[") {
			n-- // Segment starts with an index operation
		}
		for ; n > 0; n-- {
			segs = append(segs, segInfo{num: segNum + 1, text: seg})
		}
	}

	t := root
	for i, step := range r.steps {
		seg := segs[i]
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if !step.isIndex() {
			if t.Kind() != reflect.Struct {
				return fmt.Errorf("cmp: invalid policy path %q: segment %d %q: cannot access field %q of non-struct type %v", r.raw, seg.num, seg.text, step.field, t)
			}
			f, ok := t.FieldByName(step.field)
			if !ok {
				return fmt.Errorf("cmp: invalid policy path %q: segment %d %q: struct type %v has no field %q", r.raw, seg.num, seg.text, t, step.field)
			}
			if f.PkgPath != "" {
				return fmt.Errorf("cmp: invalid policy path %q: segment %d %q: field %q of struct type %v is unexported", r.raw, seg.num, seg.text, step.field, t)
			}
			t = f.Type
		} else {
			if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
				return fmt.Errorf("cmp: invalid policy path %q: segment %d %q: cannot index non-slice type %v", r.raw, seg.num, seg.text, t)
			}
			if !step.wildcard && t.Kind() == reflect.Array && step.index >= t.Len() {
				return fmt.Errorf("cmp: invalid policy path %q: segment %d %q: index %d out of bounds for array type %v", r.raw, seg.num, seg.text, step.index, t)
			}
			t = t.Elem()
		}
	}

	switch r.action {
	case policyIgnore:
		r.opt = Ignore()
	case policyTolerance:
		last := segs[len(segs)-1]
		if math.IsNaN(r.tol) || r.tol < 0 {
			return fmt.Errorf("cmp: invalid policy path %q: segment %d %q: tolerance %v must be a non-negative number", r.raw, last.num, last.text, r.tol)
		}
		if k := t.Kind(); k != reflect.Float32 && k != reflect.Float64 {
			return fmt.Errorf("cmp: invalid policy path %q: segment %d %q: tolerance requires a float type, got %v", r.raw, last.num, last.text, t)
		}
		r.opt = makeToleranceComparer(t, r.tol)
	}
	return nil
}

// makeToleranceComparer returns a Comparer option for the exact float type t
// (which may be a named float type) applying an absolute tolerance.
func makeToleranceComparer(t reflect.Type, tol float64) Option {
	boolType := reflect.TypeOf(true)
	ft := reflect.FuncOf([]reflect.Type{t, t}, []reflect.Type{boolType}, false)
	fnc := reflect.MakeFunc(ft, func(args []reflect.Value) []reflect.Value {
		eq := math.Abs(args[0].Float()-args[1].Float()) <= tol
		return []reflect.Value{reflect.ValueOf(eq)}
	})
	return Comparer(fnc.Interface())
}

// overlaps reports whether two rules can match the same concrete path.
func (r1 *policyRule) overlaps(r2 *policyRule) bool {
	if len(r1.steps) != len(r2.steps) {
		return false
	}
	for i := range r1.steps {
		s1, s2 := r1.steps[i], r2.steps[i]
		if !s1.isIndex() || !s2.isIndex() {
			if s1.field != s2.field {
				return false
			}
			continue
		}
		if !s1.wildcard && !s2.wildcard && s1.index != s2.index {
			return false
		}
	}
	return true
}

// dominates reports whether r1 is strictly more specific than r2,
// assuming the two rules overlap.
func (r1 *policyRule) dominates(r2 *policyRule) bool {
	more := false
	for i := range r1.steps {
		s1, s2 := r1.steps[i], r2.steps[i]
		if s1.isIndex() && s2.isIndex() && s1.wildcard != s2.wildcard {
			if s1.wildcard {
				return false // r1 is less specific at this step
			}
			more = true
		}
	}
	return more
}

// sameAction reports whether two rules have equivalent behavior.
func (r1 *policyRule) sameAction(r2 *policyRule) bool {
	return r1.action == r2.action && (r1.action == policyIgnore || r1.tol == r2.tol)
}

// checkPolicyConflicts rejects pairs of overlapping rules that disagree
// on the action without one being strictly more specific than the other.
func checkPolicyConflicts(rules []*policyRule) error {
	for i, r1 := range rules {
		for _, r2 := range rules[i+1:] {
			if !r1.overlaps(r2) || r1.sameAction(r2) {
				continue
			}
			if r1.dominates(r2) || r2.dominates(r1) {
				continue // The more specific rule takes precedence
			}
			return fmt.Errorf("cmp: conflicting policy rules: rule %d (%s) and rule %d (%s) overlap with equal specificity", r1.num, r1, r2.num, r2)
		}
	}
	return nil
}

// compiledPolicy matches runtime paths against the compiled rules.
type compiledPolicy struct {
	rules []*policyRule
}

// winner returns the index of the most specific rule matching pa,
// or -1 if no rule matches.
func (cp compiledPolicy) winner(pa Path) int {
	best := -1
	for i, r := range cp.rules {
		if !r.matches(pa) {
			continue
		}
		if best < 0 || r.dominates(cp.rules[best]) {
			best = i
		}
	}
	return best
}

// matches reports whether the rule applies exactly at the node
// addressed by pa. Pointer indirections and type assertions along
// the path are traversed transparently.
func (r *policyRule) matches(pa Path) bool {
	si := 0
	for i, ps := range pa {
		if i == 0 {
			continue // Operation-less root step
		}
		switch ps := ps.(type) {
		case StructField:
			if si >= len(r.steps) || r.steps[si].isIndex() || r.steps[si].field != ps.Name() {
				return false
			}
			si++
		case SliceIndex:
			if si >= len(r.steps) || !r.steps[si].isIndex() {
				return false
			}
			if s := r.steps[si]; !s.wildcard && s.index != ps.Key() {
				return false
			}
			si++
		case Indirect, TypeAssertion:
			continue
		default: // MapIndex, Transform, etc.
			return false
		}
	}
	return si == len(r.steps)
}
