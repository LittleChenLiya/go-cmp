// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp_test

import (
	"fmt"

	"github.com/google/go-cmp/cmp"
)

// ExampleAuditReporter demonstrates how to audit a comparison so that a
// final "equal" verdict is not misread as "all fields are identical":
// the report explicitly lists the real paths that were masked by Ignore
// options, and branches the engine never entered are absent from the
// tree rather than marked as compared.
func ExampleAuditReporter() {
	type Credentials struct {
		User   string
		Secret string
	}
	x := Credentials{User: "alice", Secret: "s3cr3t"}
	y := Credentials{User: "alice", Secret: "hunter2"}

	ignoreSecret := cmp.FilterPath(func(p cmp.Path) bool {
		return p.String() == "Secret"
	}, cmp.Ignore())

	ar := cmp.NewAuditReporter()
	fmt.Println("equal:", cmp.Equal(x, y, ignoreSecret, cmp.Reporter(ar)))
	fmt.Println("masked:", ar.Ignored())
	fmt.Print(ar)

	// Output:
	// equal: true
	// masked: [{cmp_test.Credentials}.Secret]
	// {cmp_test.Credentials}: unvisited (traversed, but never directly compared)
	//   .User: compared equal (by built-in comparison)
	//   .Secret: IGNORED (masked by an Ignore option; subtree not visited)
}
