package cmp

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// Policy is a declarative comparison policy bound to a single root type.
//
// A policy is an ordered list of rules addressed by field paths relative to
// the root type. Each rule either ignores a location or compares the
// floating-point values found at a location within an absolute tolerance.
// [Policy.Compile] validates every rule against the root type and produces
// an ordinary [Option] that may be composed with any other options.
//
// The path syntax is a dot-separated sequence of segments, where each
// segment is a struct field name with an optional slice index:
//
//	Items[0].Price   struct field "Items", element 0, then field "Price"
//	Items[*].Price   the "*" index wildcard matches every slice element
//	*.Price          the "*" field wildcard matches every struct field
//
// Pointer indirections along a path are implicit and never appear in the
// path string, so the same rule applies through any number of nested
// pointers, including within recursive data structures.
//
// When several rules match the same location, the most specific rule (the
// one with the fewest wildcards) wins. Rules of equal specificity that may
// apply to the same location and yet disagree on the action are reported
// as a [ConflictError] by [Policy.Compile].
type Policy struct {
	root  reflect.Type
	rules []*policyRule
	errs  []error
}

// NewPolicy returns an empty [Policy] bound to the root type root.
// The compiled [Option] only takes effect when the values passed to
// [Equal] or [Diff] are of exactly this type.
func NewPolicy(root reflect.Type) *Policy {
	return &Policy{root: root}
}

// Ignore appends a rule that ignores the location selected by path.
// It returns the same [Policy] to allow chaining.
func (p *Policy) Ignore(path string) *Policy {
	return p.add(path, ruleIgnore, 0)
}

// Tolerance appends a rule that compares the floating-point values at the
// location selected by path as equal when their absolute difference does
// not exceed absTol. The tolerance must be non-negative and not NaN.
// It returns the same [Policy] to allow chaining.
func (p *Policy) Tolerance(path string, absTol float64) *Policy {
	return p.add(path, ruleTolerance, absTol)
}

// Compile validates all rules against the root type and returns the
// equivalent [Option]. It reports a [PathError] for every rule whose path
// traverses a nonexistent field or an inapplicable type, and a
// [ConflictError] for every pair of equally specific rules that may apply
// to the same location with conflicting actions.
func (p *Policy) Compile() (Option, error) {
	var errs []error
	errs = append(errs, p.errs...)
	if p.root == nil {
		errs = append(errs, errors.New("cmp: policy has a nil root type"))
	}
	if len(errs) == 0 {
		for _, r := range p.rules {
			if err := p.checkTypes(r); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) == 0 {
		for i := 0; i < len(p.rules); i++ {
			for j := i + 1; j < len(p.rules); j++ {
				a, b := p.rules[i], p.rules[j]
				if a.specificity == b.specificity && a.compatible(b) && a.conflicts(b) {
					errs = append(errs, &ConflictError{RuleA: a.describe(), RuleB: b.describe()})
				}
			}
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	root := p.root
	rules := p.rules // snapshot; later additions do not affect this Option
	winner := func(pa Path) *policyRule {
		if len(pa) == 0 || pa[0].Type() != root {
			return nil // policy is bound to its root type
		}
		var best *policyRule
		for _, r := range rules {
			if !r.matches(pa) {
				continue
			}
			if best == nil || r.specificity < best.specificity ||
				(r.specificity == best.specificity && r.order < best.order) {
				best = r
			}
		}
		return best
	}

	var opts Options
	for _, r := range rules {
		r := r
		pred := func(pa Path) bool { return winner(pa) == r }
		switch r.kind {
		case ruleIgnore:
			opts = append(opts, FilterPath(pred, Ignore()))
		case ruleTolerance:
			seen := make(map[reflect.Type]bool)
			for _, t := range r.leafTypes {
				t = derefType(t)
				if seen[t] {
					continue
				}
				seen[t] = true
				opts = append(opts, FilterPath(pred, Comparer(makeFloatComparer(t, r.tolerance))))
			}
		}
	}
	return opts, nil
}

type policyRuleKind int

const (
	ruleIgnore policyRuleKind = iota
	ruleTolerance
)

type policyRule struct {
	path        string
	kind        policyRuleKind
	tolerance   float64
	segments    []policySegment
	specificity int // number of wildcards; lower is more specific
	leafTypes   []reflect.Type
	order       int
}

type policySegment struct {
	name     string // field name; empty when wild is set
	wild     bool   // matches any struct field
	hasIndex bool
	index    int
	wildIdx  bool // matches any slice index
}

func (p *Policy) add(path string, kind policyRuleKind, tolerance float64) *Policy {
	r := &policyRule{path: path, kind: kind, tolerance: tolerance, order: len(p.rules)}
	segments, err := parsePolicyPath(path)
	if err != nil {
		p.errs = append(p.errs, err)
		return p
	}
	r.segments = segments
	for _, s := range segments {
		if s.wild {
			r.specificity++
		}
		if s.hasIndex && s.wildIdx {
			r.specificity++
		}
	}
	if kind == ruleTolerance && (math.IsNaN(tolerance) || tolerance < 0) {
		p.errs = append(p.errs, fmt.Errorf("cmp: policy rule %q: invalid tolerance %v: must be non-negative and not NaN", path, tolerance))
	}
	p.rules = append(p.rules, r)
	return p
}

func (r *policyRule) describe() string {
	switch r.kind {
	case ruleIgnore:
		return fmt.Sprintf("%q (ignore)", r.path)
	default:
		return fmt.Sprintf("%q (tolerance %v)", r.path, r.tolerance)
	}
}

// compatible reports whether r and o could match the same concrete path.
func (r *policyRule) compatible(o *policyRule) bool {
	if len(r.segments) != len(o.segments) {
		return false
	}
	for i := range r.segments {
		x, y := r.segments[i], o.segments[i]
		if !x.wild && !y.wild && x.name != y.name {
			return false
		}
		if x.hasIndex != y.hasIndex {
			return false
		}
		if x.hasIndex && !x.wildIdx && !y.wildIdx && x.index != y.index {
			return false
		}
	}
	return true
}

// conflicts reports whether r and o disagree on the action to take.
func (r *policyRule) conflicts(o *policyRule) bool {
	if r.kind != o.kind {
		return true
	}
	return r.kind == ruleTolerance && r.tolerance != o.tolerance
}

// matches reports whether the concrete path pa is selected by the rule.
// Indirect steps are transparent: they never appear in policy paths.
func (r *policyRule) matches(pa Path) bool {
	i := 1 // skip the root step
	skipIndirect := func() {
		for i < len(pa) {
			if _, ok := pa[i].(Indirect); ok {
				i++
			} else {
				break
			}
		}
	}
	for _, seg := range r.segments {
		skipIndirect()
		if i >= len(pa) {
			return false
		}
		sf, ok := pa[i].(StructField)
		if !ok || (!seg.wild && sf.Name() != seg.name) {
			return false
		}
		i++
		if seg.hasIndex {
			skipIndirect()
			if i >= len(pa) {
				return false
			}
			si, ok := pa[i].(SliceIndex)
			if !ok || (!seg.wildIdx && si.Key() != seg.index) {
				return false
			}
			i++
		}
	}
	for ; i < len(pa); i++ {
		if _, ok := pa[i].(Indirect); !ok {
			return false
		}
	}
	return true
}

// checkTypes resolves the rule path against the root type, recording the
// leaf types the rule may apply to. It returns a [PathError] identifying
// the exact segment at which the path becomes invalid.
func (p *Policy) checkTypes(r *policyRule) error {
	types := []reflect.Type{p.root}
	for i, seg := range r.segments {
		var next []reflect.Type
		for _, t := range types {
			t = derefType(t)
			if t.Kind() != reflect.Struct {
				return &PathError{Path: r.path, Segment: i, Err: fmt.Errorf("type %s is not a struct", t)}
			}
			if seg.wild {
				for j := 0; j < t.NumField(); j++ {
					next = append(next, t.Field(j).Type)
				}
			} else {
				f, ok := t.FieldByName(seg.name)
				if !ok {
					return &PathError{Path: r.path, Segment: i, Err: fmt.Errorf("type %s has no field %q", t, seg.name)}
				}
				next = append(next, f.Type)
			}
		}
		if len(next) == 0 {
			return &PathError{Path: r.path, Segment: i, Err: errors.New("wildcard matches no fields")}
		}
		if seg.hasIndex {
			var elems []reflect.Type
			for _, t := range next {
				t = derefType(t)
				if t.Kind() != reflect.Slice && t.Kind() != reflect.Array {
					return &PathError{Path: r.path, Segment: i, Err: fmt.Errorf("cannot index type %s", t)}
				}
				elems = append(elems, t.Elem())
			}
			next = elems
		}
		types = next
	}
	r.leafTypes = types
	if r.kind == ruleTolerance {
		for _, t := range types {
			if k := derefType(t).Kind(); k != reflect.Float32 && k != reflect.Float64 {
				return &PathError{Path: r.path, Segment: len(r.segments) - 1, Err: fmt.Errorf("tolerance requires a floating-point type, got %s", t)}
			}
		}
	}
	return nil
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

var policyBoolType = reflect.TypeOf(true)

// makeFloatComparer builds a func(T, T) bool for a named or unnamed
// floating-point type T that reports equality within an absolute tolerance.
func makeFloatComparer(t reflect.Type, tolerance float64) any {
	ft := reflect.FuncOf([]reflect.Type{t, t}, []reflect.Type{policyBoolType}, false)
	f := reflect.MakeFunc(ft, func(args []reflect.Value) []reflect.Value {
		x, y := args[0].Float(), args[1].Float()
		eq := x == y || math.Abs(x-y) <= tolerance
		return []reflect.Value{reflect.ValueOf(eq)}
	})
	return f.Interface()
}

var policyIdentRx = regexp.MustCompile(`^` + identRx + `$`)

func parsePolicyPath(path string) ([]policySegment, error) {
	if path == "" {
		return nil, errors.New("cmp: policy path is empty")
	}
	parts := strings.Split(path, ".")
	segments := make([]policySegment, 0, len(parts))
	for _, part := range parts {
		seg, err := parsePolicySegment(part)
		if err != nil {
			return nil, fmt.Errorf("cmp: invalid policy path %q: %v", path, err)
		}
		segments = append(segments, seg)
	}
	return segments, nil
}

func parsePolicySegment(s string) (policySegment, error) {
	var seg policySegment
	name := s
	if i := strings.IndexByte(s, '['); i >= 0 {
		name = s[:i]
		rest := s[i:]
		if !strings.HasSuffix(rest, "]") {
			return seg, fmt.Errorf("malformed segment %q", s)
		}
		seg.hasIndex = true
		switch inner := rest[1 : len(rest)-1]; {
		case inner == "*":
			seg.wildIdx = true
		default:
			n, err := strconv.Atoi(inner)
			if err != nil || n < 0 {
				return seg, fmt.Errorf("invalid index in segment %q", s)
			}
			seg.index = n
		}
	}
	switch {
	case name == "*":
		seg.wild = true
	case name == "":
		return seg, fmt.Errorf("empty field name in segment %q", s)
	case !policyIdentRx.MatchString(name):
		return seg, fmt.Errorf("invalid field name %q", name)
	default:
		seg.name = name
	}
	return seg, nil
}

// PathError describes a policy rule whose path cannot be resolved against
// the root type of the [Policy].
type PathError struct {
	Path    string // the offending rule path
	Segment int    // index of the path segment that failed to resolve
	Err     error  // the underlying reason
}

func (e *PathError) Error() string {
	return fmt.Sprintf("cmp: policy rule %q: segment %d: %v", e.Path, e.Segment, e.Err)
}

func (e *PathError) Unwrap() error { return e.Err }

// ConflictError describes a pair of policy rules with equal specificity
// that may apply to the same location but disagree on the action to take.
type ConflictError struct {
	RuleA string // description of the first rule
	RuleB string // description of the second rule
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("cmp: conflicting policy rules %s and %s: equal specificity, neither takes precedence", e.RuleA, e.RuleB)
}
