// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp_test

import (
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// ExampleAuditReporter demonstrates that an ignored field is recorded as
// obscured even when the overall comparison reports equal. The final result
// being equal must not be interpreted as every field being equal.
func ExampleAuditReporter() {
	type Account struct {
		Username string
		Secret   string
	}
	x := Account{Username: "alice", Secret: "s3cr3t"}
	y := Account{Username: "alice", Secret: "totally-different"}

	var ar cmp.AuditReporter
	eq := cmp.Equal(x, y, cmpopts.IgnoreFields(Account{}, "Secret"), cmp.Reporter(&ar))
	fmt.Println("equal:", eq)
	fmt.Print(ar.String())

	// Output:
	// equal: true
	// {cmp_test.Account}.Username: equal
	// {cmp_test.Account}.Secret: ignored
}
