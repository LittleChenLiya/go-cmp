// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"errors"
	"math"
	"strings"
	"testing"
)

type summaryOuter struct {
	List []int
	Name string
}

// Three differences in the same group with an example cap of one must yield
// a count of three and exactly one example path, while a second group is
// counted independently.
func TestSummarizerGroupsAndCapsExamples(t *testing.T) {
	x := summaryOuter{List: []int{1, 2, 3}, Name: "x"}
	y := summaryOuter{List: []int{7, 8, 9}, Name: "y"}

	s := NewSummarizer(1)
	if Equal(x, y, Reporter(s)) {
		t.Fatal("Equal reported true, want false")
	}
	sum, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary returned error: %v", err)
	}
	if got, want := len(sum.Groups), 2; got != want {
		t.Fatalf("len(Groups) = %d, want %d (groups: %v)", got, want, sum.Groups)
	}
	if got, want := sum.Total(), uint64(4); got != want {
		t.Errorf("Total() = %d, want %d", got, want)
	}
	if got, want := sum.Replaces, uint64(4); got != want {
		t.Errorf("Replaces = %d, want %d", got, want)
	}

	byPath := make(map[string]DiffGroup)
	for _, g := range sum.Groups {
		byPath[g.Path] = g
	}
	list, ok := byPath["{cmp.summaryOuter}.List[]"]
	if !ok {
		t.Fatalf("missing group for List, got groups: %v", sum.Groups)
	}
	if got, want := list.Replaces, uint64(3); got != want {
		t.Errorf("List group Replaces = %d, want %d", got, want)
	}
	if got, want := list.Total(), uint64(3); got != want {
		t.Errorf("List group Total() = %d, want %d", got, want)
	}
	if got, want := len(list.Examples), 1; got != want {
		t.Fatalf("List group examples = %d, want %d (capped)", got, want)
	}
	ex := list.Examples[0]
	if want := "{cmp.summaryOuter}.List[0]"; ex.Path != want {
		t.Errorf("example Path = %q, want %q", ex.Path, want)
	}
	if ex.Type != "int" {
		t.Errorf("example Type = %q, want %q", ex.Type, "int")
	}
	if ex.Kind != Replace {
		t.Errorf("example Kind = %v, want %v", ex.Kind, Replace)
	}

	name, ok := byPath["{cmp.summaryOuter}.Name"]
	if !ok {
		t.Fatalf("missing group for Name, got groups: %v", sum.Groups)
	}
	if got, want := name.Replaces, uint64(1); got != want {
		t.Errorf("Name group Replaces = %d, want %d", got, want)
	}
	if got, want := len(name.Examples), 1; got != want {
		t.Errorf("Name group examples = %d, want %d", got, want)
	}
}

// Slice elements missing on one side must be paired through the split keys
// and counted as deletions or insertions, not replacements.
func TestSummarizerSlicePairing(t *testing.T) {
	s := NewSummarizer(4)
	Equal([]int{1, 2, 3}, []int{1}, Reporter(s))
	sum, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary returned error: %v", err)
	}
	if got, want := sum.Deletes, uint64(2); got != want {
		t.Errorf("Deletes = %d, want %d", got, want)
	}
	if got := sum.Inserts; got != 0 {
		t.Errorf("Inserts = %d, want 0", got)
	}
	if got := sum.Replaces; got != 0 {
		t.Errorf("Replaces = %d, want 0", got)
	}
	if got, want := len(sum.Groups), 1; got != want {
		t.Fatalf("len(Groups) = %d, want %d", got, want)
	}
	g := sum.Groups[0]
	if want := "{[]int}[]"; g.Path != want {
		t.Errorf("group Path = %q, want %q", g.Path, want)
	}
	var paths []string
	for _, ex := range g.Examples {
		paths = append(paths, ex.Path)
		if ex.Kind != Delete {
			t.Errorf("example Kind = %v, want %v", ex.Kind, Delete)
		}
	}
	wantPaths := []string{"{[]int}[1->?]", "{[]int}[2->?]"}
	if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
		t.Errorf("example paths = %v, want %v", paths, wantPaths)
	}

	s = NewSummarizer(4)
	Equal([]int{1}, []int{1, 2, 3}, Reporter(s))
	sum, err = s.Summary()
	if err != nil {
		t.Fatalf("Summary returned error: %v", err)
	}
	if got, want := sum.Inserts, uint64(2); got != want {
		t.Errorf("Inserts = %d, want %d", got, want)
	}
	if got := sum.Deletes; got != 0 {
		t.Errorf("Deletes = %d, want 0", got)
	}
	paths = paths[:0]
	for _, ex := range sum.Groups[0].Examples {
		paths = append(paths, ex.Path)
		if ex.Kind != Insert {
			t.Errorf("example Kind = %v, want %v", ex.Kind, Insert)
		}
	}
	wantPaths = []string{"{[]int}[?->1]", "{[]int}[?->2]"}
	if strings.Join(paths, ",") != strings.Join(wantPaths, ",") {
		t.Errorf("example paths = %v, want %v", paths, wantPaths)
	}
}

// stringerSpy is a fmt.Stringer whose String method has a side effect.
// The summarizer must never call it, and the type summary must not leak
// the values it wraps.
type stringerSpy struct {
	Calls *int
	V     int
}

func (s stringerSpy) String() string {
	*s.Calls++
	return "stringerSpy-called"
}

type stringerHolder struct {
	Spy stringerSpy
}

func TestSummarizerNeverCallsString(t *testing.T) {
	calls := 0

	// Difference inside a struct field of a Stringer type.
	x := stringerHolder{Spy: stringerSpy{Calls: &calls, V: 1}}
	y := stringerHolder{Spy: stringerSpy{Calls: &calls, V: 2}}
	s := NewSummarizer(2)
	Equal(x, y, Reporter(s))
	sum, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary returned error: %v", err)
	}
	if got, want := sum.Replaces, uint64(1); got != want {
		t.Errorf("Replaces = %d, want %d", got, want)
	}

	// Difference in a map entry whose key is a Stringer type.
	mx := map[stringerSpy]int{{Calls: &calls, V: 1}: 10}
	my := map[stringerSpy]int{{Calls: &calls, V: 1}: 20}
	s = NewSummarizer(2)
	Equal(mx, my, Reporter(s))
	sum, err = s.Summary()
	if err != nil {
		t.Fatalf("Summary returned error: %v", err)
	}
	if got, want := sum.Replaces, uint64(1); got != want {
		t.Errorf("Replaces = %d, want %d", got, want)
	}
	if got, want := len(sum.Groups), 1; got != want {
		t.Fatalf("len(Groups) = %d, want %d", got, want)
	}
	g := sum.Groups[0]
	// The group path renders the map key as its type only.
	if want := "{map[cmp.stringerSpy]int}[cmp.stringerSpy]"; g.Path != want {
		t.Errorf("group Path = %q, want %q", g.Path, want)
	}
	// The type summary must not leak any map key or entry values.
	for _, leaked := range []string{"10", "20", "stringerSpy-called"} {
		if strings.Contains(g.Path, leaked) {
			t.Errorf("group Path %q leaks value %q", g.Path, leaked)
		}
	}
	for _, ex := range g.Examples {
		for _, leaked := range []string{"10", "20", "stringerSpy-called"} {
			if strings.Contains(ex.Path, leaked) || strings.Contains(ex.Type, leaked) {
				t.Errorf("example %+v leaks value %q", ex, leaked)
			}
		}
	}

	if calls != 0 {
		t.Errorf("String called %d times, want 0", calls)
	}
}

// A counter that would overflow must surface an explicit error instead of
// returning a misleading, wrapped-around "complete" summary.
func TestSummarizerOverflow(t *testing.T) {
	s := NewSummarizer(1)
	Equal(struct{ A int }{1}, struct{ A int }{2}, Reporter(s))

	// Drive the group counter to the brink of overflow.
	var g *diffGroup
	for _, g = range s.groups {
		g.replaces = math.MaxUint64
	}
	if g == nil {
		t.Fatal("no group recorded")
	}

	// One more reported difference in the same group overflows the counter.
	Equal(struct{ A int }{1}, struct{ A int }{3}, Reporter(s))

	sum, err := s.Summary()
	if !errors.Is(err, ErrCountOverflow) {
		t.Fatalf("Summary error = %v, want %v", err, ErrCountOverflow)
	}
	if len(sum.Groups) != 0 || sum.Total() != 0 {
		t.Errorf("Summary = %+v, want zero summary on overflow", sum)
	}
}

// Equal inputs must produce an empty summary.
func TestSummarizerEqual(t *testing.T) {
	s := NewSummarizer(1)
	if !Equal(summaryOuter{List: []int{1}, Name: "a"}, summaryOuter{List: []int{1}, Name: "a"}, Reporter(s)) {
		t.Fatal("Equal reported false, want true")
	}
	sum, err := s.Summary()
	if err != nil {
		t.Fatalf("Summary returned error: %v", err)
	}
	if len(sum.Groups) != 0 || sum.Total() != 0 {
		t.Errorf("Summary = %+v, want empty", sum)
	}
}
