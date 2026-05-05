package codegen_test

import (
	"strings"
	"testing"
)

func TestStringOrderingInCondition(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a = "apple"
			let b = "banana"
			if a < b {
				echo("apple < banana")
			}
			if b > a {
				echo("banana > apple")
			}
			if a >= a {
				echo("apple >= apple")
			}
		}
	`)
	out := runShell(t, sh)
	want := "apple < banana\nbanana > apple\napple >= apple\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestStringOrderingInExpression(t *testing.T) {
	// The result of a comparison can be assigned to a bool variable. That
	// path goes through compareOp (not compileCmpCond), so it exercises the
	// awk-as-substitution form.
	sh := compile(t, `
		func main(): void {
			let a = "z"
			let b = "a"
			let lt = a < b
			let gt = a > b
			if lt { echo("a<b") } else { echo("not a<b") }
			if gt { echo("a>b") } else { echo("not a>b") }
		}
	`)
	out := runShell(t, sh)
	want := "not a<b\na>b\n"
	if !strings.HasPrefix(out, want) {
		t.Errorf("got %q want prefix %q", out, want)
	}
}
