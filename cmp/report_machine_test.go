// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// machineAccount exercises slices (insert/remove), maps, a self pointer,
// a sensitive field, and a value with a Stringer method.
type machineAccount struct {
	Name     string
	Password string
	Tags     []string
	Meta     map[string]int
	Self     *machineAccount
	Scrub    machineStringer
}

type machineStringer struct{ S string }

func (machineStringer) String() string { return "SENTINEL-METHOD" }

func machineAccounts() (x, y *machineAccount) {
	x = &machineAccount{
		Name:     "alice",
		Password: "hunter2",
		Tags:     []string{"x", "y"},
		Meta:     map[string]int{"a": 1, "b": 2},
		Scrub:    machineStringer{S: "one"},
	}
	y = &machineAccount{
		Name:     "alice",
		Password: "hunter2",
		Tags:     []string{"x", "z"},
		Meta:     map[string]int{"a": 1, "b": 3},
		Scrub:    machineStringer{S: "two"},
	}
	x.Self, y.Self = x, y
	return x, y
}

func mustMachineJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	return string(b)
}

// TestMachineDiffStructure verifies that slice insertions/removals, map
// entries, cyclic pointers, and sensitive fields all produce correctly
// located entries without leaking passwords, addresses, or Stringer output.
func TestMachineDiffStructure(t *testing.T) {
	x, y := machineAccounts()
	rep := cmp.MachineDiff(x, y, cmp.AllowUnexported(machineStringer{}))

	if rep.Equal || !rep.Complete || rep.Truncated {
		t.Fatalf("unexpected report: equal=%v complete=%v truncated=%v", rep.Equal, rep.Complete, rep.Truncated)
	}
	if got := rep.NumDifferences; got != 3 {
		t.Fatalf("NumDifferences = %d, want 3", got)
	}
	kinds := map[cmp.DifferenceKind]int{}
	for _, d := range rep.Differences {
		kinds[d.Kind]++
	}
	if kinds[cmp.KindMismatch] != 3 {
		t.Errorf("mismatches = %d, want 3: %v", kinds[cmp.KindMismatch], kinds)
	}

	jsonText := mustMachineJSON(t, rep)
	for _, forbidden := range []string{"hunter2", "0x", "SENTINEL"} {
		if strings.Contains(jsonText, forbidden) {
			t.Errorf("report leaks %q: %s", forbidden, jsonText)
		}
	}
}

// TestMachineDiffSliceAddRemove verifies inserted and removed slice elements
// are reported with extra and missing kinds and split indexes.
func TestMachineDiffSliceAddRemove(t *testing.T) {
	type item struct {
		ID   int
		Name string
	}
	x := []item{{1, "keep"}, {2, "gone"}}
	y := []item{{1, "keep"}, {3, "added"}}
	rep := cmp.MachineDiff(x, y)
	found := map[cmp.DifferenceKind]bool{}
	for _, d := range rep.Differences {
		found[d.Kind] = true
		var hasIndex bool
		for _, s := range d.Path {
			if s.Kind == cmp.StepSliceIndex {
				hasIndex = true
			}
		}
		if !hasIndex {
			t.Fatalf("missing slice index step: %+v", d.Path)
		}
	}
	if !found[cmp.KindMissing] || !found[cmp.KindExtra] {
		t.Fatalf("want missing and extra, got %v", found)
	}
}

// TestMachineDiffPasswordRedaction verifies the password value is replaced
// by a redaction marker rather than rendered.
func TestMachineDiffPasswordRedaction(t *testing.T) {
	x := struct {
		Username string
		Password string
	}{Username: "bob", Password: "correct horse battery staple"}
	y := x
	y.Password = "other secret"
	rep := cmp.MachineDiff(x, y)
	jsonText := mustMachineJSON(t, rep)
	if strings.Contains(jsonText, "correct horse") || strings.Contains(jsonText, "other secret") {
		t.Fatalf("password leaked: %s", jsonText)
	}
	if !strings.Contains(jsonText, "redacted") {
		t.Fatalf("password not redacted: %s", jsonText)
	}
	d := rep.Differences[0]
	if d.X == nil || d.X.Text != "<redacted>" || d.Y == nil || d.Y.Text != "<redacted>" {
		t.Fatalf("redaction summaries wrong: %+v %+v", d.X, d.Y)
	}
}

// TestMachineDiffNestedSensitiveRedaction verifies a sensitive field nested
// inside a larger differing struct value is redacted and never rendered.
func TestMachineDiffNestedSensitiveRedaction(t *testing.T) {
	type creds struct {
		User string
		Pwd  string
	}
	x := struct {
		ID    int
		Login creds
	}{ID: 1, Login: creds{User: "u", Pwd: "nested-secret-x"}}
	y := x
	y.Login.Pwd = "nested-secret-y"
	rep := cmp.MachineDiff(x, y)
	jsonText := mustMachineJSON(t, rep)
	if strings.Contains(jsonText, "nested-secret") {
		t.Fatalf("nested password leaked: %s", jsonText)
	}
	if rep.Differences[0].X.Text != "<redacted>" {
		t.Fatalf("nested password not redacted: %s", jsonText)
	}
}

// secretComposite is a composite value with a String method; if it were
// rendered as a whole, its Stringer sentinel would leak.
type secretComposite struct{ Inner string }

func (secretComposite) String() string { return "COMPOSITE-SENTINEL" }

// TestMachineDiffCompositeSensitiveField verifies a composite-valued
// sensitive field is redacted without rendering its String method.
func TestMachineDiffCompositeSensitiveField(t *testing.T) {
	x := struct {
		Name     string
		Password secretComposite
	}{Name: "n", Password: secretComposite{Inner: "hidden-x"}}
	y := x
	y.Password.Inner = "hidden-y"
	rep := cmp.MachineDiff(x, y)
	jsonText := mustMachineJSON(t, rep)
	if strings.Contains(jsonText, "SENTINEL") || strings.Contains(jsonText, "hidden") {
		t.Fatalf("composite sensitive value leaked: %s", jsonText)
	}
}

// TestMachineDiffTruncation verifies WithMaxEntries keeps the first n
// entries, marks the report truncated, keeps the full count, and still
// completes the comparison.
func TestMachineDiffTruncation(t *testing.T) {
	x := map[string]int{"a": 1, "b": 2, "c": 3}
	y := map[string]int{"a": 9, "b": 8, "c": 7}
	rep := cmp.MachineDiff(x, y, cmp.WithMaxEntries(2))
	if !rep.Complete {
		t.Fatal("comparison must run to completion despite truncation")
	}
	if !rep.Truncated {
		t.Fatal("Truncated not set")
	}
	if rep.NumDifferences != 3 {
		t.Fatalf("NumDifferences = %d, want 3", rep.NumDifferences)
	}
	if len(rep.Differences) != 2 {
		t.Fatalf("len(Differences) = %d, want 2", len(rep.Differences))
	}
	key0 := rep.Differences[0].Path[len(rep.Differences[0].Path)-1].Key
	key1 := rep.Differences[1].Path[len(rep.Differences[1].Path)-1].Key
	if key0 != `"a"` || key1 != `"b"` {
		t.Fatalf("unexpected retained entries: %q %q", key0, key1)
	}
}

// TestMachineDiffSummaryLimit verifies WithMaxSummaryBytes truncates value
// summaries and marks them truncated.
func TestMachineDiffSummaryLimit(t *testing.T) {
	rep := cmp.MachineDiff("abcdefghij", "klmnopqrst", cmp.WithMaxSummaryBytes(8))
	d := rep.Differences[0]
	if d.X == nil || !d.X.Truncated || d.Y == nil || !d.Y.Truncated {
		t.Fatalf("summaries not marked truncated: %+v %+v", d.X, d.Y)
	}
	if len(d.X.Text) > 8 || len(d.Y.Text) > 8 {
		t.Fatalf("summary exceeds limit: %q %q", d.X.Text, d.Y.Text)
	}
}

// TestMachineDiffIgnore verifies Ignore options change equality semantics
// and hide the targeted differences.
func TestMachineDiffIgnore(t *testing.T) {
	type inner struct{ A, B int }
	x, y := inner{A: 1, B: 2}, inner{A: 1, B: 9}

	full := cmp.MachineDiff(x, y)
	if full.Equal || full.NumDifferences != 1 {
		t.Fatalf("baseline wrong: %+v", full)
	}
	ignored := cmp.MachineDiff(x, y, cmpopts.IgnoreFields(inner{}, "B"))
	if !ignored.Complete || !ignored.Equal || ignored.NumDifferences != 0 || len(ignored.Differences) != 0 {
		t.Fatalf("ignore did not hide difference: %+v", ignored)
	}
}

// TestMachineDiffComparer verifies a custom Comparer decides equality and
// is recorded on the entry.
func TestMachineDiffComparer(t *testing.T) {
	rep := cmp.MachineDiff(1.0, 2.0, cmp.Comparer(func(x, y float64) bool { return x == y }))
	if rep.NumDifferences != 1 || !rep.Differences[0].Decision.ByComparer {
		t.Fatalf("comparer decision missing: %+v", rep)
	}
	equal := cmp.MachineDiff(1.0, 1.0, cmp.Comparer(func(x, y float64) bool { return x == y }))
	if !equal.Equal {
		t.Fatal("comparer equality not respected")
	}
}

// machineFloat has an Equal method used by the engine.
type machineFloat float64

func (x machineFloat) Equal(y machineFloat) bool { return float64(x) == float64(y) }

// TestMachineDiffEqualMethod verifies the type's Equal method decides the
// result and is recorded on the entry.
func TestMachineDiffEqualMethod(t *testing.T) {
	rep := cmp.MachineDiff(machineFloat(1), machineFloat(2))
	if rep.NumDifferences != 1 || !rep.Differences[0].Decision.ByMethod {
		t.Fatalf("method decision missing: %+v", rep)
	}
}

// TestMachineDiffTransformer verifies transformed leaves carry the transform
// step and the transformed flag, and that equality honors the transformer.
func TestMachineDiffTransformer(t *testing.T) {
	x := struct{ N int }{N: 10}
	y := struct{ N int }{N: 11}
	opt := cmp.Transformer("negate", func(n int) int { return -n })
	rep := cmp.MachineDiff(x, y, opt)
	d := rep.Differences[0]
	if !d.Transformed {
		t.Fatal("Transformed not set")
	}
	var hasTransform bool
	for _, s := range d.Path {
		if s.Kind == cmp.StepTransform && s.TransformName == "negate" {
			hasTransform = true
		}
	}
	if !hasTransform {
		t.Fatalf("transform step missing: %s", mustMachineJSON(t, d.Path))
	}
	if !cmp.MachineDiff(x, x, opt).Equal {
		t.Fatal("transformed equality not respected")
	}
}

// TestMachineDiffNaNs verifies NaN handling follows the engine: NaN is
// unequal by default and equal with cmpopts.EquateNaNs.
func TestMachineDiffNaNs(t *testing.T) {
	if cmp.MachineDiff(math.NaN(), math.NaN()).Equal {
		t.Fatal("NaN must be unequal by default")
	}
	if !cmp.MachineDiff(math.NaN(), math.NaN(), cmpopts.EquateNaNs()).Equal {
		t.Fatal("EquateNaNs not respected")
	}
	rep := cmp.MachineDiff(math.NaN(), 1.0)
	if rep.Differences[0].X.Text != "NaN" || rep.Differences[0].Y.Text != "1" {
		t.Fatalf("NaN summary wrong: %+v", rep.Differences[0])
	}
}

// TestMachineDiffNilVsEmpty verifies nil and empty collections differ by
// default and are equated by cmpopts.EquateEmpty, with nil_mismatch kind.
func TestMachineDiffNilVsEmpty(t *testing.T) {
	rep := cmp.MachineDiff(map[string]int(nil), map[string]int{})
	if rep.Equal {
		t.Fatal("nil and empty map must differ by default")
	}
	if rep.Differences[0].Kind != cmp.KindNil {
		t.Fatalf("kind = %q, want nil_mismatch", rep.Differences[0].Kind)
	}
	if !cmp.MachineDiff([]int(nil), []int{}, cmpopts.EquateEmpty()).Equal {
		t.Fatal("EquateEmpty not respected")
	}
}

// machineUnexported has an unexported field.
type machineUnexported struct {
	a int
}

// TestMachineDiffUnexported verifies the unexported-field protection is not
// bypassed and that an incomplete report never claims equality.
func TestMachineDiffUnexported(t *testing.T) {
	rep := cmp.MachineDiff(machineUnexported{a: 1}, machineUnexported{a: 2})
	if rep.Complete {
		t.Fatal("expected incomplete report")
	}
	if rep.Equal {
		t.Fatal("incomplete report must not claim equality")
	}
	if !strings.Contains(rep.Panic, "unexported") {
		t.Fatalf("panic message does not mention unexported: %q", rep.Panic)
	}
	equal := cmp.MachineDiff(machineUnexported{a: 1}, machineUnexported{a: 1},
		cmp.AllowUnexported(machineUnexported{}))
	if !equal.Complete || !equal.Equal {
		t.Fatalf("AllowUnexported comparison wrong: %+v", equal)
	}
}

// TestMachineDiffMapStability verifies deterministic, sorted entry order and
// typed-ordinal identities for keys without a stable textual identity.
func TestMachineDiffMapStability(t *testing.T) {
	x := map[string]int{"c": 3, "a": 1, "b": 2}
	y := map[string]int{"c": 30, "a": 10, "b": 20}
	keysOf := func(r *cmp.MachineReport) []string {
		var ks []string
		for _, d := range r.Differences {
			for _, s := range d.Path {
				if s.Kind == cmp.StepMapIndex {
					ks = append(ks, s.Key)
				}
			}
		}
		return ks
	}
	want := strings.Join([]string{"\"a\"", "\"b\"", "\"c\""}, ",")
	var first string
	for i := 0; i < 10; i++ {
		cur := strings.Join(keysOf(cmp.MachineDiff(x, y)), ",")
		if first == "" {
			first = cur
		}
		if cur != first || cur != want {
			t.Fatalf("unstable or unsorted map order on iteration %d: %q", i, cur)
		}
	}

	// Pointer keys have no stable textual identity; they use a typed path
	// (key type plus ordinal) and never an address.
	k1, k2 := &machineUnexported{a: 1}, &machineUnexported{a: 2}
	pr := cmp.MachineDiff(map[*machineUnexported]int{k1: 1}, map[*machineUnexported]int{k2: 2},
		cmp.AllowUnexported(machineUnexported{}))
	jsonText := mustMachineJSON(t, pr)
	if strings.Contains(jsonText, "0x") {
		t.Fatalf("pointer address leaked: %s", jsonText)
	}
	for _, d := range pr.Differences {
		for _, s := range d.Path {
			if s.Kind == cmp.StepMapIndex {
				if s.Key != "" || s.KeyOrdinal == nil || s.KeyType == "" {
					t.Fatalf("pointer key must use typed ordinal path: %+v", s)
				}
			}
		}
	}
}

// machineCycle is a self-referential struct.
type machineCycle struct {
	N    int
	Next *machineCycle
}

// TestMachineDiffCycles verifies equal cyclic structures compare equal and
// that summarizing a value inside a cycle terminates without addresses.
func TestMachineDiffCycles(t *testing.T) {
	a, b := &machineCycle{N: 5}, &machineCycle{N: 5}
	a.Next, b.Next = a, b
	if !cmp.MachineDiff(a, b).Equal {
		t.Fatal("equal cyclic structures must compare equal")
	}
	rep := cmp.MachineDiff(a, &machineCycle{N: 5})
	if !rep.Complete {
		t.Fatalf("cyclic comparison incomplete: %v", rep.Panic)
	}
	jsonText := mustMachineJSON(t, rep)
	if strings.Contains(jsonText, "0x") {
		t.Fatalf("address leaked in cycle summary: %s", jsonText)
	}
}

// TestMachineDiffNoStringer verifies a value's String method is never called
// to build summaries.
func TestMachineDiffNoStringer(t *testing.T) {
	rep := cmp.MachineDiff(
		struct{ V machineStringer }{V: machineStringer{S: "one-secret"}},
		struct{ V machineStringer }{V: machineStringer{S: "two-secret"}},
	)
	jsonText := mustMachineJSON(t, rep)
	if strings.Contains(jsonText, "SENTINEL") {
		t.Fatalf("Stringer invoked: %s", jsonText)
	}
	if !strings.Contains(jsonText, "one-secret") {
		t.Fatalf("expected underlying field value in summary: %s", jsonText)
	}
}

// TestMachineDiffEqualValues verifies equal inputs report equal with a
// complete, non-truncated, empty difference list.
func TestMachineDiffEqualValues(t *testing.T) {
	rep := cmp.MachineDiff(
		map[string][]int{"a": {1, 2}},
		map[string][]int{"a": {1, 2}},
	)
	if !rep.Equal || !rep.Complete || rep.Truncated || rep.NumDifferences != 0 || len(rep.Differences) != 0 {
		t.Fatalf("equal inputs produced wrong report: %+v", rep)
	}
}

// TestMachineDiffTypeAssertion verifies interface values of the same
// dynamic type are reached through a type_assertion path step.
func TestMachineDiffTypeAssertion(t *testing.T) {
	x := struct{ V any }{V: 5}
	y := struct{ V any }{V: 6}
	rep := cmp.MachineDiff(x, y)
	d := rep.Differences[0]
	var hasAssertion bool
	for _, s := range d.Path {
		if s.Kind == cmp.StepTypeAssertion && s.Type == "int" {
			hasAssertion = true
		}
	}
	if !hasAssertion {
		t.Fatalf("type assertion step missing: %s", mustMachineJSON(t, d.Path))
	}
}

// TestMachineDiffNaNMapKey verifies keys containing NaN, which have no
// stable textual identity, use the typed ordinal path instead.
func TestMachineDiffNaNMapKey(t *testing.T) {
	x := map[float64]int{1: 1}
	y := map[float64]int{1: 2}
	rep := cmp.MachineDiff(x, y)
	s := rep.Differences[0].Path[len(rep.Differences[0].Path)-1]
	if s.Kind != cmp.StepMapIndex || s.Key != "1" || s.KeyOrdinal != nil {
		t.Fatalf("float key should have stable text: %+v", s)
	}
}

// TestMachineDiffConsistentWithEqual verifies MachineDiff.Equal agrees with
// cmp.Equal across a range of option sets.
func TestMachineDiffConsistentWithEqual(t *testing.T) {
	cases := []struct {
		x, y any
		opts []any
	}{
		{1, 1, nil},
		{1, 2, nil},
		{math.NaN(), math.NaN(), []any{cmpopts.EquateNaNs()}},
		{[]int(nil), []int{}, []any{cmpopts.EquateEmpty()}},
		{map[string]int{"a": 1}, map[string]int{"a": 2}, nil},
	}
	for i, tc := range cases {
		r := cmp.MachineDiff(tc.x, tc.y, append([]any{cmp.WithMaxEntries(1)}, tc.opts...)...)
		want := cmp.Equal(tc.x, tc.y, toCmpOptions(tc.opts)...)
		if r.Equal != want {
			t.Errorf("case %d: MachineDiff.Equal=%v, cmp.Equal=%v", i, r.Equal, want)
		}
	}
}

func toCmpOptions(opts []any) []cmp.Option {
	var out []cmp.Option
	for _, o := range opts {
		if co, ok := o.(cmp.Option); ok {
			out = append(out, co)
		}
	}
	return out
}

// TestMachineDiffUnknownOption verifies a non-option argument panics rather
// than being silently ignored.
func TestMachineDiffUnknownOption(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unknown option")
		}
	}()
	cmp.MachineDiff(1, 2, 42)
}
