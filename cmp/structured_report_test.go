// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp_test

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// secretStringer would leak sentinel text if summaries invoked String().
type secretStringer string

func (s secretStringer) String() string { return "STRINGER_SENTINEL" }

type account struct {
	Name     string
	Password secretStringer
	Tags     []string
	Meta     map[string]int
	Next     *account
}

func TestCompareMapOrderStable(t *testing.T) {
	x := map[string]int{"banana": 1, "cherry": 2, "apple": 3}
	y := map[string]int{"banana": 9, "cherry": 8, "apple": 7}
	var first []cmp.PathElement
	for i := 0; i < 20; i++ {
		r := cmp.Compare(x, y, cmp.CompareConfig{MaxSummaryBytes: 16})
		if len(r.Differences) != 3 {
			t.Fatalf("iteration %d: %+v", i, r)
		}
		keys := []string{}
		for _, d := range r.Differences {
			for _, el := range d.Path {
				if el.Kind == "map_index" && el.MapKey != nil {
					keys = append(keys, el.MapKey.Text)
				}
			}
		}
		if got := strings.Join(keys, ","); got != "\"apple\",\"banana\",\"cherry\"" {
			t.Fatalf("unstable map order: %s", got)
		}
		if i == 0 {
			first = append([]cmp.PathElement(nil), r.Differences[0].Path...)
			continue
		}
		if !reflect.DeepEqual(first, r.Differences[0].Path) {
			t.Fatalf("path changed across runs")
		}
	}
}

func TestCompareSummaryTruncationAndRedaction(t *testing.T) {
	type row struct {
		Note string
		SSN  string
	}
	x := row{Note: strings.Repeat("z", 500), SSN: "123-45-6789"}
	y := row{Note: strings.Repeat("q", 500), SSN: "999-99-9999"}
	r := cmp.Compare(x, y, cmp.CompareConfig{
		MaxSummaryBytes: 24,
		Redact: func(p cmp.Path) bool {
			if sf, ok := p.Last().(cmp.StructField); ok {
				return sf.Name() == "Note"
			}
			return false
		},
	})
	if len(r.Differences) != 2 {
		t.Fatalf("%+v", r)
	}
	note := r.Differences[0]
	if note.X == nil || !note.X.Redacted || note.X.Text != "***redacted***" {
		t.Fatalf("custom redaction failed: %+v", note.X)
	}
	if note.Y != nil && len(note.Y.Text) > 24 {
		t.Fatalf("summary byte limit exceeded: %q", note.Y.Text)
	}
	// SSN is not a built-in sensitive name; its long text truncates.
	ssn := r.Differences[1]
	if ssn.X.Redacted || ssn.X.Truncated {
		t.Fatalf("short SSN should be untruncated: %+v", ssn.X)
	}
}

func TestCompareNoPointerAddresses(t *testing.T) {
	type node struct{ V int }
	x := &node{V: 1}
	y := &node{V: 2}
	r := cmp.Compare(x, y, cmp.CompareConfig{})
	js := reportJSON(t, r)
	if strings.ContainsAny(js, "0123456789abcdef") && strings.Contains(js, "0x") {
		t.Fatalf("address-like text present: %s", js)
	}
	if !r.Differences[0].Path[0].Root {
		t.Fatalf("root step missing: %+v", r.Differences[0].Path)
	}
}

func TestCompareDiffContractUnchanged(t *testing.T) {
	x, y := makeAccounts()
	// Ignore the cycle so the textual diff terminates at the same leaves.
	ignoreNext := cmpopts.IgnoreFields(account{}, "Next", "Password")
	d := cmp.Diff(x, y, ignoreNext)
	if d == "" {
		t.Fatalf("expected textual diff")
	}
	r := cmp.Compare(x, y, cmp.CompareConfig{MaxSummaryBytes: 128}, ignoreNext)
	if r.Equal != (d == "") || r.Equal {
		t.Fatalf("Compare equality=%v but Diff empty=%v", r.Equal, d == "")
	}
}

func TestCompareEqualInputs(t *testing.T) {
	x := map[string][]int{"a": {1, 2, 3}}
	r := cmp.Compare(x, x, cmp.CompareConfig{MaxEntries: 1})
	if !r.Equal || !r.Completed || r.EntriesTruncated || len(r.Differences) != 0 {
		t.Fatalf("equal report wrong: %+v", r)
	}
}

type cycleNode struct {
	V    int
	Next *cycleNode
}

func TestCompareCyclesTerminateAndFlagged(t *testing.T) {
	x := &cycleNode{V: 1}
	x.Next = x
	y := &cycleNode{V: 1}
	y.Next = y
	// Structurally identical cycles are equal and must not recurse forever.
	if r := cmp.Compare(x, y, cmp.CompareConfig{}); !r.Equal || len(r.Differences) != 0 {
		t.Fatalf("equal cycle: %+v", r)
	}

	// Asymmetric graphs hit the cycle rule and are unequal.
	x2 := &cycleNode{V: 1}
	x2.Next = &cycleNode{V: 2, Next: x2}
	y2 := &cycleNode{V: 1}
	y2.Next = y2
	r := cmp.Compare(x2, y2, cmp.CompareConfig{MaxEntries: 10})
	if r.Equal {
		t.Fatalf("asymmetric cycle should differ")
	}
	if !r.Completed {
		t.Fatalf("cycle comparison should finish: %+v", r)
	}
	var byCycle bool
	for _, d := range r.Differences {
		if d.ByCycle {
			byCycle = true
		}
	}
	if !byCycle {
		t.Fatalf("cycle-decided difference not flagged: %+v", r.Differences)
	}
}

func TestCompareSummaryByteLimit(t *testing.T) {
	type long struct{ Label string }
	r := cmp.Compare(long{strings.Repeat("a", 300)}, long{strings.Repeat("b", 300)}, cmp.CompareConfig{MaxSummaryBytes: 40})
	if len(r.Differences) != 1 {
		t.Fatalf("%+v", r)
	}
	for _, s := range []*cmp.ValueSummary{r.Differences[0].X, r.Differences[0].Y} {
		if s == nil || !s.Truncated || len(s.Text) > 40 || !strings.HasSuffix(s.Text, "...") {
			t.Fatalf("bad truncated summary: %+v", s)
		}
	}
}
func TestCompareSelfReferencingSliceSummary(t *testing.T) {
	type item struct {
		V int
		L []*item
	}
	x := &item{V: 1}
	x.L = []*item{x}
	y := &item{V: 2}
	y.L = []*item{y}
	r := cmp.Compare(x, y, cmp.CompareConfig{MaxEntries: 10, MaxSummaryBytes: 200})
	js := reportJSON(t, r)
	if strings.Contains(js, "0x") {
		t.Fatalf("address leaked in recursive summary: %s", js)
	}
	if !r.Completed {
		t.Fatalf("recursive summary aborted early: %+v", r)
	}
}

type NoisyStringer struct{ Called *bool }

func (s NoisyStringer) String() string {
	*s.Called = true
	return "NOISY_SENTINEL"
}

func TestCompareDoesNotInvokeStringer(t *testing.T) {
	calledX, calledY := false, false
	type holder struct{ V NoisyStringer }
	r := cmp.Compare(
		holder{V: NoisyStringer{Called: &calledX}},
		holder{V: NoisyStringer{Called: &calledY}},
		cmp.CompareConfig{MaxSummaryBytes: 128},
	)
	js := reportJSON(t, r)
	if calledX || calledY || strings.Contains(js, "NOISY_SENTINEL") {
		t.Fatalf("Stringer invoked or leaked: x=%v y=%v %s", calledX, calledY, js)
	}
}

type box struct {
	V any
}

func TestCompareTypeAssertionAndTransformer(t *testing.T) {
	// Mismatched concrete types are unequal at the interface field itself.
	r := cmp.Compare(box{V: 1}, box{V: "s"}, cmp.CompareConfig{})
	if r.Equal || len(r.Differences) != 1 {
		t.Fatalf("dynamic-type mismatch: %+v", r)
	}
	if r.Differences[0].XType != "int" || r.Differences[0].YType != "string" {
		t.Fatalf("dynamic types not recorded: %+v", r.Differences[0])
	}
	// Equal dynamic types descend through a TypeAssertion step.
	r2 := cmp.Compare(box{V: 1}, box{V: 2}, cmp.CompareConfig{})
	if r2.Equal || len(r2.Differences) != 1 {
		t.Fatalf("interface value mismatch: %+v", r2)
	}
	found := false
	for _, el := range r2.Differences[0].Path {
		if el.Kind == "type_assertion" {
			found = true
		}
	}
	if !found {
		t.Fatalf("type assertion path missing: %+v", r2.Differences[0].Path)
	}

	// A Transformer changes the compared value and is recorded on the path.
	tr := cmp.Transformer("stringsToLen", func(s string) int { return len(s) })
	rt := cmp.Compare(struct{ S string }{"abc"}, struct{ S string }{"xyz"}, cmp.CompareConfig{}, tr)
	if !rt.Equal {
		t.Fatalf("transformed comparison should be equal: %+v", rt)
	}
	rt2 := cmp.Compare(struct{ S string }{"abc"}, struct{ S string }{"xy"}, cmp.CompareConfig{}, tr)
	if rt2.Equal || len(rt2.Differences) != 1 || !rt2.Differences[0].Transformed {
		t.Fatalf("transform flag missing: %+v", rt2)
	}
	var sawTransform bool
	for _, el := range rt2.Differences[0].Path {
		if el.Kind == "transform" && el.Transform == "stringsToLen" {
			sawTransform = true
		}
	}
	if !sawTransform {
		t.Fatalf("transform path element missing: %+v", rt2.Differences[0].Path)
	}
}

type eqMethod struct{ v int }

func (a eqMethod) Equal(b eqMethod) bool { return a.v%10 == b.v%10 }

func TestCompareEqualMethodAndComparerFlags(t *testing.T) {
	if r := cmp.Compare(eqMethod{1}, eqMethod{11}, cmp.CompareConfig{}); !r.Equal {
		t.Fatalf("Equal method bypassed: %+v", r)
	}
	r := cmp.Compare(eqMethod{1}, eqMethod{2}, cmp.CompareConfig{})
	if r.Equal || !r.Differences[0].ByEqualMethod {
		t.Fatalf("ByEqualMethod missing: %+v", r)
	}
	cm := cmp.Comparer(func(a, b eqMethod) bool { return a.v == b.v+100 })
	r2 := cmp.Compare(eqMethod{1}, eqMethod{2}, cmp.CompareConfig{}, cm)
	if r2.Equal {
		t.Fatalf("comparer unexpectedly equal")
	}
	if !r2.Differences[0].ByComparer || r2.Differences[0].ByEqualMethod {
		t.Fatalf("comparer flags wrong: %+v", r2)
	}
}

type hidden struct {
	Pub int
	pri int
}

func TestCompareUnexportedPanicsAndExportOption(t *testing.T) {
	func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("expected panic on unexported field without Exporter")
			}
		}()
		cmp.Compare(hidden{1, 2}, hidden{1, 3}, cmp.CompareConfig{})
	}()
	// Ignoring unexported fields preserves the established protection model.
	r := cmp.Compare(hidden{1, 2}, hidden{1, 3}, cmp.CompareConfig{}, cmpopts.IgnoreUnexported(hidden{}))
	if !r.Equal {
		t.Fatalf("IgnoreUnexported should equate: %+v", r)
	}
	r2 := cmp.Compare(hidden{1, 2}, hidden{2, 2}, cmp.CompareConfig{}, cmpopts.IgnoreUnexported(hidden{}))
	if r2.Equal || len(r2.Differences) != 1 {
		t.Fatalf("exported field diff missing: %+v", r2)
	}
}
func TestCompareMaxEntriesTruncation(t *testing.T) {
	x, y := makeAccounts()
	r := cmp.Compare(x, y, cmp.CompareConfig{MaxEntries: 2, MaxSummaryBytes: 64})
	if len(r.Differences) != 2 {
		t.Fatalf("got %d entries, want 2", len(r.Differences))
	}
	if r.Completed || !r.EntriesTruncated || r.Equal {
		t.Fatalf("truncation flags wrong: %+v", r)
	}
	if r.NumDifferences != 2 {
		t.Fatalf("NumDifferences=%d want 2", r.NumDifferences)
	}
}

func TestCompareIgnoreHidesSecret(t *testing.T) {
	x, y := makeAccounts()
	ignore := cmpopts.IgnoreFields(account{}, "Password")
	full := cmp.Compare(x, y, cmp.CompareConfig{}, ignore)
	if !full.Completed {
		t.Fatalf("Ignore run reported incomplete: %+v", full)
	}
	for _, d := range full.Differences {
		for _, el := range d.Path {
			if el.Kind == "struct_field" && el.Field == "Password" {
				t.Fatalf("password difference was not ignored")
			}
		}
	}
	js := reportJSON(t, full)
	if strings.Contains(js, "hunter2") || strings.Contains(js, "changed") {
		t.Fatalf("secret leaked despite ignore: %s", js)
	}
}

type floatPair struct {
	A float64
	B float64
}

func TestCompareNaNAndNilEmpty(t *testing.T) {
	// NaN is unequal by default, including inside slices.
	nan := math.NaN()
	if r := cmp.Compare(floatPair{A: nan}, floatPair{A: nan}, cmp.CompareConfig{}); r.Equal || len(r.Differences) != 1 {
		t.Fatalf("NaN must differ by default: %+v", r)
	}
	// A Comparer may equate NaN values; it decides the semantics.
	eqNaN := cmp.Comparer(func(x, y float64) bool { return math.IsNaN(x) && math.IsNaN(y) || x == y })
	if r := cmp.Compare(floatPair{A: nan}, floatPair{A: nan}, cmp.CompareConfig{}, eqNaN); !r.Equal {
		t.Fatalf("NaN comparer was bypassed: %+v", r)
	} else if len(r.Differences) != 0 {
		t.Fatalf("unexpected diffs: %+v", r.Differences)
	}

	// nil and empty-but-non-nil collections differ by default.
	type coll struct {
		S []int
		M map[string]int
	}
	r := cmp.Compare(coll{}, coll{S: []int{}, M: map[string]int{}}, cmp.CompareConfig{})
	if r.Equal || len(r.Differences) != 2 {
		t.Fatalf("nil vs empty collections: %+v", r)
	}
	if r.Differences[0].Kind != cmp.KindModified || r.Differences[1].Kind != cmp.KindModified {
		t.Fatalf("nil/empty must be modified, got %v %v", r.Differences[0].Kind, r.Differences[1].Kind)
	}
	// EquateEmpty keeps them equal through the option engine.
	if r := cmp.Compare(coll{}, coll{S: []int{}, M: map[string]int{}}, cmp.CompareConfig{}, cmpopts.EquateEmpty()); !r.Equal {
		t.Fatalf("EquateEmpty was bypassed: %+v", r)
	}
}

func makeAccounts() (account, account) {
	x := account{
		Name:     "alice",
		Password: "hunter2",
		Tags:     []string{"a", "b"},
		Meta:     map[string]int{"k": 1, "onlyX": 9},
	}
	x.Next = &x
	y := account{
		Name:     "alice!",
		Password: "changed",
		Tags:     []string{"a", "b", "c"},
		Meta:     map[string]int{"k": 2, "onlyY": 7},
	}
	y.Next = &y
	return x, y
}

func reportJSON(t *testing.T, r cmp.CompareReport) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	return string(b)
}

func TestCompareBasicSliceMapCycleSecret(t *testing.T) {
	x, y := makeAccounts()
	r := cmp.Compare(x, y, cmp.CompareConfig{MaxSummaryBytes: 64})
	if r.Equal || !r.Completed || r.EntriesTruncated {
		t.Fatalf("unexpected header: %+v", r)
	}
	js := reportJSON(t, r)
	for _, banned := range []string{"hunter2", "changed", "STRINGER_SENTINEL", "0xc0", "0xC0"} {
		if strings.Contains(js, banned) {
			t.Fatalf("JSON leaked %q: %s", banned, js)
		}
	}
	if !strings.Contains(js, "***redacted***") {
		t.Fatalf("password not redacted: %s", js)
	}

	kinds := map[cmp.DifferenceKind]bool{}
	paths := map[string]bool{}
	var sawSliceAdd, sawMapRemove bool
	for _, d := range r.Differences {
		kinds[d.Kind] = true
		for _, el := range d.Path {
			paths[el.Kind] = true
			if el.Kind == "slice_index" && el.SliceX == nil && el.SliceY != nil {
				sawSliceAdd = true
			}
			if el.Kind == "map_index" {
				sawMapRemove = true
			}
		}
	}
	if !kinds[cmp.KindModified] || !kinds[cmp.KindAdded] || !kinds[cmp.KindRemoved] {
		t.Fatalf("missing kinds: %+v", kinds)
	}
	for _, want := range []string{"struct_field", "slice_index", "map_index", "indirect"} {
		if !paths[want] {
			t.Fatalf("missing path kind %q in %s", want, js)
		}
	}
	if !sawSliceAdd || !sawMapRemove {
		t.Fatalf("slice/map not located: sliceAdd=%v map=%v", sawSliceAdd, sawMapRemove)
	}
	if r.NumRedacted == 0 {
		t.Fatalf("expected redacted values")
	}
}
