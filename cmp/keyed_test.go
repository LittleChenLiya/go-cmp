package cmp_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type keyedItem struct {
	ID    string
	Value int
	Note  string
}

func itemKey(it keyedItem) (string, error) { return it.ID, nil }

func mustDiffKeyed[T any, K comparable](t *testing.T, x, y []T, key func(T) (K, error), opts ...cmp.Option) *cmp.KeyedDiff[T, K] {
	t.Helper()
	d, err := cmp.DiffKeyed(x, y, key, opts...)
	if err != nil {
		t.Fatalf("DiffKeyed returned unexpected error: %v", err)
	}
	if d == nil {
		t.Fatal("DiffKeyed returned nil diff without an error")
	}
	return d
}

// TestDiffKeyedReorder is the acceptance scenario: the old list [a, b, c]
// becomes [c, a', d], where a also has a field modified. The report must
// distinguish the removal of b, the addition of d, the content change of a,
// and the moves of a and c.
func TestDiffKeyedReorder(t *testing.T) {
	oldList := []keyedItem{
		{ID: "a", Value: 1},
		{ID: "b", Value: 2},
		{ID: "c", Value: 3},
	}
	newList := []keyedItem{
		{ID: "c", Value: 3},
		{ID: "a", Value: 10}, // same key as old a, but Value changed
		{ID: "d", Value: 4},
	}

	d := mustDiffKeyed(t, oldList, newList, itemKey)
	if d.Equal {
		t.Error("Equal = true, want false")
	}

	if len(d.Removed) != 1 || d.Removed[0].Index != 1 || d.Removed[0].Value.ID != "b" {
		t.Errorf("Removed = %+v, want b at old index 1", d.Removed)
	}
	if len(d.Added) != 1 || d.Added[0].Index != 2 || d.Added[0].Value.ID != "d" {
		t.Errorf("Added = %+v, want d at new index 2", d.Added)
	}
	if len(d.Changed) != 1 {
		t.Fatalf("Changed = %+v, want exactly one entry for a", d.Changed)
	}
	ch := d.Changed[0]
	if ch.Key != "a" || ch.OldIndex != 0 || ch.NewIndex != 1 {
		t.Errorf("Changed[0] = key %q [%d]->[%d], want key \"a\" [0]->[1]", ch.Key, ch.OldIndex, ch.NewIndex)
	}
	if ch.Old.Value != 1 || ch.New.Value != 10 {
		t.Errorf("Changed[0] values = %+v -> %+v, want Value 1 -> 10", ch.Old, ch.New)
	}
	if !strings.Contains(ch.Diff, "Value") {
		t.Errorf("Changed[0].Diff does not mention the changed field:\n%s", ch.Diff)
	}

	// Both a (0->1) and c (2->0) moved; a is reported as both moved and changed.
	wantMoves := []cmp.KeyedMove[string]{
		{Key: "a", OldIndex: 0, NewIndex: 1},
		{Key: "c", OldIndex: 2, NewIndex: 0},
	}
	if !cmp.Equal(d.Moved, wantMoves) {
		t.Errorf("Moved = %+v, want %+v", d.Moved, wantMoves)
	}
}

// TestDiffKeyedPointers exercises lists of pointer elements.
func TestDiffKeyedPointers(t *testing.T) {
	oldList := []*keyedItem{{ID: "a", Value: 1}, {ID: "b", Value: 2}}
	newList := []*keyedItem{{ID: "b", Value: 2}, {ID: "a", Value: 9}}
	key := func(it *keyedItem) (string, error) { return it.ID, nil }

	d := mustDiffKeyed(t, oldList, newList, key)
	if len(d.Removed) != 0 || len(d.Added) != 0 {
		t.Errorf("Removed/Added = %+v/%+v, want both empty", d.Removed, d.Added)
	}
	if len(d.Changed) != 1 || d.Changed[0].Key != "a" || d.Changed[0].OldIndex != 0 || d.Changed[0].NewIndex != 1 {
		t.Errorf("Changed = %+v, want a [0]->[1]", d.Changed)
	}
	if len(d.Moved) != 2 {
		t.Errorf("Moved = %+v, want moves for a and b", d.Moved)
	}
}

// TestDiffKeyedKeyCalledOnce verifies the key function runs exactly once per
// element.
func TestDiffKeyedKeyCalledOnce(t *testing.T) {
	oldList := []keyedItem{{ID: "a"}, {ID: "b"}}
	newList := []keyedItem{{ID: "b"}, {ID: "c"}, {ID: "d"}}
	total := 0
	calls := map[string]int{}
	key := func(it keyedItem) (string, error) {
		total++
		calls[it.ID]++
		return it.ID, nil
	}
	mustDiffKeyed(t, oldList, newList, key)
	if want := len(oldList) + len(newList); total != want {
		t.Errorf("key called %d times total, want exactly %d (once per element)", total, want)
	}
	// Element "b" appears in both lists; every other element appears once.
	// No element may have its key computed more than once per occurrence.
	for id, n := range calls {
		want := 1
		if id == "b" {
			want = 2
		}
		if n != want {
			t.Errorf("key called %d times for ID %q, want %d", n, id, want)
		}
	}
}

// TestDiffKeyedDuplicateKey verifies that duplicate keys are reported with
// both indices instead of being silently matched to the first element.
func TestDiffKeyedDuplicateKey(t *testing.T) {
	key := func(it keyedItem) (string, error) { return it.ID, nil }

	t.Run("OldList", func(t *testing.T) {
		x := []keyedItem{{ID: "a"}, {ID: "b"}, {ID: "a"}}
		y := []keyedItem{{ID: "a"}, {ID: "b"}}
		d, err := cmp.DiffKeyed(x, y, key)
		if err == nil {
			t.Fatal("DiffKeyed returned nil error for duplicate keys")
		}
		if d != nil {
			t.Errorf("DiffKeyed returned non-nil diff on failure: %+v", d)
		}
		dup, ok := err.(cmp.DuplicateKeyError[string])
		if !ok {
			t.Fatalf("error type = %T, want cmp.DuplicateKeyError[string]", err)
		}
		if dup.Key != "a" || dup.New || dup.First != 0 || dup.Second != 2 {
			t.Errorf("DuplicateKeyError = %+v, want key \"a\" in old list at indices 0 and 2", dup)
		}
	})

	t.Run("NewList", func(t *testing.T) {
		x := []keyedItem{{ID: "a"}}
		y := []keyedItem{{ID: "a"}, {ID: "a"}}
		d, err := cmp.DiffKeyed(x, y, key)
		if err == nil {
			t.Fatal("DiffKeyed returned nil error for duplicate keys")
		}
		if d != nil {
			t.Errorf("DiffKeyed returned non-nil diff on failure: %+v", d)
		}
		dup, ok := err.(cmp.DuplicateKeyError[string])
		if !ok {
			t.Fatalf("error type = %T, want cmp.DuplicateKeyError[string]", err)
		}
		if dup.Key != "a" || !dup.New || dup.First != 0 || dup.Second != 1 {
			t.Errorf("DuplicateKeyError = %+v, want key \"a\" in new list at indices 0 and 1", dup)
		}
	})
}

// TestDiffKeyedKeyError verifies that a failing key function yields an error
// and no partial diff masquerading as a successful result.
func TestDiffKeyedKeyError(t *testing.T) {
	boom := errors.New("boom")
	key := func(it keyedItem) (string, error) {
		if it.ID == "bad" {
			return "", boom
		}
		return it.ID, nil
	}

	t.Run("OldList", func(t *testing.T) {
		d, err := cmp.DiffKeyed([]keyedItem{{ID: "a"}, {ID: "bad"}}, []keyedItem{{ID: "a"}}, key)
		if !errors.Is(err, boom) {
			t.Fatalf("error = %v, want wrapped boom", err)
		}
		if d != nil {
			t.Errorf("DiffKeyed returned non-nil diff on failure: %+v", d)
		}
		var kerr *cmp.KeyError
		if !errors.As(err, &kerr) {
			t.Fatalf("error type = %T, want *cmp.KeyError", err)
		}
		if kerr.New || kerr.Index != 1 {
			t.Errorf("KeyError = %+v, want old list index 1", kerr)
		}
	})

	t.Run("NewList", func(t *testing.T) {
		d, err := cmp.DiffKeyed([]keyedItem{{ID: "a"}}, []keyedItem{{ID: "a"}, {ID: "bad"}}, key)
		if !errors.Is(err, boom) {
			t.Fatalf("error = %v, want wrapped boom", err)
		}
		if d != nil {
			t.Errorf("DiffKeyed returned non-nil diff on failure: %+v", d)
		}
		var kerr *cmp.KeyError
		if !errors.As(err, &kerr) {
			t.Fatalf("error type = %T, want *cmp.KeyError", err)
		}
		if !kerr.New || kerr.Index != 1 {
			t.Errorf("KeyError = %+v, want new list index 1", kerr)
		}
	})
}

// TestDiffKeyedComparer verifies that a custom cmp.Comparer option is honored
// when comparing the content of key-matched elements.
func TestDiffKeyedComparer(t *testing.T) {
	oldList := []keyedItem{{ID: "a", Value: 100}}
	newList := []keyedItem{{ID: "a", Value: 105}}
	within10 := cmp.Comparer(func(x, y int) bool {
		d := x - y
		return d >= -10 && d <= 10
	})

	d := mustDiffKeyed(t, oldList, newList, itemKey, within10)
	if !d.Equal {
		t.Errorf("Equal = false, want true with tolerance comparer; diff:\n%s", d)
	}
	if len(d.Changed) != 0 {
		t.Errorf("Changed = %+v, want empty with tolerance comparer", d.Changed)
	}

	d = mustDiffKeyed(t, oldList, newList, itemKey)
	if d.Equal || len(d.Changed) != 1 {
		t.Errorf("without comparer: Equal = %v, Changed = %+v, want one change", d.Equal, d.Changed)
	}
}

// TestDiffKeyedIgnoreFields verifies that cmpopts.IgnoreFields is honored for
// key-matched content, and that movement does not alter content comparison.
func TestDiffKeyedIgnoreFields(t *testing.T) {
	oldList := []keyedItem{
		{ID: "a", Value: 1, Note: "old"},
		{ID: "b", Value: 2, Note: "keep"},
	}
	newList := []keyedItem{
		{ID: "b", Value: 2, Note: "keep"},
		{ID: "a", Value: 1, Note: "new"}, // moved and Note changed
	}
	ignoreNote := cmpopts.IgnoreFields(keyedItem{}, "Note")

	d := mustDiffKeyed(t, oldList, newList, itemKey, ignoreNote)
	if len(d.Changed) != 0 {
		t.Errorf("Changed = %+v, want empty when Note is ignored", d.Changed)
	}
	// Position changes are still reported even though content compares equal.
	if len(d.Moved) != 2 {
		t.Errorf("Moved = %+v, want moves for a and b", d.Moved)
	}
	if d.Equal {
		t.Error("Equal = true, want false since elements moved")
	}

	d = mustDiffKeyed(t, oldList, newList, itemKey)
	if len(d.Changed) != 1 || d.Changed[0].Key != "a" {
		t.Errorf("without IgnoreFields: Changed = %+v, want one change for a", d.Changed)
	}
}

// TestDiffKeyedNilVsEmpty verifies that nil versus empty list equivalence
// follows the caller-provided options.
func TestDiffKeyedNilVsEmpty(t *testing.T) {
	var nilList []keyedItem
	emptyList := []keyedItem{}

	d := mustDiffKeyed(t, nilList, emptyList, itemKey)
	if d.Equal {
		t.Error("Equal = true for nil vs empty without options, want false")
	}

	d = mustDiffKeyed(t, nilList, emptyList, itemKey, cmpopts.EquateEmpty())
	if !d.Equal {
		t.Error("Equal = false for nil vs empty with EquateEmpty, want true")
	}

	d = mustDiffKeyed(t, nilList, nilList, itemKey)
	if !d.Equal {
		t.Error("Equal = false for nil vs nil, want true")
	}
}

// TestDiffKeyedOrdering verifies that matched elements are reported in
// old-list order and added elements in new-list order, and that the inputs
// are not reordered.
func TestDiffKeyedOrdering(t *testing.T) {
	oldList := []keyedItem{{ID: "z", Value: 1}, {ID: "m", Value: 1}, {ID: "a", Value: 1}}
	newList := []keyedItem{
		{ID: "n2", Value: 1},
		{ID: "a", Value: 2},
		{ID: "n1", Value: 1},
		{ID: "m", Value: 2},
		{ID: "z", Value: 2},
	}
	oldSnapshot := append([]keyedItem(nil), oldList...)
	newSnapshot := append([]keyedItem(nil), newList...)

	d := mustDiffKeyed(t, oldList, newList, itemKey)

	var changedOrder []string
	for _, c := range d.Changed {
		changedOrder = append(changedOrder, c.Key)
	}
	if want := []string{"z", "m", "a"}; !cmp.Equal(changedOrder, want) {
		t.Errorf("changed order = %v, want old-list order %v", changedOrder, want)
	}
	var addedOrder []string
	for _, a := range d.Added {
		addedOrder = append(addedOrder, a.Value.ID)
	}
	if want := []string{"n2", "n1"}; !cmp.Equal(addedOrder, want) {
		t.Errorf("added order = %v, want new-list order %v", addedOrder, want)
	}

	if !cmp.Equal(oldList, oldSnapshot) || !cmp.Equal(newList, newSnapshot) {
		t.Error("DiffKeyed mutated or reordered its input lists")
	}
}

// TestDiffKeyedEqual verifies that identical lists report equality.
func TestDiffKeyedEqual(t *testing.T) {
	list := []keyedItem{{ID: "a", Value: 1}, {ID: "b", Value: 2}}
	d := mustDiffKeyed(t, list, list, itemKey)
	if !d.Equal {
		t.Errorf("Equal = false for identical lists, want true; diff:\n%s", d)
	}
	if got := d.String(); got != "equal\n" {
		t.Errorf("String() = %q, want %q", got, "equal\n")
	}
}
