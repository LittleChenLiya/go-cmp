package cmp

import (
	"fmt"
	"strings"
)

// KeyedEntry is an element of a list paired with the index it appeared at.
type KeyedEntry[T any] struct {
	Index int
	Value T
}

// KeyedChange describes an element that is present in both lists (matched by
// key) but whose content differs according to the provided cmp.Options.
type KeyedChange[T any, K comparable] struct {
	Key      K
	OldIndex int    // index of the element in the old list
	NewIndex int    // index of the element in the new list
	Old      T      // element from the old list
	New      T      // element from the new list
	Diff     string // result of Diff(Old, New, opts...)
}

// KeyedMove describes an element that is present in both lists (matched by
// key) but appears at a different index.
type KeyedMove[K comparable] struct {
	Key      K
	OldIndex int // index of the element in the old list
	NewIndex int // index of the element in the new list
}

// KeyedDiff is the result of DiffKeyed. Elements that exist in both lists
// (Removed, Changed, and Moved) are reported in the order of the old list,
// while Added elements are reported in the order of the new list. An element
// that both moved and changed content appears in both Changed and Moved.
//
// The input lists are never modified or sorted.
type KeyedDiff[T any, K comparable] struct {
	// Removed lists elements present only in the old list, in old-list order.
	Removed []KeyedEntry[T]
	// Added lists elements present only in the new list, in new-list order.
	Added []KeyedEntry[T]
	// Changed lists elements matched by key whose content differs,
	// in old-list order.
	Changed []KeyedChange[T, K]
	// Moved lists elements matched by key whose index changed,
	// in old-list order.
	Moved []KeyedMove[K]
	// Equal reports whether the two lists are considered equal.
	// It is false if any of the above slices is non-empty. When both lists
	// are empty, whether a nil list equals an empty (non-nil) list is
	// determined by the caller-provided options (e.g., an option such as
	// cmpopts.EquateEmpty makes them equal).
	Equal bool
}

// String returns a human-readable report of the keyed difference.
func (d *KeyedDiff[T, K]) String() string {
	if d.Equal {
		return "equal\n"
	}
	var b strings.Builder
	for _, e := range d.Removed {
		fmt.Fprintf(&b, "removed: [%d] %v\n", e.Index, e.Value)
	}
	for _, c := range d.Changed {
		fmt.Fprintf(&b, "changed: key=%v [%d]->[%d]\n%s", c.Key, c.OldIndex, c.NewIndex, indent(c.Diff))
	}
	for _, m := range d.Moved {
		fmt.Fprintf(&b, "moved:   key=%v [%d]->[%d]\n", m.Key, m.OldIndex, m.NewIndex)
	}
	for _, e := range d.Added {
		fmt.Fprintf(&b, "added:   [%d] %v\n", e.Index, e.Value)
	}
	return b.String()
}

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = "\t" + ln
	}
	return strings.Join(lines, "\n") + "\n"
}

// DuplicateKeyError is returned by DiffKeyed when the key function produces
// the same key for two elements of the same list. First and Second are the
// indices of both offending elements; duplicates are never silently matched
// to the first occurrence.
type DuplicateKeyError[K comparable] struct {
	Key    K
	New    bool // true if the duplicate occurred in the new (second) list
	First  int  // index of the first element with this key
	Second int  // index of the second element with this key
}

// Error implements the error interface.
func (e DuplicateKeyError[K]) Error() string {
	list := "old"
	if e.New {
		list = "new"
	}
	return fmt.Sprintf("cmp: duplicate key %v in %s list at indices %d and %d", e.Key, list, e.First, e.Second)
}

// KeyError is returned by DiffKeyed when the key function itself reports an
// error for some element.
type KeyError struct {
	New   bool // true if the error occurred while processing the new (second) list
	Index int  // index of the element whose key could not be computed
	Err   error
}

// Error implements the error interface.
func (e *KeyError) Error() string {
	list := "old"
	if e.New {
		list = "new"
	}
	return fmt.Sprintf("cmp: key function failed for %s list element %d: %v", list, e.Index, e.Err)
}

// Unwrap returns the underlying error reported by the key function.
func (e *KeyError) Unwrap() error { return e.Err }

// DiffKeyed compares two lists by matching elements through a key function
// rather than by position, so that elements that merely changed position can
// be distinguished from elements whose content changed.
//
// The key function is invoked exactly once per element and must return a
// comparable key. Keys must be unique within each list; if a key occurs
// twice in the same list, a DuplicateKeyError reporting both indices is
// returned. If the key function reports an error, a KeyError is returned.
// In either failure case the returned diff is nil; a partial diff is never
// returned as if it were successful.
//
// Elements sharing a key are compared for content with Diff using the
// provided options, so custom comparers, ignore rules, and similar options
// apply exactly as they would to a direct comparison; an element's position
// never affects how its content is compared. Whether a nil list is
// equivalent to an empty list is likewise governed by the provided options
// (see the Equal field).
//
// The result reports removed, added, changed, and moved elements separately,
// preserving both the old and new indices. Entries for elements present in
// both lists appear in old-list order, followed by added elements in
// new-list order. The input lists are not modified or sorted.
func DiffKeyed[T any, K comparable](x, y []T, key func(T) (K, error), opts ...Option) (*KeyedDiff[T, K], error) {
	xkeys := make([]K, len(x))
	for i, v := range x {
		k, err := key(v)
		if err != nil {
			return nil, &KeyError{Index: i, Err: err}
		}
		xkeys[i] = k
	}
	ykeys := make([]K, len(y))
	for j, v := range y {
		k, err := key(v)
		if err != nil {
			return nil, &KeyError{New: true, Index: j, Err: err}
		}
		ykeys[j] = k
	}

	xidx, err := keyIndex(xkeys, false)
	if err != nil {
		return nil, err
	}
	yidx, err := keyIndex(ykeys, true)
	if err != nil {
		return nil, err
	}

	d := &KeyedDiff[T, K]{}
	for i, v := range x {
		j, ok := yidx[xkeys[i]]
		if !ok {
			d.Removed = append(d.Removed, KeyedEntry[T]{Index: i, Value: v})
			continue
		}
		if diff := Diff(v, y[j], opts...); diff != "" {
			d.Changed = append(d.Changed, KeyedChange[T, K]{
				Key: xkeys[i], OldIndex: i, NewIndex: j,
				Old: v, New: y[j], Diff: diff,
			})
		}
		if i != j {
			d.Moved = append(d.Moved, KeyedMove[K]{Key: xkeys[i], OldIndex: i, NewIndex: j})
		}
	}
	for j, v := range y {
		if _, ok := xidx[ykeys[j]]; !ok {
			d.Added = append(d.Added, KeyedEntry[T]{Index: j, Value: v})
		}
	}

	d.Equal = len(d.Removed) == 0 && len(d.Added) == 0 &&
		len(d.Changed) == 0 && len(d.Moved) == 0 &&
		Equal(x, y, opts...)
	return d, nil
}

func keyIndex[K comparable](keys []K, isNew bool) (map[K]int, error) {
	m := make(map[K]int, len(keys))
	for i, k := range keys {
		if first, ok := m[k]; ok {
			return nil, DuplicateKeyError[K]{Key: k, New: isNew, First: first, Second: i}
		}
		m[k] = i
	}
	return m, nil
}
