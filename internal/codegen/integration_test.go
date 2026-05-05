package codegen_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/codegen"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/parser"
)

// compile is the test-side version of the compiler pipeline. It returns the
// generated sh and any errors encountered.
func compile(t *testing.T, src string) string {
	t.Helper()
	toks, lerrs := lexer.New("test.tt", src).Tokenize()
	if len(lerrs) > 0 {
		t.Fatalf("lex errors: %v", lerrs)
	}
	file, perrs := parser.New(toks).Parse("test.tt")
	if len(perrs) > 0 {
		t.Fatalf("parse errors: %v", perrs)
	}
	info, cerrs := checker.New().CheckFile(file)
	if len(cerrs) > 0 {
		t.Fatalf("type errors: %v", cerrs)
	}
	return codegen.New(info).Emit(file)
}

// runShell writes the script to a temp file, executes it under /bin/sh and
// returns combined stdout/stderr.
func runShell(t *testing.T, sh string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.sh")
	if err := writeFile(path, sh); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("/bin/sh", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n--script--\n%s\n--output--\n%s", err, sh, out)
	}
	return string(out)
}

func writeFile(path, content string) error {
	return execWrite(path, content)
}

// execWrite is a tiny indirection to avoid pulling os into the test signature.
func execWrite(path, content string) error {
	cmd := exec.Command("/bin/sh", "-c", "cat > '"+path+"'")
	cmd.Stdin = strings.NewReader(content)
	return cmd.Run()
}

func TestHelloWorld(t *testing.T) {
	sh := compile(t, `
		func greet(who: string): string {
			return "Hello, " + who + "!"
		}
		func main(): void {
			let who: string = "world"
			echo(greet(who))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "Hello, world!" {
		t.Errorf("got %q", got)
	}
}

func TestArithmetic(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a: number = 3
			let b: number = 4
			echo(str(a * a + b * b))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "25" {
		t.Errorf("got %q", got)
	}
}

func TestFizzBuzzSlice(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			for i in 1..6 {
				if i % 3 == 0 {
					echo("Fizz")
				} else {
					echo(str(i))
				}
			}
		}
	`)
	out := runShell(t, sh)
	want := "1\n2\nFizz\n4\n5\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestStringInterpolation(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let name: string = "world"
			let n: number = 42
			echo("Hello, ${name}! count=${n}")
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "Hello, world! count=42" {
		t.Errorf("got %q", got)
	}
}

func TestForLines(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			for line in ` + "`printf 'a\\nb\\nc\\n'`" + ` {
				echo("got: " + line)
			}
		}
	`)
	out := runShell(t, sh)
	want := "got: a\ngot: b\ngot: c\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestBoolAndComparisons(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let x: number = 5
			let big: bool = x > 3 && x < 10
			if big {
				echo("yes")
			} else {
				echo("no")
			}
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "yes" {
		t.Errorf("got %q", got)
	}
}

func TestNestedCallsDontClobberRet(t *testing.T) {
	sh := compile(t, `
		func id(s: string): string { return s }
		func main(): void {
			echo(id(id("hello")) + " " + id("world"))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestQuotingSafety(t *testing.T) {
	// Spaces, globs, dollar signs in user data must not be re-interpreted.
	sh := compile(t, `
		func main(): void {
			let dangerous: string = "a b * $(echo NOPE)"
			echo(dangerous)
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "a b * $(echo NOPE)" {
		t.Errorf("got %q", got)
	}
}
