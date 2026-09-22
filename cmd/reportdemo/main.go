// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command reportdemo runs cmp.Compare on a structure containing slice edits,
// map differences, cyclic pointers, and a password field, printing two
// machine-readable JSON reports:
//
//  1. a report with MaxEntries=2 to demonstrate entry truncation, and
//  2. a report that ignores the password field to demonstrate the difference.
//
// The reports intentionally contain no passwords, pointer addresses, or text
// produced by String/GoString methods.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type secret string

func (s secret) String() string { return "STRINGER_SENTINEL" }

type user struct {
	Name     string
	Password secret
	Roles    []string
	Props    map[string]int
	Self     *user
}

func values() (user, user) {
	x := user{
		Name:     "ada",
		Password: "do-not-print",
		Roles:    []string{"admin", "ops"},
		Props:    map[string]int{"retries": 0, "legacy": 1},
	}
	x.Self = &x
	y := user{
		Name:     "ada-lovelace",
		Password: "also-do-not-print",
		Roles:    []string{"admin", "ops", "auditor"},
		Props:    map[string]int{"retries": 3, "fresh": 2},
	}
	y.Self = &y
	return x, y
}

func printJSON(label string, r cmp.CompareReport) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Printf("=== %s ===\n%s\n", label, b)
}

func main() {
	x, y := values()

	truncated := cmp.Compare(x, y, cmp.CompareConfig{
		MaxEntries:      2,
		MaxSummaryBytes: 96,
	})
	printJSON("report 1: max_entries=2 (truncated)", truncated)

	ignored := cmp.Compare(x, y, cmp.CompareConfig{
		MaxSummaryBytes: 96,
	}, cmpopts.IgnoreFields(user{}, "Password"))
	printJSON("report 2: password ignored", ignored)
}
