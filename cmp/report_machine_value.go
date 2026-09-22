// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp/internal/value"
)

// maxSummaryDepth bounds recursion while rendering a single value summary.
// Together with reference-cycle detection this guarantees rendering
// always terminates.
const maxSummaryDepth = 10000

// redactedPlaceholder replaces values of sensitive-looking struct fields.
const redactedPlaceholder = "<redacted>"

// machineSummarizer renders a compact, length-limited summary of a
// reflect.Value. It never calls String, GoString, or error methods, never
// prints pointer addresses, redacts sensitive fields, and terminates on
// cycles.
type machineSummarizer struct {
	buf       strings.Builder
	maxBytes  int
	written   int
	truncated bool

	// visited holds Ptr/Slice/Map pointers currently on the render stack
	// for cycle detection. The pointers are never formatted.
	visited map[visitID]bool
	depth   int
}

type visitID struct {
	p uintptr
	k reflect.Kind
}

// summarizeValue renders v with an optional byte limit. A non-positive
// maxBytes means unlimited.
func summarizeValue(v reflect.Value, maxBytes int) MachineValueSummary {
	s := &machineSummarizer{maxBytes: maxBytes, visited: map[visitID]bool{}}
	s.value(v)
	return MachineValueSummary{Text: s.buf.String(), Truncated: s.truncated}
}

const ellipsis = "..."

// write appends str, honoring the byte limit. It appends an ellipsis on a
// rune boundary once when the limit is reached.
func (s *machineSummarizer) write(str string) bool {
	if s.truncated {
		return false
	}
	if s.maxBytes <= 0 || s.written+len(str) <= s.maxBytes-len(ellipsis) {
		s.buf.WriteString(str)
		s.written += len(str)
		return true
	}
	remain := s.maxBytes - s.written - len(ellipsis)
	if remain > 0 {
		b := str[:remain]
		for len(b) > 0 && !utf8.ValidString(b) {
			b = b[:len(b)-1]
		}
		s.buf.WriteString(b)
		s.written += len(b)
	}
	s.truncated = true
	s.buf.WriteString(ellipsis)
	return false
}

// value renders v.
func (s *machineSummarizer) value(v reflect.Value) {
	if !v.IsValid() {
		s.write("<invalid>")
		return
	}
	if s.depth >= maxSummaryDepth {
		s.truncated = true
		s.write(ellipsis)
		return
	}
	t := v.Type()
	name := value.TypeString(t, false)
	switch t.Kind() {
	case reflect.Bool:
		s.write(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		s.write(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		s.write(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		s.write(formatFloat(v.Float(), v.Type().Kind() == reflect.Float32))
	case reflect.Complex64, reflect.Complex128:
		c := v.Complex()
		b32 := v.Type().Kind() == reflect.Complex64
		s.write("(" + formatFloat(real(c), b32) + "+" + formatFloat(imag(c), b32) + "i)")
	case reflect.String:
		s.write(strconv.Quote(v.String()))
	case reflect.Chan:
		s.write(name + parenNil(v.IsNil()))
	case reflect.Func, reflect.UnsafePointer:
		s.write(name + parenNil(v.IsNil()))
	case reflect.Interface:
		if v.IsNil() {
			s.write("nil")
		} else {
			e := v.Elem()
			s.write("(" + value.TypeString(e.Type(), false) + ")")
			s.value(e)
		}
	case reflect.Ptr:
		s.writePtr(v, name)
	case reflect.Struct:
		s.writeStruct(v, name)
	case reflect.Slice, reflect.Array:
		s.writeSlice(v, name)
	case reflect.Map:
		s.writeMap(v, name)
	}
}

func parenNil(isNil bool) string {
	if isNil {
		return "(nil)"
	}
	return "(non-nil)"
}

func (s *machineSummarizer) writePtr(v reflect.Value, name string) {
	if v.IsNil() {
		s.write(name + "(nil)")
		return
	}
	id := visitID{v.Pointer(), reflect.Ptr}
	if s.visited[id] {
		s.write(name + "{<cycle>}")
		return
	}
	s.visited[id] = true
	defer delete(s.visited, id)
	s.write(name + "{")
	s.depth++
	s.value(v.Elem())
	s.depth--
	s.write("}")
}

func (s *machineSummarizer) writeStruct(v reflect.Value, name string) {
	if v.Type().Name() != "" {
		s.write(name)
	}
	s.write("{")
	s.depth++
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if i > 0 && !s.write(", ") {
			return
		}
		f := t.Field(i)
		if !s.write(f.Name + ": ") {
			return
		}
		fv := v.Field(i)
		if isSensitiveFieldName(f.Name) {
			// Redact before rendering; never inspect the field value, so no
			// Stringer on a sensitive value is ever invoked.
			s.write(redactedPlaceholder)
			continue
		}
		if !fv.CanInterface() {
			// Unexported field the engine did not make accessible; do not use
			// unsafe to read it.
			s.write("<unexported>")
			continue
		}
		s.value(fv)
		if s.truncated {
			return
		}
	}
	s.depth--
	s.write("}")
}

func (s *machineSummarizer) writeSlice(v reflect.Value, name string) {
	if v.Kind() == reflect.Slice && v.IsNil() {
		s.write(name + "(nil)")
		return
	}
	if v.Kind() == reflect.Slice {
		id := visitID{v.Pointer(), reflect.Slice}
		if s.visited[id] {
			s.write(name + "[<cycle>]")
			return
		}
		s.visited[id] = true
		defer delete(s.visited, id)
	}
	s.write(name + "[")
	s.depth++
	for i := 0; i < v.Len(); i++ {
		if i > 0 && !s.write(", ") {
			return
		}
		s.value(v.Index(i))
		if s.truncated {
			return
		}
	}
	s.depth--
	s.write("]")
}

func (s *machineSummarizer) writeMap(v reflect.Value, name string) {
	if v.IsNil() {
		s.write(name + "(nil)")
		return
	}
	id := visitID{v.Pointer(), reflect.Map}
	if s.visited[id] {
		s.write(name + "[<cycle>]")
		return
	}
	s.visited[id] = true
	defer delete(s.visited, id)
	s.write(name + "{")
	s.depth++
	for i, k := range value.SortKeys(v.MapKeys()) {
		if i > 0 && !s.write(", ") {
			return
		}
		s.key(k)
		if s.truncated || !s.write(": ") {
			return
		}
		s.value(v.MapIndex(k))
		if s.truncated {
			return
		}
	}
	s.depth--
	s.write("}")
}

// key renders a map key already known to have a stable textual identity.
func (s *machineSummarizer) key(v reflect.Value) {
	s.value(v)
}

// formatFloat renders a float while distinguishing NaN and infinities
// without relying on the value's String method.
func formatFloat(f float64, bits32 bool) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	default:
		bits := 64
		if bits32 {
			bits = 32
		}
		return strconv.FormatFloat(f, 'g', -1, bits)
	}
}

// isSensitiveFieldName reports whether a struct field name suggests secret
// material that must be redacted in summaries.
func isSensitiveFieldName(name string) bool {
	lower := strings.ToLower(name)
	if lower == "pwd" {
		return true
	}
	for _, part := range sensitiveFieldParts {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

var sensitiveFieldParts = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"apikey",
	"api_key",
	"credential",
	"privatekey",
	"private_key",
}

// stableKeyText reports whether key v has a stable textual identity and
// returns it. Pointer-addressed keys and keys containing NaN are not stable
// and use the typed ordinal path instead.
func stableKeyText(v reflect.Value, maxBytes int) (string, bool) {
	if !v.IsValid() || !stableKeyType(v.Type()) || containsNaN(v) {
		return "", false
	}
	s := &machineSummarizer{maxBytes: maxBytes, visited: map[visitID]bool{}}
	s.value(v)
	if s.truncated {
		return "", false
	}
	return s.buf.String(), true
}

// stableKeyType reports whether t has pointer-independent identity: booleans,
// numbers, strings, and arrays or structs composed of those, plus interface
// values that hold such types.
func stableKeyType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
		reflect.Array, reflect.Struct, reflect.Interface:
		return true
	default:
		return false
	}
}

// containsNaN reports whether v is or contains a NaN float.
func containsNaN(v reflect.Value) bool {
	if !v.IsValid() {
		return false
	}
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return math.IsNaN(v.Float())
	case reflect.Complex64, reflect.Complex128:
		c := v.Complex()
		return math.IsNaN(real(c)) || math.IsNaN(imag(c))
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if containsNaN(v.Index(i)) {
				return true
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if fv := v.Field(i); fv.CanInterface() && containsNaN(fv) {
				return true
			}
		}
	case reflect.Interface:
		return containsNaN(v.Elem())
	}
	return false
}

func typeName(t reflect.Type) string {
	if t == nil {
		return ""
	}
	return value.TypeString(t, false)
}
