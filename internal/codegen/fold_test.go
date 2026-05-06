package codegen_test

import (
	"strings"
	"testing"
)

func TestConstantFoldIntArith(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo(str(1 + 2))
			echo(str(10 - 3))
			echo(str(4 * 5))
			echo(str(12 / 4))
			echo(str(11 % 3))
		}
	`)
	out := runShell(t, sh)
	want := "3\n7\n20\n3\n2\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
	// Folded constants must not appear as $((expr)) in the generated script.
	for _, notFolded := range []string{"$((1 + 2))", "$((4 * 5))", "$((10 - 3))"} {
		if strings.Contains(sh, notFolded) {
			t.Errorf("constant expression %q was not folded in generated sh", notFolded)
		}
	}
}

func TestConstantFoldIntComparison(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			if 3 > 2 { echo("yes") }
			if 1 == 1 { echo("eq") }
			if 5 != 6 { echo("ne") }
			if 2 < 3 { echo("lt") }
			if 4 >= 4 { echo("ge") }
			if 3 <= 5 { echo("le") }
		}
	`)
	out := runShell(t, sh)
	want := "yes\neq\nne\nlt\nge\nle\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestConstantFoldUnaryMinus(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let x = -7
			echo(str(x))
			echo(str(-3 + 1))
		}
	`)
	out := runShell(t, sh)
	want := "-7\n-2\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
	if strings.Contains(sh, "-(7)") {
		t.Error("unary minus on integer literal was not folded")
	}
}

func TestConstantFoldBoolLiterals(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			if true && true { echo("tt") }
			if true && false { echo("tf") } else { echo("!tf") }
			if false || true { echo("ft") }
			if false || false { echo("ff") } else { echo("!ff") }
			if false && true { echo("short") } else { echo("ok") }
		}
	`)
	out := runShell(t, sh)
	want := "tt\n!tf\nft\n!ff\nok\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestConstantFoldNotLiteral(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			if !false { echo("yes") }
			if !true { echo("no") } else { echo("ok") }
		}
	`)
	out := runShell(t, sh)
	want := "yes\nok\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestConstantFoldChained(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo(str((1 + 2) * 3))
			echo(str(100 / (4 * 5)))
		}
	`)
	out := runShell(t, sh)
	want := "9\n5\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}
