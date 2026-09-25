// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmpopts_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type diffListItem struct {
	ID    string
	Value int
}

func diffListKey(it diffListItem) (string, error) { return it.ID, nil }

func TestDiffList_Basic(t *testing.T) {
	oldList := []diffListItem{{ID: "a", Value: 1}, {ID: "b", Value: 2}, {ID: "c", Value: 3}}
	newList := []diffListItem{{ID: "c", Value: 3}, {ID: "a", Value: 42}, {ID: "d", Value: 4}}
	oldCopy := append([]diffListItem(nil), oldList...)
	newCopy := append([]diffListItem(nil), newList...)

	calls := 0
	keyOf := func(it diffListItem) (string, error) {
		calls++
		return it.ID, nil
	}
	d, err := cmpopts.DiffList(oldList, newList, keyOf)
	if err != nil {
		t.Fatalf("DiffList returned unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("DiffList returned nil diff")
	}
	if d.Equal() {
		t.Error("Equal reports true, want false")
	}
	if calls != len(oldList)+len(newList) {
		t.Errorf("key function called %d times, want exactly %d (once per element)", calls, len(oldList)+len(newList))
	}

	// Removed: b at old index 1.
	if len(d.Removed) != 1 || d.Removed[0].Key != "b" || d.Removed[0].Index != 1 {
		t.Errorf("Removed = %+v, want single entry {Key:b Index:1}", d.Removed)
	}
	// Changed: a moved from old index 0 to new index 1 with Value 1 -> 42.
	if len(d.Changed) != 1 {
		t.Fatalf("Changed = %+v, want single entry", d.Changed)
	}
	if c := d.Changed[0]; c.Key != "a" || c.OldIndex != 0 || c.NewIndex != 1 {
		t.Errorf("Changed[0] = %+v, want {Key:a OldIndex:0 NewIndex:1}", c)
	}
	if d.Changed[0].Diff == "" {
		t.Error("Changed[0].Diff is empty, want a content diff")
	}
	// Moved: a (0 -> 1) and c (2 -> 0), in old-list order.
	wantMoved := []cmpopts.ListMove[string]{
		{Key: "a", OldIndex: 0, NewIndex: 1},
		{Key: "c", OldIndex: 2, NewIndex: 0},
	}
	if !reflect.DeepEqual(d.Moved, wantMoved) {
		t.Errorf("Moved = %+v, want %+v", d.Moved, wantMoved)
	}
	// Added: d at new index 2.
	if len(d.Added) != 1 || d.Added[0].Key != "d" || d.Added[0].Index != 2 {
		t.Errorf("Added = %+v, want single entry {Key:d Index:2}", d.Added)
	}

	// The element that both moved and changed must appear in both lists.
	if d.Changed[0].Key != "a" || d.Moved[0].Key != "a" {
		t.Error("element a both moved and changed, want it in both Changed and Moved")
	}

	// Input lists must not be reordered or modified.
	if !reflect.DeepEqual(oldList, oldCopy) {
		t.Errorf("old list modified: got %v, want %v", oldList, oldCopy)
	}
	if !reflect.DeepEqual(newList, newCopy) {
		t.Errorf("new list modified: got %v, want %v", newList, newCopy)
	}
}

func TestDiffList_Pointers(t *testing.T) {
	oldList := []*diffListItem{{ID: "a", Value: 1}, {ID: "b", Value: 2}}
	newList := []*diffListItem{{ID: "b", Value: 2}, {ID: "a", Value: 9}}
	keyOf := func(it *diffListItem) (string, error) { return it.ID, nil }

	d, err := cmpopts.DiffList(oldList, newList, keyOf)
	if err != nil {
		t.Fatalf("DiffList returned unexpected error: %v", err)
	}
	if len(d.Removed) != 0 || len(d.Added) != 0 {
		t.Errorf("Removed = %+v, Added = %+v, want both empty", d.Removed, d.Added)
	}
	if len(d.Changed) != 1 || d.Changed[0].Key != "a" || d.Changed[0].OldIndex != 0 || d.Changed[0].NewIndex != 1 {
		t.Errorf("Changed = %+v, want single entry {Key:a OldIndex:0 NewIndex:1}", d.Changed)
	}
	wantMoved := []cmpopts.ListMove[string]{
		{Key: "a", OldIndex: 0, NewIndex: 1},
		{Key: "b", OldIndex: 1, NewIndex: 0},
	}
	if !reflect.DeepEqual(d.Moved, wantMoved) {
		t.Errorf("Moved = %+v, want %+v", d.Moved, wantMoved)
	}
}

func TestDiffList_DuplicateKeys(t *testing.T) {
	tests := []struct {
		name        string
		oldList     []diffListItem
		newList     []diffListItem
		wantInNew   bool
		wantIndices [2]int
	}{{
		name:        "old list",
		oldList:     []diffListItem{{ID: "a"}, {ID: "b"}, {ID: "a"}},
		newList:     []diffListItem{{ID: "a"}, {ID: "b"}},
		wantInNew:   false,
		wantIndices: [2]int{0, 2},
	}, {
		name:        "new list",
		oldList:     []diffListItem{{ID: "a"}, {ID: "b"}},
		newList:     []diffListItem{{ID: "b"}, {ID: "a"}, {ID: "b"}},
		wantInNew:   true,
		wantIndices: [2]int{0, 2},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := cmpopts.DiffList(tt.oldList, tt.newList, diffListKey)
			if err == nil {
				t.Fatal("DiffList returned nil error, want DuplicateKeyError")
			}
			if d != nil {
				t.Errorf("DiffList returned non-nil diff on failure: %+v", d)
			}
			var dupErr cmpopts.DuplicateKeyError[string]
			if !errors.As(err, &dupErr) {
				t.Fatalf("error = %v (%T), want DuplicateKeyError", err, err)
			}
			if dupErr.InNew != tt.wantInNew {
				t.Errorf("DuplicateKeyError.InNew = %v, want %v", dupErr.InNew, tt.wantInNew)
			}
			if dupErr.Indices != tt.wantIndices {
				t.Errorf("DuplicateKeyError.Indices = %v, want %v", dupErr.Indices, tt.wantIndices)
			}
		})
	}
}

func TestDiffList_CustomComparer(t *testing.T) {
	oldList := []diffListItem{{ID: "a", Value: 100}, {ID: "b", Value: 2}}
	newList := []diffListItem{{ID: "b", Value: 2}, {ID: "a", Value: 105}}
	// Treat Value as equal when within a margin of 10.
	withinMargin := cmp.Comparer(func(x, y int) bool {
		d := x - y
		return d >= -10 && d <= 10
	})
	d, err := cmpopts.DiffList(oldList, newList, diffListKey, withinMargin)
	if err != nil {
		t.Fatalf("DiffList returned unexpected error: %v", err)
	}
	if len(d.Changed) != 0 {
		t.Errorf("Changed = %+v, want empty (custom comparer equates values)", d.Changed)
	}
	// Moves are still reported; content rules are unaffected by position.
	wantMoved := []cmpopts.ListMove[string]{
		{Key: "a", OldIndex: 0, NewIndex: 1},
		{Key: "b", OldIndex: 1, NewIndex: 0},
	}
	if !reflect.DeepEqual(d.Moved, wantMoved) {
		t.Errorf("Moved = %+v, want %+v", d.Moved, wantMoved)
	}

	// Without the comparer, the same lists report a content change.
	d2, err := cmpopts.DiffList(oldList, newList, diffListKey)
	if err != nil {
		t.Fatalf("DiffList returned unexpected error: %v", err)
	}
	if len(d2.Changed) != 1 || d2.Changed[0].Key != "a" {
		t.Errorf("Changed = %+v, want single entry for key a", d2.Changed)
	}
}

func TestDiffList_IgnoreFields(t *testing.T) {
	oldList := []diffListItem{{ID: "a", Value: 1}, {ID: "b", Value: 2}}
	newList := []diffListItem{{ID: "b", Value: 2}, {ID: "a", Value: 99}}
	d, err := cmpopts.DiffList(oldList, newList, diffListKey,
		cmpopts.IgnoreFields(diffListItem{}, "Value"))
	if err != nil {
		t.Fatalf("DiffList returned unexpected error: %v", err)
	}
	if len(d.Changed) != 0 {
		t.Errorf("Changed = %+v, want empty (Value ignored)", d.Changed)
	}
	if len(d.Moved) != 2 {
		t.Errorf("Moved = %+v, want two moves", d.Moved)
	}
}

func TestDiffList_KeyError(t *testing.T) {
	keyErr := errors.New("no key")
	oldList := []diffListItem{{ID: "a"}, {ID: "b"}}
	newList := []diffListItem{{ID: "a"}, {ID: "b"}}
	calls := 0
	keyOf := func(it diffListItem) (string, error) {
		calls++
		if it.ID == "b" {
			return "", keyErr
		}
		return it.ID, nil
	}
	d, err := cmpopts.DiffList(oldList, newList, keyOf)
	if err == nil {
		t.Fatal("DiffList returned nil error, want key function error")
	}
	if d != nil {
		t.Errorf("DiffList returned non-nil diff on failure: %+v", d)
	}
	if !errors.Is(err, keyErr) {
		t.Errorf("error = %v, want it to wrap %v", err, keyErr)
	}
	// The old list is keyed first; the failure aborts before any diff.
	if calls != 2 {
		t.Errorf("key function called %d times, want 2 (aborts on first error)", calls)
	}
}

func TestDiffList_NilKeyFunc(t *testing.T) {
	d, err := cmpopts.DiffList[diffListItem, string]([]diffListItem{{ID: "a"}}, []diffListItem{{ID: "a"}}, nil)
	if err == nil {
		t.Fatal("DiffList returned nil error, want error for nil key function")
	}
	if d != nil {
		t.Errorf("DiffList returned non-nil diff on failure: %+v", d)
	}
}

func TestDiffList_NilVsEmpty(t *testing.T) {
	var nilList []diffListItem
	emptyList := []diffListItem{}

	// Without EquateEmpty, nil and empty lists differ.
	d, err := cmpopts.DiffList(nilList, emptyList, diffListKey)
	if err != nil {
		t.Fatalf("DiffList returned unexpected error: %v", err)
	}
	if d.Equal() {
		t.Error("Equal reports true for nil vs empty without EquateEmpty, want false")
	}
	if len(d.Added) != 0 || len(d.Removed) != 0 || len(d.Changed) != 0 || len(d.Moved) != 0 {
		t.Errorf("element-level entries = %+v, want none", d)
	}

	// With EquateEmpty, nil and empty lists are equal.
	d, err = cmpopts.DiffList(nilList, emptyList, diffListKey, cmpopts.EquateEmpty())
	if err != nil {
		t.Fatalf("DiffList returned unexpected error: %v", err)
	}
	if !d.Equal() {
		t.Errorf("Equal reports false for nil vs empty with EquateEmpty, want true; diff:\n%s", d)
	}
}

func TestDiffList_Equal(t *testing.T) {
	list := []diffListItem{{ID: "a", Value: 1}, {ID: "b", Value: 2}}
	d, err := cmpopts.DiffList(list, list, diffListKey)
	if err != nil {
		t.Fatalf("DiffList returned unexpected error: %v", err)
	}
	if !d.Equal() {
		t.Errorf("Equal reports false for identical lists, want true; diff:\n%s", d)
	}
	if got := d.String(); got != "lists are equal" {
		t.Errorf("String = %q, want %q", got, "lists are equal")
	}
}

func TestDiffList_StringOrder(t *testing.T) {
	oldList := []diffListItem{{ID: "a", Value: 1}, {ID: "b", Value: 2}, {ID: "c", Value: 3}}
	newList := []diffListItem{{ID: "c", Value: 3}, {ID: "a", Value: 42}, {ID: "d", Value: 4}}
	d, err := cmpopts.DiffList(oldList, newList, diffListKey)
	if err != nil {
		t.Fatalf("DiffList returned unexpected error: %v", err)
	}
	got := d.String()
	// Existing elements appear in old-list order (a, b, c),
	// followed by added elements in new-list order (d).
	order := []string{"key=a", "key=b", "key=c", "key=d"}
	prev := -1
	for _, want := range order {
		idx := strings.Index(got, want)
		if idx < 0 {
			t.Fatalf("String output missing %q:\n%s", want, got)
		}
		if idx < prev {
			t.Fatalf("String output out of order at %q:\n%s", want, got)
		}
		prev = idx
	}
	for _, want := range []string{"removed", "modified", "moved", "added"} {
		if !strings.Contains(got, want) {
			t.Errorf("String output missing %q:\n%s", want, got)
		}
	}
}
