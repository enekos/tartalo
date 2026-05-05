package codegen_test

import (
	"strings"
	"testing"
)

// TestSpecialCharsInString runs a string through the compiler that contains
// every metacharacter the shell cares about. The interpolation pipeline must
// keep them inert.
func TestSpecialCharsInString(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s = "back\`+"`"+`tick \$dollar \"quote\\ slash * glob ? mark [bracket] | pipe & amp ; semi (paren) <redir> {brace}"
			echo(s)
		}
	`)
	out := runShell(t, sh)
	want := "back`tick $dollar \"quote\\ slash * glob ? mark [bracket] | pipe & amp ; semi (paren) <redir> {brace}\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestCommandSubstitutionInUserData ensures user data that *looks* like a
// shell command substitution stays inert. This is the classic injection
// regression.
func TestCommandSubstitutionInUserData(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s = "$(echo PWNED)"
			echo(s)
			let s2 = "`+"`"+`echo PWNED2`+"`"+`"
			echo(s2)
		}
	`)
	out := runShell(t, sh)
	if !strings.Contains(out, "$(echo PWNED)") || !strings.Contains(out, "`echo PWNED2`") {
		t.Errorf("user-data leaked into shell evaluation:\n%s", out)
	}
	if strings.Contains(out, "PWNED") && !strings.Contains(out, "$(echo PWNED)") {
		t.Errorf("PWNED actually executed:\n%s", out)
	}
}

// TestEmptyStringInterpolation: `${x}` with x=="" should produce nothing,
// not a syntax error or unset-variable diagnostic.
func TestEmptyStringInterpolation(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a = ""
			echo("[${a}]")
			let b = "x" + a
			echo("[" + b + "]")
		}
	`)
	out := runShell(t, sh)
	want := "[]\n[x]\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestRecursion: a function that calls itself.
func TestRecursion(t *testing.T) {
	sh := compile(t, `
		func fact(n: number): number {
			if n <= 1 { return 1 }
			return n * fact(n - 1)
		}
		func main(): void {
			echo(str(fact(6)))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "720" {
		t.Errorf("got %q", got)
	}
}

// TestMutualRecursion: two functions that call each other; the checker's
// forward-reference handling must allow this.
func TestMutualRecursion(t *testing.T) {
	sh := compile(t, `
		func isEven(n: number): bool {
			if n == 0 { return true }
			return isOdd(n - 1)
		}
		func isOdd(n: number): bool {
			if n == 0 { return false }
			return isEven(n - 1)
		}
		func main(): void {
			if isEven(8) { echo("even") }
			if isOdd(7) { echo("odd") }
		}
	`)
	out := runShell(t, sh)
	want := "even\nodd\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestDeeplyNestedIfs: 30 levels of nested if statements should still work.
// Catches both stack-overflow and indentation bugs in codegen.
func TestDeeplyNestedIfs(t *testing.T) {
	const depth = 30
	var b strings.Builder
	b.WriteString("func main(): void {\n")
	for i := 0; i < depth; i++ {
		b.WriteString(strings.Repeat("  ", i+1))
		b.WriteString("if true {\n")
	}
	b.WriteString(strings.Repeat("  ", depth+1))
	b.WriteString(`echo("deep")` + "\n")
	for i := depth - 1; i >= 0; i-- {
		b.WriteString(strings.Repeat("  ", i+1))
		b.WriteString("}\n")
	}
	b.WriteString("}\n")
	sh := compile(t, b.String())
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "deep" {
		t.Errorf("got %q", got)
	}
}

// TestLongChainOfStringConcat: 50 string concatenations.
func TestLongChainOfStringConcat(t *testing.T) {
	const n = 50
	parts := make([]string, n)
	for i := range parts {
		parts[i] = `"` + string(rune('a'+(i%26))) + `"`
	}
	src := `func main(): void { echo(` + strings.Join(parts, " + ") + `) }`
	sh := compile(t, src)
	out := runShell(t, sh)
	var want strings.Builder
	for i := 0; i < n; i++ {
		want.WriteByte(byte('a' + (i % 26)))
	}
	want.WriteByte('\n')
	if out != want.String() {
		t.Errorf("got %q want %q", out, want.String())
	}
}

// TestArrayWithSpacesAndGlobs: array elements that look like they could glob
// or word-split must be preserved verbatim through iteration and indexing.
func TestArrayWithSpacesAndGlobs(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs = ["one two three", "*.txt", "a$b"]
			for x in xs {
				echo("[" + x + "]")
			}
			echo("first=" + xs[0])
		}
	`)
	out := runShell(t, sh)
	want := "[one two three]\n[*.txt]\n[a$b]\nfirst=one two three\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestShReservedWordsAsIdentifiers: identifiers that are reserved in sh but
// not in tartalo (`local`, `function`, `then`, `done`, …) must be mangled in
// the generated script so the shell doesn't choke. shName() handles this.
func TestShReservedWordsAsIdentifiers(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let local = "loc"
			let function = "fun"
			let done = "fin"
			let case = "swap"
			echo(local + "/" + function + "/" + done + "/" + case)
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "loc/fun/fin/swap" {
		t.Errorf("got %q", got)
	}
}

// TestRecordWithManyFields: 20-field record. Catches off-by-one and
// performance bugs in record codegen.
func TestRecordWithManyFields(t *testing.T) {
	const n = 20
	var typeBuilder, litBuilder strings.Builder
	typeBuilder.WriteString("type Big = {\n")
	litBuilder.WriteString("Big{\n")
	for i := 0; i < n; i++ {
		typeBuilder.WriteString("  f" + itoa(i) + ": number,\n")
		litBuilder.WriteString("  f" + itoa(i) + ": " + itoa(i*2) + ",\n")
	}
	typeBuilder.WriteString("}\n")
	litBuilder.WriteString("}")

	src := typeBuilder.String() + `
		func main(): void {
			let x: Big = ` + litBuilder.String() + `
			echo(str(x.f0 + x.f10 + x.f19))
		}
	`
	sh := compile(t, src)
	out := runShell(t, sh)
	want := itoa(0+20+38) + "\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestNullStringAndNonNullEmpty: `null` and `""` are different observable
// things — the codegen must not collapse them.
func TestNullStringAndNonNullEmpty(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a: string? = null
			let b: string? = ""
			if a == null { echo("a-null") }
			if b == null { echo("b-null") }
			if a != null { echo("a-set") }
			if b != null { echo("b-set") }
			echo("[" + (b ?? "FALLBACK") + "]")
		}
	`)
	out := runShell(t, sh)
	want := "a-null\nb-set\n[]\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestStringInterpolationWithExpression: more than just identifiers in `${}`.
func TestStringInterpolationWithExpression(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let n = 7
			echo("n*n = ${n * n}")
			let s = "hi"
			echo("upper(s) interp ok: ${upper(s)}")
		}
	`)
	out := runShell(t, sh)
	want := "n*n = 49\nupper(s) interp ok: HI\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestEarlyReturn: a function with multiple returns; the codegen must not
// fall through past a return.
func TestEarlyReturn(t *testing.T) {
	sh := compile(t, `
		func first(s: string): string {
			if s == "" { return "(empty)" }
			return s + "!"
		}
		func main(): void {
			echo(first("hi"))
			echo(first(""))
		}
	`)
	out := runShell(t, sh)
	want := "hi!\n(empty)\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestVoidFunctionWithEarlyReturn: a void function that uses bare `return`.
func TestVoidFunctionWithEarlyReturn(t *testing.T) {
	sh := compile(t, `
		func bail(condition: bool): void {
			if condition { return }
			echo("not bailing")
		}
		func main(): void {
			bail(true)
			echo("after-true")
			bail(false)
			echo("after-false")
		}
	`)
	out := runShell(t, sh)
	want := "after-true\nnot bailing\nafter-false\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// itoa is a tiny helper to avoid pulling in strconv for tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
