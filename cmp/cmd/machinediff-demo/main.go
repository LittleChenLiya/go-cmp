// Command machinediff-demo emits two machine-readable difference reports as
// JSON:
//
//  1. report-truncated.json: a report capped with WithMaxEntries(2).
//  2. report-ignored.json: the same comparison with an Ignore option that
//     hides the password field, showing how the report changes.
//
// The compared values contain slice insertions/removals, maps, cyclic
// pointers, a password field, and a value implementing fmt.Stringer. The
// reports never contain the password, a pointer address, or the Stringer
// sentinel text.
package main

import (
	"encoding/json"
	"os"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// Account is a sample record containing a sensitive field and a cycle.
type Account struct {
	Name     string
	Password string
	Tags     []string
	Meta     map[string]int
	Self     *Account
	Token    string
}

func main() {
	x := &Account{
		Name:     "alice",
		Password: "super-secret-password",
		Token:    "super-secret-token",
		Tags:     []string{"admin", "db"},
		Meta:     map[string]int{"retries": 3, "age": 10, "level": 2},
	}
	y := &Account{
		Name:     "alice",
		Password: "changed-password",
		Token:    "changed-token",
		Tags:     []string{"admin", "db", "ops"},
		Meta:     map[string]int{"retries": 5, "age": 10, "level": 2, "quota": 9},
	}
	x.Self, y.Self = x, y

	truncated := cmp.MachineDiff(x, y, cmp.WithMaxEntries(2))
	ignored := cmp.MachineDiff(x, y,
		cmpopts.IgnoreFields(Account{}, "Password"),
	)

	if err := writeJSON("report-truncated.json", truncated); err != nil {
		panic(err)
	}
	if err := writeJSON("report-ignored.json", ignored); err != nil {
		panic(err)
	}
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
