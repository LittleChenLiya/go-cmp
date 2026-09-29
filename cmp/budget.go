// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

// Status is the outcome of a budgeted comparison performed by [EqualBudgeted].
type Status uint8

const (
	// StatusIncomplete is the zero value of [Status] and indicates that the
	// comparison could not finish within the requested work budget.
	StatusIncomplete Status = iota
	// StatusEqual indicates that the comparison finished and the values are
	// equal, exactly as [Equal] would report.
	StatusEqual
	// StatusDifferent indicates that the comparison finished and at least one
	// difference was found, exactly as [Equal] would report.
	StatusDifferent
)

func (s Status) String() string {
	switch s {
	case StatusEqual:
		return "equal"
	case StatusDifferent:
		return "different"
	default:
		return "incomplete"
	}
}

// Equal reports whether the comparison finished with [StatusEqual].
func (s Status) Equal() bool { return s == StatusEqual }

// Different reports whether the comparison finished with [StatusDifferent].
func (s Status) Different() bool { return s == StatusDifferent }

// Incomplete reports whether the comparison exhausted the work budget before
// it could determine equality.
func (s Status) Incomplete() bool { return s == StatusIncomplete }

// BudgetResult is the result of [EqualBudgeted].
type BudgetResult struct {
	// Status is the outcome of the comparison. It is [StatusIncomplete] if
	// the comparison exhausted the budget before finishing; otherwise it is
	// [StatusEqual] or [StatusDifferent].
	Status Status
	// Used is the number of value pairs charged to the primary comparison
	// traversal. Entering a comparison of a real value pair counts once.
	// Value pairs already visited on the current path as part of a reference
	// cycle are not charged. It never exceeds the requested budget.
	Used int
}

// EqualBudgeted compares x and y using the given options with an explicit
// work budget, returning a three-valued result that distinguishes values
// known to be equal, values known to be different, and comparisons that did
// not finish within the budget.
//
// The budget bounds the number of comparisons of real value pairs performed
// by the traversal: every entry into the comparison of a value pair
// consumes one unit. Value pairs already visited on the current path as part
// of a reference cycle are visited for free and do not consume a unit. Once
// the budget is exhausted, the traversal stops immediately; no [Comparer],
// [Transformer], or Equal method is invoked for the value pair that would
// exceed the budget, nor for any later value pair. The result is
// [StatusIncomplete], which is never reported as equal or different, even if
// a difference was observed before the budget was exhausted. A budget of
// zero always returns [StatusIncomplete] without comparing any values.
//
// A negative budget disables the limit, in which case [EqualBudgeted] behaves
// like [Equal] and the returned result is [StatusEqual] or
// [StatusDifferent].
//
// [Ignore], [Transformer], and [Comparer] options and [Reporter]s are
// honored exactly as in [Equal]. Note that [EqualBudgeted] does not produce
// a textual difference report; use a [Reporter] if partial output is needed.
func EqualBudgeted(x, y any, budget int, opts ...Option) BudgetResult {
	s := newState(opts)
	s.budget = budget
	s.compareAny(rootStep(x, y))
	r := BudgetResult{Used: s.used}
	switch {
	case s.incomplete:
		r.Status = StatusIncomplete
	case s.result.Equal():
		r.Status = StatusEqual
	default:
		r.Status = StatusDifferent
	}
	return r
}
