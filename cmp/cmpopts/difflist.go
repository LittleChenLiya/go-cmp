// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmpopts

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/go-cmp/cmp"
)

// DuplicateKeyError is returned by [DiffList] when the key function produces
// the same key for more than one element of either the old or the new list.
// Keys must be unique within each list so that elements can be matched
// unambiguously; a duplicate key is never silently matched to the first
// element that produced it.
type DuplicateKeyError[K comparable] struct {
	Key     K      // the duplicated key
	InNew   bool   // true if the duplicate occurred in the new list
	Indices [2]int // indices of the two elements that share Key
}

// Error implements the error interface.
func (e DuplicateKeyError[K]) Error() string {
	list := "old"
	if e.InNew {
		list = "new"
	}
	return fmt.Sprintf("cmpopts: duplicate key %v in %s list at indices %d and %d",
		e.Key, list, e.Indices[0], e.Indices[1])
}

// ListEntry describes an element present in only one of the two lists
// compared by [DiffList].
type ListEntry[K comparable, T any] struct {
	Key   K   // key of the element, as produced by the key function
	Index int // index of the element within its own list
	Value T   // the element itself
}

// ListChange describes an element present in both lists whose content
// differs according to the [cmp.Option]s passed to [DiffList].
type ListChange[K comparable, T any] struct {
	Key      K      // key of the element, as produced by the key function
	OldIndex int    // index of the element in the old list
	NewIndex int    // index of the element in the new list
	Old      T      // the element in the old list
	New      T      // the element in the new list
	Diff     string // the output of cmp.Diff(Old, New, opts...)
}

// ListMove describes an element present in both lists whose position
// changed between the old and the new list. A move is reported independently
// of any content change: an element that both moved and was modified appears
// in both [ListDiff.Moved] and [ListDiff.Changed].
type ListMove[K comparable] struct {
	Key      K // key of the element, as produced by the key function
	OldIndex int
	NewIndex int
}

// ListDiff is the result of [DiffList]. Elements present in both lists are
// reported in old-list order, while added elements are reported in new-list
// order. The input lists are never reordered or otherwise modified.
type ListDiff[K comparable, T any] struct {
	// Removed lists elements only present in the old list, in old-list order.
	Removed []ListEntry[K, T]
	// Changed lists elements present in both lists whose content differs,
	// in old-list order.
	Changed []ListChange[K, T]
	// Moved lists elements present in both lists whose index changed,
	// in old-list order.
	Moved []ListMove[K]
	// Added lists elements only present in the new list, in new-list order.
	Added []ListEntry[K, T]

	// emptyDiff is non-empty when both lists are empty but are still
	// unequal according to the provided options (e.g., a nil list versus
	// an empty list without EquateEmpty).
	emptyDiff string
}

// Equal reports whether the two lists are equal: there are no added,
// removed, changed, or moved elements, and two empty lists are considered
// equal according to the provided options.
func (d *ListDiff[K, T]) Equal() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 &&
		len(d.Changed) == 0 && len(d.Moved) == 0 && d.emptyDiff == ""
}

// String returns a human-readable report of the differences. Elements
// present in both lists (removed, changed, and moved entries) are listed in
// old-list order, followed by added elements in new-list order.
func (d *ListDiff[K, T]) String() string {
	if d.Equal() {
		return "lists are equal"
	}
	var b strings.Builder
	b.WriteString("list diff:\n")
	if d.emptyDiff != "" {
		fmt.Fprintf(&b, "  lists differ: %s", d.emptyDiff)
	}
	type line struct {
		oldIndex int
		priority int // orders entries sharing the same old index
		text     string
	}
	var lines []line
	for _, e := range d.Removed {
		lines = append(lines, line{e.Index, 0,
			fmt.Sprintf("  - [old %d] key=%v: removed", e.Index, e.Key)})
	}
	for _, c := range d.Changed {
		diff := strings.TrimSuffix(c.Diff, "\n")
		diff = strings.ReplaceAll(diff, "\n", "\n      ")
		lines = append(lines, line{c.OldIndex, 1,
			fmt.Sprintf("  ~ [old %d -> new %d] key=%v: modified\n      %s",
				c.OldIndex, c.NewIndex, c.Key, diff)})
	}
	for _, m := range d.Moved {
		lines = append(lines, line{m.OldIndex, 2,
			fmt.Sprintf("  > [old %d -> new %d] key=%v: moved",
				m.OldIndex, m.NewIndex, m.Key)})
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].oldIndex != lines[j].oldIndex {
			return lines[i].oldIndex < lines[j].oldIndex
		}
		return lines[i].priority < lines[j].priority
	})
	for _, l := range lines {
		b.WriteString(l.text)
		b.WriteByte('\n')
	}
	for _, e := range d.Added {
		fmt.Fprintf(&b, "  + [new %d] key=%v: added\n", e.Index, e.Key)
	}
	return b.String()
}

// DiffList compares two lists by matching elements through the keys produced
// by keyOf, rather than by position, and reports which elements were added,
// removed, modified, or moved.
//
// The keyOf function is called exactly once per element and must return a
// comparable key that is unique within each list. If keyOf returns an error,
// or if either list contains a duplicate key, DiffList returns a nil *ListDiff
// together with the error; a duplicate key is reported as a
// [DuplicateKeyError] carrying the indices of both colliding elements.
//
// Elements sharing a key are compared for content using cmp.Diff with the
// provided options, so options such as [EquateEmpty], [IgnoreFields], or
// custom [cmp.Comparer] options apply exactly as they would to a direct
// cmp.Diff call. Position changes never affect content comparison: an
// element that only moved is not reported as changed, and an element that
// both moved and changed is reported in both Moved and Changed.
//
// Whether a nil list and an empty list are considered equal is left to the
// provided options (see [EquateEmpty]); the input lists themselves are never
// sorted or otherwise modified.
func DiffList[T any, K comparable](oldList, newList []T, keyOf func(T) (K, error), opts ...cmp.Option) (*ListDiff[K, T], error) {
	if keyOf == nil {
		return nil, errors.New("cmpopts: nil key function")
	}
	oldKeys, err := listKeys(oldList, keyOf, false)
	if err != nil {
		return nil, err
	}
	newKeys, err := listKeys(newList, keyOf, true)
	if err != nil {
		return nil, err
	}

	d := &ListDiff[K, T]{}
	if len(oldList) == 0 && len(newList) == 0 {
		// Both lists are empty; whether they are still unequal (e.g.,
		// nil versus empty) is determined by the caller's options.
		if !cmp.Equal(oldList, newList, opts...) {
			d.emptyDiff = cmp.Diff(oldList, newList, opts...)
		}
		return d, nil
	}

	oldIndexOf := make(map[K]int, len(oldList))
	for i, k := range oldKeys {
		oldIndexOf[k] = i
	}
	newIndexOf := make(map[K]int, len(newList))
	for j, k := range newKeys {
		newIndexOf[k] = j
	}

	for i, old := range oldList {
		k := oldKeys[i]
		j, ok := newIndexOf[k]
		if !ok {
			d.Removed = append(d.Removed, ListEntry[K, T]{Key: k, Index: i, Value: old})
			continue
		}
		if diff := cmp.Diff(old, newList[j], opts...); diff != "" {
			d.Changed = append(d.Changed, ListChange[K, T]{
				Key: k, OldIndex: i, NewIndex: j,
				Old: old, New: newList[j], Diff: diff,
			})
		}
		if i != j {
			d.Moved = append(d.Moved, ListMove[K]{Key: k, OldIndex: i, NewIndex: j})
		}
	}
	for j, nw := range newList {
		k := newKeys[j]
		if _, ok := oldIndexOf[k]; !ok {
			d.Added = append(d.Added, ListEntry[K, T]{Key: k, Index: j, Value: nw})
		}
	}
	return d, nil
}

// listKeys computes the key of every element exactly once and verifies that
// the keys are unique within the list.
func listKeys[T any, K comparable](list []T, keyOf func(T) (K, error), inNew bool) ([]K, error) {
	which := "old"
	if inNew {
		which = "new"
	}
	keys := make([]K, len(list))
	seen := make(map[K]int, len(list))
	for i, v := range list {
		k, err := keyOf(v)
		if err != nil {
			return nil, fmt.Errorf("cmpopts: key function for %s list element %d: %w", which, i, err)
		}
		if j, ok := seen[k]; ok {
			return nil, DuplicateKeyError[K]{Key: k, InNew: inNew, Indices: [2]int{j, i}}
		}
		seen[k] = i
		keys[i] = k
	}
	return keys, nil
}
