// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/go-cmp/cmp/internal/value"
)

// DefaultSummaryByteLimit is the default byte limit for each value summary
// produced by [Compare] when [CompareConfig.MaxSummaryBytes] is non-positive.
const DefaultSummaryByteLimit = 128

// DifferenceKind identifies why a leaf node is unequal.
type DifferenceKind string

const (
	// KindModified means both values exist, but are unequal.
	KindModified DifferenceKind = "modified"
	// KindRemoved means the value exists in x, but not in y,
	// for example a removed slice element or map entry.
	KindRemoved DifferenceKind = "removed"
	// KindAdded means the value exists in y, but not in x,
	// for example an added slice element or map entry.
	KindAdded DifferenceKind = "added"
)

// CompareConfig configures the machine-readable report produced by [Compare].
// Equality semantics are determined solely by the [Option] values passed to
// [Compare]: [Ignore], [Comparer], Equal methods, and [Transformer] apply
// exactly as in [Equal] and [Diff]. Unexported fields are never inspected
// without an [Exporter].
type CompareConfig struct {
	// MaxEntries limits the number of collected differences. A non-positive
	// value means no limit. Once the limit is reached the comparison stops
	// early; the returned report sets EntriesTruncated and reports
	// Completed=false so that a partial collection is never mistaken for an
	// exhaustive result or for equality.
	MaxEntries int

	// MaxSummaryBytes limits the length of each value summary in bytes.
	// A non-positive value means [DefaultSummaryByteLimit]. A cut summary
	// ends in "..." and sets ValueSummary.Truncated.
	MaxSummaryBytes int

	// Redact optionally reports whether the value reached by p must be hidden
	// from summaries. It is evaluated before summary formatting; a redacted
	// value is replaced without ever invoking its fmt.Stringer, fmt.GoStringer,
	// or error method. Independently, the nearest [StructField] on the path
	// is matched against a built-in set of common sensitive field names such
	// as "password", "secret", and "token". The path must not be mutated.
	Redact func(p Path) bool
}

// Compare compares x and y with the same engine and [Option] semantics as
// [Equal] and returns a machine-readable [CompareReport]. Unlike [Diff], it
// does not produce pseudo-Go text; each difference records a typed path, the
// difference kind, the types of both values, whether a [Transformer] was used
// to reach the value, and byte-limited value summaries.
//
// Summaries never contain pointer addresses and never invoke String, GoString,
// or error methods, so a sensitive type cannot leak sentinel text through a
// Stringer. Common sensitive field names are redacted before formatting, and
// cfg.Redact can hide additional values. Cyclic values are summarized safely
// without infinite recursion.
//
// The report and its referenced types only use JSON-safe fields and can be
// marshaled directly with encoding/json. Compare panics on the same
// configuration and unexported-field errors as [Equal].
func Compare(x, y any, cfg CompareConfig, opts ...Option) CompareReport {
	r := newStructuredReporter(cfg)
	s := newState(append(opts, Reporter(r)))
	func() {
		defer r.finish()
		s.compareAny(rootStep(x, y))
	}()
	return r.build()
}

// CompareReport is the machine-readable result of [Compare].
type CompareReport struct {
	// Equal reports whether x and y are equal under the given options.
	// A report with truncated entries is never equal.
	Equal bool `json:"equal"`
	// Completed reports whether the comparison traversed the whole value
	// tree. It is false when MaxEntries stopped the comparison early.
	Completed bool `json:"completed"`
	// EntriesTruncated reports that further differences were not collected
	// because MaxEntries was reached.
	EntriesTruncated bool `json:"entries_truncated"`
	// MaxEntries is the configured entry limit; zero means unlimited.
	MaxEntries int `json:"max_entries,omitempty"`
	// MaxSummaryBytes is the effective per-summary byte limit.
	MaxSummaryBytes int `json:"max_summary_bytes"`
	// NumDifferences is the number of entries collected in Differences.
	NumDifferences int `json:"num_differences"`
	// NumRedacted is the number of summarized values replaced by redaction.
	NumRedacted int `json:"num_redacted"`
	// Differences lists the collected leaf differences in deterministic
	// order. Map entries appear in sorted-key order; the order does not
	// depend on Go's randomized map iteration.
	Differences []Difference `json:"differences"`
}

// Difference is a single unequal leaf in a [CompareReport].
type Difference struct {
	// Path is the typed path from the root to the unequal leaf. It preserves
	// [StructField], [MapIndex], [SliceIndex], and [TypeAssertion] steps, as
	// well as [Indirect], [Transform], and the root step.
	Path []PathElement `json:"path"`
	// Kind reports whether the value was modified, removed, or added.
	Kind DifferenceKind `json:"kind"`
	// XType is the type of the x value, or "" when x has no value here.
	XType string `json:"x_type,omitempty"`
	// YType is the type of the y value, or "" when y has no value here.
	YType string `json:"y_type,omitempty"`
	// X is the redacted, byte-limited summary of the x value, or nil when
	// the value is absent (see Kind).
	X *ValueSummary `json:"x,omitempty"`
	// Y is the redacted, byte-limited summary of the y value, or nil when
	// the value is absent (see Kind).
	Y *ValueSummary `json:"y,omitempty"`
	// Transformed reports whether at least one [Transform] step is on Path.
	Transformed bool `json:"transformed"`
	// ByComparer reports whether a [Comparer] decided this leaf.
	ByComparer bool `json:"by_comparer,omitempty"`
	// ByEqualMethod reports whether an Equal method decided this leaf.
	ByEqualMethod bool `json:"by_equal_method,omitempty"`
	// ByCycle reports whether a reference cycle decided this leaf.
	ByCycle bool `json:"by_cycle,omitempty"`
}

// PathElement is the machine-readable form of one [PathStep]. The Kind field
// identifies which of the semantic fields is populated.
type PathElement struct {
	// Type is the result type of the step, using reflect.Type syntax.
	Type string `json:"type"`
	// Kind is one of "root", "struct_field", "map_index", "slice_index",
	// "type_assertion", "indirect", or "transform".
	Kind string `json:"kind"`

	// Field is the field name of a "struct_field" step.
	Field string `json:"field,omitempty"`
	// FieldIndex is the field index within the parent struct.
	FieldIndex *int `json:"field_index,omitempty"`

	// MapKey is the typed key of a "map_index" step. Keys without a stable
	// textual identity are represented by type and contents, never an address.
	MapKey *ValueSummary `json:"map_key,omitempty"`

	// SliceX is the x index of a "slice_index" step, or nil when x has no
	// element at this step.
	SliceX *int `json:"slice_x,omitempty"`
	// SliceY is the y index of a "slice_index" step, or nil when y has no
	// element at this step.
	SliceY *int `json:"slice_y,omitempty"`

	// TypeAssertion marks a "type_assertion" step.
	TypeAssertion bool `json:"type_assertion,omitempty"`
	// Indirect marks an "indirect" step.
	Indirect bool `json:"indirect,omitempty"`
	// Transform names the [Transformer] of a "transform" step.
	Transform string `json:"transform,omitempty"`
	// Root marks the operation-less first step.
	Root bool `json:"root,omitempty"`
}

// ValueSummary is a safe, length-limited rendering of a value. It never
// contains pointer addresses and never calls String, GoString, or error
// methods on the summarized value.
type ValueSummary struct {
	// Type is the reflect.Type name of the summarized value.
	Type string `json:"type"`
	// Text is a Go-like rendering bounded by the configured byte limit.
	Text string `json:"text"`
	// Truncated reports whether Text was cut at the byte limit.
	Truncated bool `json:"truncated,omitempty"`
	// Redacted reports that the value was replaced before formatting.
	Redacted bool `json:"redacted,omitempty"`
}

// errDiffLimit is a sentinel panic that stops traversal once the configured
// number of differences has been collected. All path state is stack managed,
// so unwinding on this panic leaves no observable state behind.
type errDiffLimit struct{}

func (errDiffLimit) Error() string { return "cmp: difference entry limit reached" }

// structuredReporter implements reporterIface, streaming unequal leaves into
// machine-readable Difference values.
type structuredReporter struct {
	cfg      CompareConfig
	sumLimit int

	path     Path
	elements []PathElement
	diffs    []Difference

	aborted  bool
	redacted int
}

func newStructuredReporter(cfg CompareConfig) *structuredReporter {
	limit := cfg.MaxSummaryBytes
	if limit <= 0 {
		limit = DefaultSummaryByteLimit
	}
	return &structuredReporter{cfg: cfg, sumLimit: limit}
}

// finish maps the early-stop sentinel to an incomplete report and lets every
// other panic (ambiguous options, unexported fields, user function panics)
// propagate as it does for Equal and Diff.
func (r *structuredReporter) finish() {
	if recovered := recover(); recovered != nil {
		if _, ok := recovered.(errDiffLimit); ok {
			r.aborted = true
			return
		}
		panic(recovered)
	}
}

func (r *structuredReporter) PushStep(ps PathStep) {
	r.path.push(ps)
	r.elements = append(r.elements, encodePathStep(ps, r.sumLimit))
}

func (r *structuredReporter) PopStep() {
	r.path.pop()
	r.elements = r.elements[:len(r.elements)-1]
}

func (r *structuredReporter) Report(rs Result) {
	if rs.Equal() {
		return
	}
	if r.cfg.MaxEntries > 0 && len(r.diffs) >= r.cfg.MaxEntries {
		panic(errDiffLimit{})
	}

	step := r.path.Last()
	vx, vy := step.Values()
	diff := Difference{
		Path:          append([]PathElement(nil), r.elements...),
		Kind:          differenceKind(vx, vy),
		Transformed:   pathHasTransform(r.path),
		ByComparer:    rs.ByFunc(),
		ByEqualMethod: rs.ByMethod(),
		ByCycle:       rs.ByCycle(),
	}
	if vx.IsValid() {
		summary := r.summarize(vx)
		diff.X = &summary
		diff.XType = summary.Type
	}
	if vy.IsValid() {
		summary := r.summarize(vy)
		diff.Y = &summary
		diff.YType = summary.Type
	}
	r.diffs = append(r.diffs, diff)

	if r.cfg.MaxEntries > 0 && len(r.diffs) >= r.cfg.MaxEntries {
		panic(errDiffLimit{})
	}
}

func (r *structuredReporter) summarize(v reflect.Value) ValueSummary {
	return newValueFormatter(r.sumLimit, r.shouldRedact()).format(v)
}

// shouldRedact evaluates the caller predicate first, then a built-in
// sensitive-field-name heuristic. The decision happens before formatting, so
// a redacted value never reaches a Stringer/GoStringer call.
func (r *structuredReporter) shouldRedact() bool {
	if r.cfg.Redact != nil && r.cfg.Redact(r.path) {
		r.redacted++
		return true
	}
	for i := len(r.path) - 1; i >= 0; i-- {
		if sf, ok := r.path[i].(StructField); ok {
			if isSensitiveField(sf.Name()) {
				r.redacted++
				return true
			}
			break
		}
	}
	return false
}

func (r *structuredReporter) build() CompareReport {
	diffs := r.diffs
	// Canonical sorting makes output order independent of randomized map
	// iteration; slice and struct traversal are already deterministic.
	sort.SliceStable(diffs, func(i, j int) bool {
		return canonicalPath(diffs[i].Path) < canonicalPath(diffs[j].Path)
	})
	maxEntries := r.cfg.MaxEntries
	if maxEntries < 0 {
		maxEntries = 0
	}
	return CompareReport{
		Equal:            len(diffs) == 0,
		Completed:        !r.aborted,
		EntriesTruncated: r.aborted,
		MaxEntries:       maxEntries,
		MaxSummaryBytes:  r.sumLimit,
		NumDifferences:   len(diffs),
		NumRedacted:      r.redacted,
		Differences:      diffs,
	}
}

func differenceKind(vx, vy reflect.Value) DifferenceKind {
	switch {
	case !vx.IsValid():
		return KindAdded
	case !vy.IsValid():
		return KindRemoved
	default:
		return KindModified
	}
}

func pathHasTransform(p Path) bool {
	for _, step := range p {
		if _, ok := step.(Transform); ok {
			return true
		}
	}
	return false
}

func typeString(t reflect.Type) string {
	if t == nil {
		return ""
	}
	return value.TypeString(t, false)
}

func stepKind(ps PathStep) string {
	switch ps.(type) {
	case StructField:
		return "struct_field"
	case SliceIndex:
		return "slice_index"
	case MapIndex:
		return "map_index"
	case TypeAssertion:
		return "type_assertion"
	case Indirect:
		return "indirect"
	case Transform:
		return "transform"
	default:
		return "root"
	}
}

func encodePathStep(ps PathStep, limit int) PathElement {
	el := PathElement{Type: typeString(ps.Type()), Kind: stepKind(ps)}
	switch s := ps.(type) {
	case StructField:
		el.Field = s.Name()
		idx := s.Index()
		el.FieldIndex = &idx
	case SliceIndex:
		ix, iy := s.SplitKeys()
		if ix >= 0 {
			el.SliceX = &ix
		}
		if iy >= 0 {
			el.SliceY = &iy
		}
	case MapIndex:
		key := newValueFormatter(limit, false).format(s.Key())
		el.MapKey = &key
	case TypeAssertion:
		el.TypeAssertion = true
	case Indirect:
		el.Indirect = true
	case Transform:
		el.Transform = s.Name()
	default:
		el.Root = true
	}
	return el
}

// canonicalPath renders a typed path for stable ordering. Map keys use their
// safe summary text and type rather than an address.
func canonicalPath(p []PathElement) string {
	var b strings.Builder
	for _, el := range p {
		b.WriteString(el.Kind)
		b.WriteByte(':')
		switch el.Kind {
		case "struct_field":
			b.WriteString(el.Field)
		case "map_index":
			if el.MapKey != nil {
				b.WriteString(el.MapKey.Type)
				b.WriteByte('=')
				b.WriteString(el.MapKey.Text)
			}
		case "slice_index":
			if el.SliceX != nil {
				b.WriteString(strconv.Itoa(*el.SliceX))
			} else {
				b.WriteByte('?')
			}
			b.WriteByte(',')
			if el.SliceY != nil {
				b.WriteString(strconv.Itoa(*el.SliceY))
			} else {
				b.WriteByte('?')
			}
		}
		b.WriteByte('/')
	}
	return b.String()
}

var sensitiveFieldNames = map[string]bool{
	"password":      true,
	"passwd":        true,
	"pwd":           true,
	"secret":        true,
	"token":         true,
	"accesstoken":   true,
	"refreshtoken":  true,
	"authorization": true,
	"credentials":   true,
	"apikey":        true,
	"privatekey":    true,
}

// isSensitiveField reports whether name denotes a sensitive field. Matching is
// case-insensitive and tolerates underscores and camel case. It matches either
// the whole name or a final word such as "Password" in "UserPassword", while
// avoiding short ambiguous prefixes such as "AuthorName".
func isSensitiveField(name string) bool {
	var b strings.Builder
	for _, r := range name {
		if r == '_' || r == '-' {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	normalized := b.String()
	if normalized == "" || sensitiveFieldNames[normalized] {
		return normalized != ""
	}
	words := splitCamelWords(name)
	last := strings.ToLower(strings.Join(words[len(words)-1:], ""))
	return len(last) >= 4 && sensitiveFieldNames[last]
}

func splitCamelWords(s string) []string {
	var words []string
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if r == '_' || r == '-' {
			if b.Len() > 0 {
				words = append(words, b.String())
				b.Reset()
			}
			continue
		}
		if i > 0 && unicode.IsUpper(r) {
			prev := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (nextLower && unicode.IsUpper(prev)) {
				words = append(words, b.String())
				b.Reset()
			}
		}
		b.WriteRune(r)
	}
	if b.Len() > 0 {
		words = append(words, b.String())
	}
	return words
}

const redactedText = "***redacted***"

// valueFormatter renders a reflect.Value into a Go-like summary without
// invoking fmt.Stringer, fmt.GoStringer, error, or other formatting hooks.
// It emits no pointer addresses, bounds output to a byte limit, and terminates
// on reference cycles.
type valueFormatter struct {
	limit    int
	redacted bool
	b        strings.Builder
	left     int
	trunc    bool
	seen     map[value.Pointer]bool
}

func newValueFormatter(limit int, redacted bool) *valueFormatter {
	return &valueFormatter{limit: limit, redacted: redacted, left: limit, seen: map[value.Pointer]bool{}}
}

func (f *valueFormatter) format(v reflect.Value) ValueSummary {
	summary := ValueSummary{Type: typeString(v.Type())}
	if v.IsValid() && v.Kind() == reflect.Interface && !v.IsNil() {
		// Report the concrete dynamic type for mismatched interface values.
		summary.Type = typeString(v.Elem().Type())
	}
	switch {
	case f.redacted:
		f.write(redactedText)
	case !v.IsValid():
		summary.Type = ""
		f.write("<invalid>")
	default:
		f.value(v)
	}
	summary.Truncated = f.trunc
	summary.Redacted = f.redacted
	summary.Text = f.b.String()
	return summary
}

// write appends s while reserving room for an ellipsis once the byte budget is
// exhausted. The emitted length never exceeds the configured limit.
func (f *valueFormatter) write(s string) {
	if f.trunc {
		return
	}
	if len(s) >= f.left-3 {
		if f.left > 3 {
			f.b.WriteString(s[:f.left-3])
		}
		f.b.WriteString("...")
		f.trunc = true
		return
	}
	f.b.WriteString(s)
	f.left -= len(s)
}

func (f *valueFormatter) writeString(s string) {
	f.write(strconv.Quote(s))
}

func (f *valueFormatter) value(v reflect.Value) {
	if f.trunc {
		return
	}
	t := v.Type()
	switch t.Kind() {
	case reflect.Bool:
		f.write(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f.write(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		f.write(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32:
		f.write(strconv.FormatFloat(v.Float(), 'g', -1, 32))
	case reflect.Float64:
		f.write(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	case reflect.Complex64:
		f.write(strconv.FormatComplex(v.Complex(), 'g', -1, 64))
	case reflect.Complex128:
		f.write(strconv.FormatComplex(v.Complex(), 'g', -1, 128))
	case reflect.String:
		f.writeString(v.String())
	case reflect.UnsafePointer:
		if v.IsNil() {
			f.write("nil")
		} else {
			f.write("unsafe.Pointer") // Never expose an address.
		}
	case reflect.Chan:
		if v.IsNil() {
			f.write("nil")
		} else {
			f.write(typeString(t))
		}
	case reflect.Func:
		if v.IsNil() {
			f.write("nil")
		} else {
			f.write(typeString(t))
		}
	case reflect.Interface:
		if v.IsNil() {
			f.write("nil")
			return
		}
		f.value(v.Elem())
	case reflect.Ptr:
		f.ptrValue(v)
	case reflect.Struct:
		f.structValue(v)
	case reflect.Slice, reflect.Array:
		f.sliceValue(v)
	case reflect.Map:
		f.mapValue(v)
	default:
		f.write("<unsupported>")
	}
}

func (f *valueFormatter) ptrValue(v reflect.Value) {
	if v.IsNil() {
		f.write("nil")
		return
	}
	p := value.PointerOf(v)
	if f.seen[p] {
		f.write("<cycle>")
		return
	}
	f.seen[p] = true
	defer delete(f.seen, p)
	f.write("&")
	f.value(v.Elem())
}

func (f *valueFormatter) structValue(v reflect.Value) {
	t := v.Type()
	f.write(typeString(t))
	f.write("{")
	first := true
	for i := 0; i < t.NumField(); i++ {
		if f.trunc {
			return
		}
		sf := t.Field(i)
		fv := v.Field(i)
		if !first {
			f.write(", ")
		}
		f.write(sf.Name)
		f.write(":")
		if !fv.CanInterface() {
			// Do not defeat unexported-field protection in a summary.
			f.write("<unexported>")
		} else {
			f.value(fv)
		}
		first = false
	}
	f.write("}")
}

func (f *valueFormatter) sliceValue(v reflect.Value) {
	t := v.Type()
	if t.Kind() == reflect.Slice {
		if v.IsNil() {
			f.write("nil")
			return
		}
		p := value.PointerOf(v)
		if f.seen[p] {
			f.write("<cycle>")
			return
		}
		f.seen[p] = true
		defer delete(f.seen, p)
	}
	f.write(typeString(t))
	f.write("{")
	for i := 0; i < v.Len(); i++ {
		if f.trunc {
			return
		}
		if i > 0 {
			f.write(", ")
		}
		f.value(v.Index(i))
	}
	f.write("}")
}

func (f *valueFormatter) mapValue(v reflect.Value) {
	t := v.Type()
	if v.IsNil() {
		f.write("nil")
		return
	}
	p := value.PointerOf(v)
	if f.seen[p] {
		f.write("<cycle>")
		return
	}
	f.seen[p] = true
	defer delete(f.seen, p)

	keys := value.SortKeys(v.MapKeys())
	f.write(typeString(t))
	f.write("{")
	for i, k := range keys {
		if f.trunc {
			return
		}
		if i > 0 {
			f.write(", ")
		}
		// Map keys must be comparable, so they cannot themselves be cyclic.
		f.value(k)
		f.write(":")
		f.value(v.MapIndex(k))
	}
	f.write("}")
}
