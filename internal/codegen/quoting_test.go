package codegen_test

import (
	"strings"
	"testing"
)

// TestNoInjectionViaCommandLiteralInterpolation: inside a `cmd ${var}`
// literal, even if `var` looks like a command, it must be passed as a single
// argument to the cmd, not re-evaluated.
func TestNoInjectionViaCommandLiteralInterpolation(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let evil = "; echo PWNED"
			let out = ` + "`" + `printf '%s\n' ${evil}` + "`" + `
			echo("got: " + out)
		}
	`)
	out := runShell(t, sh)
	if strings.Contains(out, "PWNED") && !strings.Contains(out, "; echo PWNED") {
		t.Fatalf("PWNED actually executed:\n%s", out)
	}
	want := "got: ; echo PWNED\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestNoInjectionViaArrayElement: array elements that look like commands
// must not execute when iterated.
func TestNoInjectionViaArrayElement(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs = ["safe", "$(echo PWNED)", "` + "`" + `echo PWNED2` + "`" + `"]
			for x in xs {
				echo("- " + x)
			}
		}
	`)
	out := runShell(t, sh)
	want := "- safe\n- $(echo PWNED)\n- `echo PWNED2`\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestNoInjectionViaRecordField: same idea, through a record field.
func TestNoInjectionViaRecordField(t *testing.T) {
	sh := compile(t, `
		type S = { val: string }
		func main(): void {
			let s: S = S{val: "$(echo PWNED)"}
			echo(s.val)
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "$(echo PWNED)" {
		t.Errorf("PWNED leaked: %q", got)
	}
}

// TestNewlinesInString: literal newlines from \n escape preserve through
// echo without being split.
func TestNewlinesInString(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s = "line1\nline2\nline3"
			echo(s)
			echo("len=" + str(len(s)))
		}
	`)
	out := runShell(t, sh)
	want := "line1\nline2\nline3\nlen=17\n" // 5+1+5+1+5 = 17
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestEmojiAndUnicode: multi-byte UTF-8 round-trips through the compiler.
func TestEmojiAndUnicode(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s = "héllo, 世界 🌍"
			echo(s)
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "héllo, 世界 🌍" {
		t.Errorf("got %q", got)
	}
}

// TestLeadingTrailingDashInString: a string that begins with `-` shouldn't
// confuse downstream commands. We test this via the fact that
// `printf '%s\n' "-n"` correctly echoes `-n` (with the `--` argument
// terminator the codegen does NOT use, so this is a regression-not-yet
// guard: if someone changes echo to use `printf '%s\n' "$@"`, the leading
// `-` would be interpreted by some printfs).
func TestLeadingTrailingDashInString(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo("-n")
			echo("--help")
		}
	`)
	out := runShell(t, sh)
	want := "-n\n--help\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestStringWithBackslashes: backslashes must round-trip as literal characters.
func TestStringWithBackslashes(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo("path\\to\\file")
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "path\\to\\file" {
		t.Errorf("got %q", got)
	}
}

// TestUserDataInRegexLikePosition: replace() takes literal substrings, not
// regex. Make sure regex metacharacters in either argument stay literal.
func TestUserDataInRegexLikePosition(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo(replace("a.b.c", ".", "X"))
			echo(replace("[brackets]", "[", "<"))
			echo(replace("a+b", "+", "PLUS"))
			echo(replace("abc", "*", "WILD"))
		}
	`)
	out := runShell(t, sh)
	want := "aXbXc\n<brackets]\naPLUSb\nabc\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestSplitWithRegexyDelimiter: same idea for split.
func TestSplitWithRegexyDelimiter(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let parts = split("a.b.c", ".")
			echo(str(len(parts)))
			echo(join(parts, "/"))
		}
	`)
	out := runShell(t, sh)
	want := "3\na/b/c\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestExecCommandWithUserData: when the user invokes exec with data they
// didn't construct, the data flows verbatim into `sh -c` — so the user IS
// expected to escape it themselves. This is documented; the test is a
// regression net so we don't accidentally start escaping.
func TestExecPassesCommandStringVerbatim(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let r = exec("printf hello | tr a-z A-Z")
			echo(r.stdout)
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "HELLO" {
		t.Errorf("got %q", got)
	}
}

// TestArrayElementWithEqualsSign: an element like "k=v" must not be
// interpreted as a sh assignment when iterated.
func TestArrayElementWithEqualsSign(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs = ["KEY=value", "FOO=$(echo PWNED)"]
			for x in xs {
				echo(x)
			}
		}
	`)
	out := runShell(t, sh)
	want := "KEY=value\nFOO=$(echo PWNED)\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestStringContainsNewline: explicit newline in string (via \n) interacts
// correctly with array operations like split.
func TestSplitOnEmbeddedNewline(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s = "a\nb\nc"
			let parts = split(s, "\n")
			echo(str(len(parts)))
			for p in parts { echo("- " + p) }
		}
	`)
	out := runShell(t, sh)
	want := "3\n- a\n- b\n- c\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}
