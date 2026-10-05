// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// Run the audit reporter tests with:
//
//	go test github.com/google/go-cmp/cmp -run 'TestAuditReporter' -v

// TestAuditReporterIgnore verifies that an ignored field is recorded as
// obscured even when the overall comparison reports equal. Equality must
// not be interpreted as every field being equal.
func TestAuditReporterIgnore(t *testing.T) {
	type Account struct {
		Username string
		Password string
		Secret   string
	}
	x := Account{Username: "alice", Password: "hunter2", Secret: "s3cr3t-value"}
	y := Account{Username: "alice", Password: "hunter2", Secret: "totally-different"}

	var ar cmp.AuditReporter
	if eq := cmp.Equal(x, y, cmpopts.IgnoreFields(Account{}, "Secret"), cmp.Reporter(&ar)); !eq {
		t.Fatalf("Equal = false, want true")
	}

	records := ar.Records()
	var secret *cmp.AuditRecord
	for i := range records {
		if strings.HasSuffix(records[i].Path, ".Secret") {
			secret = &records[i]
		}
	}
	if secret == nil {
		t.Fatalf("ignored Secret not recorded:\n%s", ar.String())
	}
	if secret.State != cmp.AuditIgnored {
		t.Errorf("Secret state = %v, want %v", secret.State, cmp.AuditIgnored)
	}
	if got := ar.IgnoredPaths(); len(got) != 1 || !strings.HasSuffix(got[0], ".Secret") {
		t.Errorf("IgnoredPaths = %v, want exactly one .Secret path", got)
	}

	var username *cmp.AuditRecord
	for i := range records {
		if strings.HasSuffix(records[i].Path, ".Username") {
			username = &records[i]
		}
	}
	if username == nil || username.State != cmp.AuditEqual {
		t.Errorf("Username not recorded as compared equal:\n%s", ar.String())
	}

	if strings.Contains(ar.String(), "s3cr3t-value") || strings.Contains(ar.String(), "totally-different") {
		t.Errorf("report contains original field values:\n%s", ar.String())
	}
}

// TestAuditReporterUnvisited verifies that branches the engine never visits
// (the children of an ignored node) are not recorded as compared.
func TestAuditReporterUnvisited(t *testing.T) {
	type Inner struct {
		Secret string
		Value  int
	}
	type Outer struct {
		A Inner
		B Inner
	}
	x := Outer{
		A: Inner{Secret: "a-secret", Value: 1},
		B: Inner{Secret: "b-secret", Value: 2},
	}
	y := Outer{
		A: Inner{Secret: "a-secret", Value: 1},
		B: Inner{Secret: "b-secret", Value: 2},
	}

	var ar cmp.AuditReporter
	opt := cmp.FilterPath(func(p cmp.Path) bool {
		return p.Last().String() == ".A"
	}, cmp.Ignore())
	if eq := cmp.Equal(x, y, opt, cmp.Reporter(&ar)); !eq {
		t.Fatalf("Equal = false, want true")
	}

	for _, rec := range ar.Records() {
		if strings.Contains(rec.Path, ".A.") {
			t.Errorf("unvisited branch recorded as compared: %s (%v)", rec.Path, rec.State)
		}
	}

	var bValue *cmp.AuditRecord
	for i := range ar.Records() {
		if strings.HasSuffix(ar.Records()[i].Path, ".B.Value") {
			bValue = &ar.Records()[i]
		}
	}
	if bValue == nil || bValue.State != cmp.AuditEqual {
		t.Errorf("visited B.Value not recorded as compared equal:\n%s", ar.String())
	}
}

// TestAuditReporterTransformerUnvisited verifies that the original children
// of a transformed node are not recorded as compared.
func TestAuditReporterTransformerUnvisited(t *testing.T) {
	type Inner struct {
		Secret string
		Value  int
	}
	type Outer struct {
		A Inner
		B Inner
	}
	x := Outer{A: Inner{Secret: "a", Value: 1}, B: Inner{Secret: "b", Value: 2}}
	y := Outer{A: Inner{Secret: "a", Value: 1}, B: Inner{Secret: "b", Value: 2}}

	var ar cmp.AuditReporter
	opt := cmp.Transformer("InnerLen", func(i Inner) int { return i.Value })
	if eq := cmp.Equal(x, y, opt, cmp.Reporter(&ar)); !eq {
		t.Fatalf("Equal = false, want true")
	}

	for _, rec := range ar.Records() {
		if strings.Contains(rec.Path, ".A.") && !strings.Contains(rec.Path, "InnerLen") {
			t.Errorf("unvisited children of transformed node recorded: %s (%v)", rec.Path, rec.State)
		}
	}
}

// TestAuditReporterComparerCount verifies that attaching the reporter does
// not cause any Comparer to be re-run: the number of Comparer invocations is
// identical with and without the reporter.
func TestAuditReporterComparerCount(t *testing.T) {
	type Item struct {
		ID   int
		Name string
	}
	x := []Item{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}
	y := []Item{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}}

	count := 0
	cm := cmp.Comparer(func(a, b Item) bool {
		count++
		return a.ID == b.ID && a.Name == b.Name
	})
	want := cmp.Equal(x, y, cm)
	wantCount := count

	count = 0
	var ar cmp.AuditReporter
	got := cmp.Equal(x, y, cm, cmp.Reporter(&ar))
	gotCount := count

	if got != want || gotCount != wantCount {
		t.Errorf("Equal with reporter = %v (comparer count %d), without = %v (count %d)", got, gotCount, want, wantCount)
	}
	if gotCount == 0 {
		t.Errorf("comparer was never called; expected it to be called")
	}
}

// TestAuditReporterCycle verifies that reference cycles are recorded and
// associated by identity with the earlier occurrence.
func TestAuditReporterCycle(t *testing.T) {
	type Node struct {
		Next *Node
	}
	x := &Node{}
	x.Next = x
	y := &Node{}
	y.Next = y

	var ar cmp.AuditReporter
	if eq := cmp.Equal(x, y, cmp.Reporter(&ar)); !eq {
		t.Fatalf("Equal = false, want true")
	}

	var cycle *cmp.AuditRecord
	for i := range ar.Records() {
		if ar.Records()[i].State == cmp.AuditCycle {
			cycle = &ar.Records()[i]
		}
	}
	if cycle == nil {
		t.Fatalf("cycle not recorded:\n%s", ar.String())
	}
	if !cycle.Equal {
		t.Errorf("cycle Equal = false, want true")
	}
	if cycle.Link == "" {
		t.Errorf("cycle not associated by identity:\n%s", ar.String())
	}
}

// TestAuditReporterCycleUnequal verifies that a cycle that is not paired
// between x and y is recorded as unequal.
func TestAuditReporterCycleUnequal(t *testing.T) {
	type Node struct {
		Next *Node
	}
	x := &Node{}
	x.Next = x
	y := &Node{}
	y.Next = &Node{} // Different object: no paired cycle.

	var ar cmp.AuditReporter
	eq := cmp.Equal(x, y, cmp.Reporter(&ar))
	if eq {
		t.Fatalf("Equal = true, want false")
	}

	var found bool
	for _, rec := range ar.Records() {
		if rec.State == cmp.AuditCycle {
			found = true
			if rec.Equal {
				t.Errorf("cycle Equal = true, want false")
			}
		}
	}
	if !found {
		t.Errorf("cycle not recorded:\n%s", ar.String())
	}
}

// TestAuditReporterCycleOneSided verifies that a cycle present on only one
// side is associated by identity with the earlier occurrence on that side.
func TestAuditReporterCycleOneSided(t *testing.T) {
	type Node struct {
		Next *Node
	}
	x := &Node{}
	x.Next = &Node{} // x has a chain, no cycle.
	y := &Node{}
	y.Next = y // y has a self-cycle.

	var ar cmp.AuditReporter
	eq := cmp.Equal(x, y, cmp.Reporter(&ar))
	if eq {
		t.Fatalf("Equal = true, want false")
	}

	var cycle *cmp.AuditRecord
	for i := range ar.Records() {
		if ar.Records()[i].State == cmp.AuditCycle {
			cycle = &ar.Records()[i]
		}
	}
	if cycle == nil {
		t.Fatalf("cycle not recorded:\n%s", ar.String())
	}
	if cycle.Equal {
		t.Errorf("cycle Equal = true, want false")
	}
	if cycle.Link == "" {
		t.Errorf("one-sided cycle not associated by identity:\n%s", ar.String())
	}
}

// TestAuditReporterCombinedOptions verifies that the reporter records both
// ignored and compared paths when Ignore is combined with a Comparer and
// with cmpopts helpers.
func TestAuditReporterCombinedOptions(t *testing.T) {
	type User struct {
		Name string
		Age  int
	}
	x := User{Name: "alice", Age: 1}
	y := User{Name: "alice", Age: 2} // Age differs but is ignored

	var ar cmp.AuditReporter
	eq := cmp.Equal(x, y,
		cmp.Comparer(func(a, b string) bool { return a == b }),
		cmpopts.IgnoreFields(User{}, "Age"),
		cmp.Reporter(&ar))
	if !eq {
		t.Fatalf("Equal = false, want true")
	}

	var ignored, compared bool
	for _, rec := range ar.Records() {
		switch {
		case strings.HasSuffix(rec.Path, ".Age") && rec.State == cmp.AuditIgnored:
			ignored = true
		case strings.HasSuffix(rec.Path, ".Name") && rec.State == cmp.AuditEqual:
			compared = true
		}
	}
	if !ignored {
		t.Errorf("Age not recorded as ignored:\n%s", ar.String())
	}
	if !compared {
		t.Errorf("Name not recorded as compared:\n%s", ar.String())
	}
}

// TestAuditReporterIgnoreUnexported verifies that unexported fields ignored
// via cmpopts.IgnoreUnexported are recorded as obscured.
func TestAuditReporterIgnoreUnexported(t *testing.T) {
	type token struct{ value string }
	type Account struct {
		Name string
		auth token
	}
	x := Account{Name: "alice", auth: token{value: "x"}}
	y := Account{Name: "alice", auth: token{value: "y"}}

	var ar cmp.AuditReporter
	eq := cmp.Equal(x, y, cmpopts.IgnoreUnexported(Account{}), cmp.Reporter(&ar))
	if !eq {
		t.Fatalf("Equal = false, want true")
	}

	var ignored bool
	for _, rec := range ar.Records() {
		if rec.State == cmp.AuditIgnored {
			ignored = true
		}
	}
	if !ignored {
		t.Errorf("unexported field not recorded as ignored:\n%s", ar.String())
	}
}

// TestAuditReporterPath verifies that anonymous (embedded) fields and slice
// indices are preserved in the recorded path.
func TestAuditReporterPath(t *testing.T) {
	type Inner struct {
		Value int
	}
	type Outer struct {
		Inner // anonymous embedding
		Items []int
	}
	x := Outer{Inner: Inner{Value: 1}, Items: []int{10, 20}}
	y := Outer{Inner: Inner{Value: 1}, Items: []int{10, 20}}

	var ar cmp.AuditReporter
	if eq := cmp.Equal(x, y, cmp.Reporter(&ar)); !eq {
		t.Fatalf("Equal = false, want true")
	}

	wantPaths := map[string]bool{
		".Inner.Value": false,
		".Items[0]":    false,
		".Items[1]":    false,
	}
	for _, rec := range ar.Records() {
		for suffix := range wantPaths {
			if strings.HasSuffix(rec.Path, suffix) {
				wantPaths[suffix] = true
			}
		}
	}
	for suffix, found := range wantPaths {
		if !found {
			t.Errorf("path suffix %q not recorded:\n%s", suffix, ar.String())
		}
	}
}

// TestAuditReporterMapKey verifies that map entries are recorded with their
// real keys in the path.
func TestAuditReporterMapKey(t *testing.T) {
	x := map[string]int{"a": 1, "b": 2}
	y := map[string]int{"a": 1, "b": 2}

	var ar cmp.AuditReporter
	if eq := cmp.Equal(x, y, cmp.Reporter(&ar)); !eq {
		t.Fatalf("Equal = false, want true")
	}

	var found bool
	for _, rec := range ar.Records() {
		if strings.Contains(rec.Path, `["a"]`) {
			found = true
		}
	}
	if !found {
		t.Errorf("map entry key not preserved in path:\n%s", ar.String())
	}
}

// TestAuditReporterRecordsCopy verifies that Records returns a copy.
func TestAuditReporterRecordsCopy(t *testing.T) {
	type Item struct{ X int }
	x := Item{X: 1}
	y := Item{X: 1}

	var ar cmp.AuditReporter
	cmp.Equal(x, y, cmp.Reporter(&ar))

	records := ar.Records()
	records[0].Path = "mutated"
	if got := ar.Records()[0].Path; got == "mutated" {
		t.Errorf("Records did not return a copy: path mutated to %q", got)
	}
}
