// Copyright 2026, The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmp_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Targeted test command for this feature:
//	go test ./cmp/ -run 'TestAuditReporter' -v
//	go test ./cmp/ -run 'ExampleAuditReporter' -v

// AuditEmbedded is an exported type so that it can be embedded as an
// anonymous field without tripping the unexported-field checks.
type AuditEmbedded struct {
	Label string
}

type auditSecret struct {
	Token   string
	Retries int
}

type auditConfig struct {
	AuditEmbedded // anonymous (embedded) field
	Name          string
	Secret        auditSecret
	Tags          []string
	Lookup        map[string]int
}

type auditNode struct {
	Name string
	Next *auditNode
}

// findAuditNode locates the first node with the given step string
// anywhere in the subtree.
func findAuditNode(n *cmp.AuditNode, step string) *cmp.AuditNode {
	if n == nil {
		return nil
	}
	if n.Step == step {
		return n
	}
	for _, c := range n.Children {
		if m := findAuditNode(c, step); m != nil {
			return m
		}
	}
	return nil
}

// countStatus counts nodes in the subtree with the given status.
func countStatus(n *cmp.AuditNode, s cmp.AuditStatus) int {
	if n == nil {
		return 0
	}
	cnt := 0
	if n.Status == s {
		cnt++
	}
	for _, c := range n.Children {
		cnt += countStatus(c, s)
	}
	return cnt
}

func makeAuditConfigs() (x, y auditConfig) {
	x = auditConfig{
		AuditEmbedded: AuditEmbedded{Label: "embedded"},
		Name:          "alpha",
		Secret:        auditSecret{Token: "s3cr3t-x", Retries: 1},
		Tags:          []string{"a", "b"},
		Lookup:        map[string]int{"k": 1},
	}
	y = x
	y.Secret = auditSecret{Token: "s3cr3t-y", Retries: 99} // masked difference
	return x, y
}

func ignoreAuditSecret() cmp.Option {
	return cmp.FilterPath(func(p cmp.Path) bool {
		return p.String() == "Secret"
	}, cmp.Ignore())
}

// TestAuditReporterIgnoreMasked verifies that when Ignore masks the
// Secret field and the objects compare equal, the audit report still
// shows Secret as ignored rather than compared.
func TestAuditReporterIgnoreMasked(t *testing.T) {
	x, y := makeAuditConfigs()

	ar := cmp.NewAuditReporter()
	if !cmp.Equal(x, y, ignoreAuditSecret(), cmp.Reporter(ar)) {
		t.Fatal("Equal reported false, want true (Secret is ignored)")
	}

	// The ignored path must be listed even though the result is equal.
	ignored := ar.Ignored()
	if len(ignored) != 1 || !strings.HasSuffix(ignored[0], "}.Secret") {
		t.Fatalf("Ignored() = %v, want a single entry ending in }.Secret", ignored)
	}

	root := ar.Root()
	if root == nil {
		t.Fatal("Root() is nil")
	}

	// The Secret node is ignored and its subtree was never visited:
	// Token and Retries must not appear as compared nodes.
	sec := findAuditNode(root, ".Secret")
	if sec == nil {
		t.Fatal("audit tree has no .Secret node")
	}
	if sec.Status != cmp.AuditIgnored {
		t.Errorf(".Secret status = %v, want %v", sec.Status, cmp.AuditIgnored)
	}
	if len(sec.Children) != 0 {
		t.Errorf(".Secret has %d children, want 0 (subtree never visited)", len(sec.Children))
	}
	if findAuditNode(root, ".Token") != nil || findAuditNode(root, ".Retries") != nil {
		t.Error("children of ignored Secret appear in the audit tree; they were never visited")
	}

	// Genuinely compared leaves are marked as compared.
	if name := findAuditNode(root, ".Name"); name == nil || name.Status != cmp.AuditCompared || !name.Equal {
		t.Errorf(".Name node = %+v, want compared and equal", name)
	}

	// Interior nodes were traversed but never directly compared;
	// they must not be marked as compared.
	if root.Status != cmp.AuditUnvisited {
		t.Errorf("root status = %v, want %v", root.Status, cmp.AuditUnvisited)
	}

	// The report must not copy the compared values.
	report := ar.String()
	if !strings.Contains(report, ".Secret") || !strings.Contains(report, "IGNORED") {
		t.Errorf("report does not show .Secret as IGNORED:\n%s", report)
	}
	if strings.Contains(report, "s3cr3t") {
		t.Errorf("report leaks a compared value:\n%s", report)
	}
}

// TestAuditReporterComparerCount verifies that attaching the audit
// reporter does not change how many times a Comparer is invoked:
// the reporter observes the same single traversal and never re-runs
// the comparison.
func TestAuditReporterComparerCount(t *testing.T) {
	x, y := makeAuditConfigs()

	var calls int
	countStrings := cmp.Comparer(func(a, b string) bool {
		calls++
		return a == b
	})

	// Baseline: ordinary comparison without the reporter.
	calls = 0
	if cmp.Equal(x, y, countStrings) {
		t.Fatal("Equal reported true, want false (Secrets differ)")
	}
	want := calls
	if want == 0 {
		t.Fatal("comparer was never called")
	}

	// With the reporter attached, the count must be identical.
	calls = 0
	ar := cmp.NewAuditReporter()
	if cmp.Equal(x, y, countStrings, cmp.Reporter(ar)) {
		t.Fatal("Equal reported true, want false (Secrets differ)")
	}
	if calls != want {
		t.Errorf("comparer called %d times with reporter, want %d (same as without)", calls, want)
	}

	// The compared-by-Comparer leaves are visible in the audit tree.
	if got := countStatus(ar.Root(), cmp.AuditCompared); got == 0 {
		t.Error("no compared nodes recorded")
	}
	if sec := findAuditNode(ar.Root(), ".Token"); sec == nil || !sec.ByFunc {
		t.Errorf(".Token node = %+v, want compared by Comparer", sec)
	}
}

// TestAuditReporterCycle verifies that cyclic references are correlated
// by pointer identity: the cycle node links back to the ancestor node
// that first visited the same address.
func TestAuditReporterCycle(t *testing.T) {
	// Equal cycles: both lists loop back onto themselves.
	x := &auditNode{Name: "loop"}
	x.Next = x
	y := &auditNode{Name: "loop"}
	y.Next = y

	ar := cmp.NewAuditReporter()
	if !cmp.Equal(x, y, cmp.Reporter(ar)) {
		t.Fatal("Equal reported false, want true for isomorphic cycles")
	}
	next := findAuditNode(ar.Root(), ".Next")
	if next == nil {
		t.Fatal("audit tree has no .Next node")
	}
	if !next.ByCycle || next.Status != cmp.AuditCompared || !next.Equal {
		t.Errorf(".Next = %+v, want compared equal by cycle", next)
	}
	if next.Cycle != ar.Root() {
		t.Errorf(".Next.Cycle = %p, want root %p (identity correlation)", next.Cycle, ar.Root())
	}

	// Mismatched cycles: x loops onto itself, y loops through a second node.
	x2 := &auditNode{Name: "loop"}
	x2.Next = x2
	y1 := &auditNode{Name: "loop"}
	y2 := &auditNode{Name: "loop"}
	y1.Next = y2
	y2.Next = y1

	ar2 := cmp.NewAuditReporter()
	if cmp.Equal(x2, y1, cmp.Reporter(ar2)) {
		t.Fatal("Equal reported true, want false for non-isomorphic cycles")
	}
	next2 := findAuditNode(ar2.Root(), ".Next")
	if next2 == nil || !next2.ByCycle || next2.Equal {
		t.Errorf(".Next = %+v, want compared unequal by cycle", next2)
	}
	if next2.Cycle != ar2.Root() {
		t.Errorf(".Next.Cycle = %p, want root %p (identity correlation)", next2.Cycle, ar2.Root())
	}
}

// TestAuditReporterSliceCycle verifies cycle correlation through slices,
// where the engine tracks element addresses.
func TestAuditReporterSliceCycle(t *testing.T) {
	type sliceNode struct {
		Kids []*sliceNode
	}
	x := &sliceNode{}
	x.Kids = []*sliceNode{x}
	y := &sliceNode{}
	y.Kids = []*sliceNode{y}

	ar := cmp.NewAuditReporter()
	if !cmp.Equal(x, y, cmp.Reporter(ar)) {
		t.Fatal("Equal reported false, want true for isomorphic slice cycles")
	}
	elem := findAuditNode(ar.Root(), "[0]")
	if elem == nil || !elem.ByCycle {
		t.Fatalf("[0] node = %+v, want compared by cycle", elem)
	}
	if elem.Cycle != ar.Root() {
		t.Errorf("[0].Cycle = %p, want root %p (identity correlation)", elem.Cycle, ar.Root())
	}
}

// TestAuditReporterRealPaths verifies that anonymous (embedded) struct
// fields, slice indices, and map keys are preserved as real paths.
func TestAuditReporterRealPaths(t *testing.T) {
	x := auditConfig{
		AuditEmbedded: AuditEmbedded{Label: "same"},
		Name:          "alpha",
		Tags:          []string{"a", "b", "c"},
		Lookup:        map[string]int{"k": 1},
	}
	y := x
	y.Tags = []string{"a", "X", "c"} // differs at index 1

	ar := cmp.NewAuditReporter()
	if cmp.Equal(x, y, cmp.Reporter(ar)) {
		t.Fatal("Equal reported true, want false")
	}

	root := ar.Root()
	prefix := "{cmp_test.auditConfig}"

	// Anonymous field: the embedded struct is a real path step.
	emb := root.Find(prefix + ".AuditEmbedded")
	if emb == nil {
		t.Fatalf("no node for anonymous field; tree:\n%s", ar)
	}
	if lbl := root.Find(prefix + ".AuditEmbedded.Label"); lbl == nil || lbl.Status != cmp.AuditCompared {
		t.Errorf("embedded field path %q not compared: %+v", prefix+".AuditEmbedded.Label", lbl)
	}

	// Slice index: the differing element keeps its real index.
	elem := root.Find(prefix + ".Tags[1]")
	if elem == nil {
		t.Fatalf("no node for slice index 1; tree:\n%s", ar)
	}
	if elem.Status != cmp.AuditCompared || elem.Equal {
		t.Errorf("Tags[1] = %+v, want compared unequal", elem)
	}

	// Map key: the entry keeps its real key as a path component.
	entry := root.Find(prefix + `.Lookup["k"]`)
	if entry == nil || entry.Status != cmp.AuditCompared {
		t.Errorf(`Lookup["k"] = %+v, want compared`, entry)
	}
}

// TestAuditReporterCombinedOptions verifies that Ignore, Comparer, and
// Transformer options compose correctly under audit: the ignored node is
// masked, the transformed node is compared through the transformer, and
// the comparer decides the transformed output.
func TestAuditReporterCombinedOptions(t *testing.T) {
	type wrapped struct{ V string }
	type record struct {
		ID      int
		Secret  string
		Wrapped wrapped
	}

	x := record{ID: 7, Secret: "s3cr3t-a", Wrapped: wrapped{"Hello"}}
	y := record{ID: 7, Secret: "s3cr3t-b", Wrapped: wrapped{"hello"}}

	opts := cmp.Options{
		cmp.FilterPath(func(p cmp.Path) bool {
			return p.String() == "Secret"
		}, cmp.Ignore()),
		cmp.Comparer(func(a, b string) bool { return strings.EqualFold(a, b) }),
		cmp.Transformer("Unwrap", func(w wrapped) string { return w.V }),
	}

	ar := cmp.NewAuditReporter()
	if !cmp.Equal(x, y, opts, cmp.Reporter(ar)) {
		t.Fatal("Equal reported false, want true (Secret ignored, Unwrap+EqualFold)")
	}

	// The ignored Secret is masked, not compared.
	if ignored := ar.Ignored(); len(ignored) != 1 || !strings.HasSuffix(ignored[0], "}.Secret") {
		t.Errorf("Ignored() = %v, want a single entry ending in }.Secret", ignored)
	}
	if sec := findAuditNode(ar.Root(), ".Secret"); sec == nil || sec.Status != cmp.AuditIgnored {
		t.Errorf(".Secret = %+v, want ignored", sec)
	}

	// The transformer step is a real path step and its output is
	// compared by the Comparer.
	tr := findAuditNode(ar.Root(), "Unwrap()")
	if tr == nil {
		t.Fatalf("no Unwrap() transform step; tree:\n%s", ar)
	}
	if tr.Status != cmp.AuditCompared || !tr.ByFunc || !tr.Equal {
		t.Errorf("Unwrap() = %+v, want compared equal by Comparer", tr)
	}
	if got, want := tr.Path(), "{cmp_test.record}.Wrapped"; !strings.Contains(got, want) {
		t.Errorf("transform path = %q, want it to contain %q", got, want)
	}

	// The report must not leak the masked secret values.
	if report := ar.String(); strings.Contains(report, "s3cr3t") {
		t.Errorf("report leaks a masked value:\n%s", report)
	}
}

// TestAuditReporterReuse verifies that a reporter resets itself when
// reused for a second comparison.
func TestAuditReporterReuse(t *testing.T) {
	ar := cmp.NewAuditReporter()
	cmp.Equal(1, 1, cmp.Reporter(ar))
	first := ar.String()
	cmp.Equal(2, 3, cmp.Reporter(ar))
	second := ar.String()
	if first == second {
		t.Error("report did not change after reuse; want a reset between traversals")
	}
	if ar.Root() == nil || ar.Root().Status != cmp.AuditCompared || ar.Root().Equal {
		t.Errorf("root after reuse = %+v, want compared unequal", ar.Root())
	}
}
