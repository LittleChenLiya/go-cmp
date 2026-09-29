// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

// Status is the outcome of a budgeted comparison performed by [Compare].
type Status int

const (
	// StatusEqual means that the comparison completed within the work budget
	// and the two values were determined to be equal.
	StatusEqual Status = iota

	// StatusDifferent means that at least one difference was found.
	// A difference is a definitive result: further work cannot undo it, so
	// this status is reported even if the remaining values were not examined.
	StatusDifferent

	// StatusIncomplete means that no difference was found before the work
	// budget was exhausted; the two values may still be different.
	StatusIncomplete
)

// String returns the name of the status.
func (s Status) String() string {
	switch s {
	case StatusEqual:
		return "equal"
	case StatusDifferent:
		return "different"
	case StatusIncomplete:
		return "incomplete"
	default:
		return "invalid cmp.Status"
	}
}

// Compare is like [Equal], but bounds the amount of work spent comparing x
// and y by budget.
//
// The work budget counts the number of concrete value pairs that are entered
// for comparison. Each call that actually begins comparing a value pair
// consumes one unit. Value pairs skipped because they were already visited as
// part of a reference cycle do not consume the budget. Applying an [Ignore],
// [Transformer], or [Comparer] option to a value pair still consumes one unit.
//
// The result is one of:
//   - [StatusEqual] if the comparison finished within the budget and the
//     values are equal.
//   - [StatusDifferent] if a difference was found.
//   - [StatusIncomplete] if the budget was exhausted before the comparison
//     could finish without finding a difference.
//
// Once the budget is exhausted, no further value pairs are examined and no
// further user-provided [Comparer] or [Transformer] callbacks are invoked.
//
// A budget of 0 always reports [StatusIncomplete].
// A negative budget means unlimited, which is equivalent to calling [Equal].
func Compare(x, y any, budget int, opts ...Option) Status {
	s := newState(opts)
	s.budget = budget
	s.compareAny(rootStep(x, y))
	if s.incomplete {
		return StatusIncomplete
	}
	if s.result.Equal() {
		return StatusEqual
	}
	return StatusDifferent
}
