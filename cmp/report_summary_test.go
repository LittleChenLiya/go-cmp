// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type sideEffectStringer struct {
	Calls *int
}

func (s sideEffectStringer) String() string {
	*s.Calls++
	return "side-effect"
}

type secretValue struct {
	Secret string
}

func (s secretValue) String() string {
	return s.Secret
}

func summarize(t *testing.T, x, y any, opts ...cmp.Option) *cmp.Summary {
	t.Helper()
	r := cmp.NewSummaryReporter()
	cmp.Equal(x, y, append(opts, cmp.Reporter(r))...)
	s, err := r.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}
	return s
}

func findGroup(s *cmp.Summary, path string) *cmp.SummaryGroup {
	for _, g := range s.Groups {
		if g.Path == path {
			return g
		}
	}
	return nil
}

func TestSummaryReporter_ThreeDiffsOneGroup(t *testing.T) {
	type S struct{ V int }
	type T struct{ Items []S }
	x := T{Items: []S{{1}, {2}, {3}}}
	y := T{Items: []S{{9}, {8}, {7}}}

	r := cmp.NewSummaryReporter(cmp.WithMaxExamples(1))
	cmp.Equal(x, y, cmp.Reporter(r))
	s, err := r.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}
	if len(s.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(s.Groups))
	}
	g := s.Groups[0]
	if g.Path != "Items.V" {
		t.Errorf("group path = %q, want %q", g.Path, "Items.V")
	}
	if g.NumReplaced != 3 {
		t.Errorf("NumReplaced = %d, want 3", g.NumReplaced)
	}
	if len(g.Examples) != 1 {
		t.Errorf("len(Examples) = %d, want 1", len(g.Examples))
	}
}

func TestSummaryReporter_TwoGroups(t *testing.T) {
	type S struct{ A, B int }
	x := S{A: 1, B: 2}
	y := S{A: 9, B: 8}

	s := summarize(t, x, y)
	if len(s.Groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(s.Groups))
	}
	for _, path := range []string{"A", "B"} {
		g := findGroup(s, path)
		if g == nil {
			t.Errorf("missing group %q", path)
			continue
		}
		if g.NumReplaced != 1 {
			t.Errorf("group %q NumReplaced = %d, want 1", path, g.NumReplaced)
		}
	}
}

func TestSummaryReporter_NoStringerCalls(t *testing.T) {
	calls := 0
	type Box struct{ Elems []sideEffectStringer }
	xb := Box{Elems: []sideEffectStringer{{&calls}, {&calls}}}
	yb := Box{Elems: []sideEffectStringer{{&calls}}}

	r := cmp.NewSummaryReporter()
	cmp.Equal(xb, yb, cmp.Reporter(r))
	if calls != 0 {
		t.Errorf("Stringer calls = %d, want 0", calls)
	}
	s, err := r.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}
	if len(s.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(s.Groups))
	}
}

func TestSummaryReporter_TypeNoLeak(t *testing.T) {
	type S struct{ V secretValue }
	x := S{V: secretValue{Secret: "hunter2"}}
	y := S{V: secretValue{Secret: "hunter3"}}

	s := summarize(t, x, y)
	g := findGroup(s, "V.Secret")
	if g == nil {
		t.Fatal("missing group V.Secret")
	}
	if len(g.Examples) != 1 {
		t.Fatalf("len(Examples) = %d, want 1", len(g.Examples))
	}
	typ := g.Examples[0].Type
	if typ == nil {
		t.Fatal("example Type is nil")
	}
	ts := typ.String()
	if strings.Contains(ts, "hunter") {
		t.Errorf("type string %q leaks value", ts)
	}
	if ts != "string" {
		t.Errorf("type string = %q, want %q", ts, "string")
	}
}

func TestSummaryReporter_SlicePairing(t *testing.T) {
	type S struct{ A, B int }
	type T struct{ Items []S }
	// {3,4} deleted, {7,8} inserted, {5,6} replaced by {5,7}.
	x := T{Items: []S{{1, 2}, {3, 4}, {5, 6}}}
	y := T{Items: []S{{1, 2}, {5, 7}, {7, 8}}}

	r := cmp.NewSummaryReporter(cmp.WithMaxExamples(10))
	cmp.Equal(x, y, cmp.Reporter(r))
	s, err := r.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}
	// Deletion and insertion are at the slice level (group "Items").
	g := findGroup(s, "Items")
	if g == nil {
		t.Fatal("missing group Items")
	}
	got := map[string]bool{}
	for _, ex := range g.Examples {
		got[ex.Path] = true
	}
	var hasDeletion, hasInsertion bool
	for path := range got {
		if strings.Contains(path, "->?") {
			hasDeletion = true
		}
		if strings.Contains(path, "?->") {
			hasInsertion = true
		}
	}
	if !hasDeletion {
		t.Errorf("no deletion path in %v", got)
	}
	if !hasInsertion {
		t.Errorf("no insertion path in %v", got)
	}
}

func TestSummaryReporter_DeletionInsertionReplacement(t *testing.T) {
	type S struct{ A, B int }
	type T struct{ Items []S }
	// {3,4} deleted, {7,8} inserted, {5,6} replaced by {5,7}.
	x := T{Items: []S{{1, 2}, {3, 4}, {5, 6}}}
	y := T{Items: []S{{1, 2}, {5, 7}, {7, 8}}}

	r := cmp.NewSummaryReporter(cmp.WithMaxExamples(10))
	cmp.Equal(x, y, cmp.Reporter(r))
	s, err := r.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}
	// Deletion and insertion are at the slice level (group "Items").
	g := findGroup(s, "Items")
	if g == nil {
		t.Fatal("missing group Items")
	}
	if g.NumDeleted != 1 {
		t.Errorf("NumDeleted = %d, want 1", g.NumDeleted)
	}
	if g.NumInserted != 1 {
		t.Errorf("NumInserted = %d, want 1", g.NumInserted)
	}
	// Replacement is at the field level (group "Items.A" or "Items.B").
	var replaced int
	for _, grp := range s.Groups {
		replaced += grp.NumReplaced
	}
	if replaced != 1 {
		t.Errorf("total NumReplaced = %d, want 1", replaced)
	}
}

func TestSummaryReporter_Overflow(t *testing.T) {
	type S struct{ A, B int }
	x := S{A: 1, B: 2}
	y := S{A: 9, B: 8}

	r := cmp.NewSummaryReporter(cmp.WithMaxGroups(1))
	cmp.Equal(x, y, cmp.Reporter(r))
	_, err := r.Summary()
	if err == nil {
		t.Fatal("Summary() error = nil, want overflow error")
	}
	if !strings.Contains(err.Error(), "overflow") {
		t.Errorf("error = %q, want overflow", err.Error())
	}
}

func TestSummaryReporter_ExamplesBounded(t *testing.T) {
	type S struct{ V int }
	type T struct{ Items []S }
	n := 5
	xs := make([]S, n)
	ys := make([]S, n)
	for i := range xs {
		xs[i] = S{V: i}
		ys[i] = S{V: i + 100}
	}
	x := T{Items: xs}
	y := T{Items: ys}

	r := cmp.NewSummaryReporter(cmp.WithMaxExamples(2))
	cmp.Equal(x, y, cmp.Reporter(r))
	s, err := r.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}
	g := findGroup(s, "Items.V")
	if g == nil {
		t.Fatal("missing group Items.V")
	}
	if len(g.Examples) != 2 {
		t.Errorf("len(Examples) = %d, want 2", len(g.Examples))
	}
	total := g.NumDeleted + g.NumInserted + g.NumReplaced
	if total != n {
		t.Errorf("total count = %d, want %d", total, n)
	}
}

func TestSummaryReporter_MapNoLeak(t *testing.T) {
	type S struct{ V int }
	x := map[string]S{"secret-key": {1}}
	y := map[string]S{"secret-key": {2}}

	r := cmp.NewSummaryReporter()
	cmp.Equal(x, y, cmp.Reporter(r))
	s, err := r.Summary()
	if err != nil {
		t.Fatalf("Summary() error: %v", err)
	}
	for _, g := range s.Groups {
		for _, ex := range g.Examples {
			if strings.Contains(ex.Path, "secret-key") {
				t.Errorf("path %q leaks map key", ex.Path)
			}
		}
	}
}

func TestSummaryReporter_Empty(t *testing.T) {
	type S struct{ V int }
	x := S{V: 1}
	y := S{V: 1}

	s := summarize(t, x, y)
	if len(s.Groups) != 0 {
		t.Errorf("got %d groups, want 0", len(s.Groups))
	}
}

func TestSummaryReporter_CountTypes(t *testing.T) {
	g := cmp.SummaryGroup{}
	_ = g.NumDeleted
	_ = g.NumInserted
	_ = g.NumReplaced
}

func TestSummaryReporter_NewDefaults(t *testing.T) {
	r := cmp.NewSummaryReporter()
	if r == nil {
		t.Fatal("NewSummaryReporter returned nil")
	}
}

func TestSummaryReporter_ImplementsReporter(t *testing.T) {
	var _ interface {
		PushStep(cmp.PathStep)
		Report(cmp.Result)
		PopStep()
	} = cmp.NewSummaryReporter()
}

