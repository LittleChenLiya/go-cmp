// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp

import (
	"math"
	"testing"
)

// TestIncCountOverflow verifies that incCount sets the overflow flag when
// the count would wrap past MaxInt.
func TestIncCountOverflow(t *testing.T) {
	var n int = math.MaxInt
	var overflow bool
	incCount(&n, &overflow)
	if !overflow {
		t.Error("overflow not set when count == MaxInt")
	}
	if n != math.MaxInt {
		t.Errorf("n = %d, want %d (unchanged)", n, math.MaxInt)
	}
}

// TestIncCountNormal verifies that incCount increments normally.
func TestIncCountNormal(t *testing.T) {
	var n int
	var overflow bool
	incCount(&n, &overflow)
	if overflow {
		t.Error("overflow set unexpectedly")
	}
	if n != 1 {
		t.Errorf("n = %d, want 1", n)
	}
}
