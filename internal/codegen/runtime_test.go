package codegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// runShellWithArgs is like runShell but passes positional args to the script.
func runShellWithArgs(t *testing.T, sh string, args ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.sh")
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	cmdArgs := append([]string{path}, args...)
	cmd := exec.Command("/bin/sh", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n--script--\n%s\n--out--\n%s", err, sh, out)
	}
	return string(out)
}

func TestArgsPassthrough(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a = args()
			echo("count=" + str(len(a)))
			for x in a { echo("[" + x + "]") }
		}
	`)
	out := runShellWithArgs(t, sh, "alpha", "beta", "gamma")
	want := "count=3\n[alpha]\n[beta]\n[gamma]\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestArgsEmpty(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a = args()
			echo("count=" + str(len(a)))
		}
	`)
	out := runShellWithArgs(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "count=0" {
		t.Errorf("got %q", got)
	}
}

func TestArgsWithSpacesAndSpecialChars(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			for x in args() { echo("[" + x + "]") }
		}
	`)
	out := runShellWithArgs(t, sh, "with space", "$(echo PWNED)", "back`tick`s")
	want := "[with space]\n[$(echo PWNED)]\n[back`tick`s]\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestArgsInsideHelperFunction(t *testing.T) {
	// Helper functions have their own `$@` (their params), so the snapshot
	// must come from a global, not from a positional re-read. This test
	// would catch any regression where args() looks at the wrong $@.
	sh := compile(t, `
		func describe(prefix: string): void {
			for x in args() { echo(prefix + x) }
		}
		func main(): void {
			describe(">> ")
		}
	`)
	out := runShellWithArgs(t, sh, "one", "two")
	want := ">> one\n>> two\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestNowReturnsAReasonableTimestamp(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let t = now()
			echo(str(t))
		}
	`)
	out := runShell(t, sh)
	got := strings.TrimRight(out, "\n")
	n, err := strconv.ParseInt(got, 10, 64)
	if err != nil {
		t.Fatalf("not an int: %q", got)
	}
	// Bound the timestamp so we know `date +%s` actually ran (i.e. the year
	// is between 2020 and the year 2200).
	if n < 1577836800 || n > 7258118400 {
		t.Errorf("timestamp out of expected range: %d", n)
	}
}

func TestSleepBlocks(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let before = now()
			sleep(1)
			let after = now()
			echo(str(after - before))
		}
	`)
	out := runShell(t, sh)
	got := strings.TrimRight(out, "\n")
	n, err := strconv.Atoi(got)
	if err != nil {
		t.Fatalf("not an int: %q", got)
	}
	// sleep(1) should take at least ~1 second; allow up to 5 to be lenient on
	// slow CI.
	if n < 1 || n > 5 {
		t.Errorf("sleep(1) measured as %ds", n)
	}
}

func TestFormatTimeFixedTimestamp(t *testing.T) {
	// Unix epoch second 0 is 1970-01-01 in UTC, but `date` on many systems
	// formats in local time. We pin the timezone via TZ to make the assertion
	// stable across machines.
	sh := compile(t, `
		func main(): void {
			let s = formatTime(0, "%Y-%m-%d %H:%M:%S")
			echo(s)
		}
	`)
	dir := t.TempDir()
	path := filepath.Join(dir, "out.sh")
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", path)
	cmd.Env = append(os.Environ(), "TZ=UTC")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "1970-01-01 00:00:00" {
		t.Errorf("got %q", got)
	}
}

func TestJsonGetSimpleFields(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	sh := compile(t, `
		func main(): void {
			let body = "{\"name\":\"alice\",\"age\":30,\"active\":true}"
			echo(jsonGet(body, ".name") ?? "MISSING")
			echo(jsonGet(body, ".age") ?? "MISSING")
			echo(jsonGet(body, ".active") ?? "MISSING")
			echo(jsonGet(body, ".missing") ?? "MISSING")
		}
	`)
	out := runShell(t, sh)
	want := "alice\n30\ntrue\nMISSING\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestJsonHas(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	sh := compile(t, `
		func main(): void {
			let body = "{\"a\":1,\"b\":null}"
			if jsonHas(body, ".a") { echo("a-yes") } else { echo("a-no") }
			if jsonHas(body, ".b") { echo("b-yes") } else { echo("b-no") }
			if jsonHas(body, ".c") { echo("c-yes") } else { echo("c-no") }
		}
	`)
	out := runShell(t, sh)
	// jq -e treats null as a "false" exit, so .b is "no" too — same as missing.
	want := "a-yes\nb-no\nc-no\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestJsonArray(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	sh := compile(t, `
		func main(): void {
			let body = "{\"tags\":[\"alpha\",\"beta\",\"gamma\"]}"
			let tags = jsonArray(body, ".tags")
			echo(str(len(tags)))
			for t in tags { echo("- " + t) }
		}
	`)
	out := runShell(t, sh)
	want := "3\n- alpha\n- beta\n- gamma\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestJsonEscape(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	sh := compile(t, `
		func main(): void {
			echo(jsonEscape("hello"))
			echo(jsonEscape("with \"quotes\""))
			echo(jsonEscape("with newline\n"))
		}
	`)
	out := runShell(t, sh)
	// jq encodes \n as a literal "\n" (two characters) in JSON form.
	want := "\"hello\"\n\"with \\\"quotes\\\"\"\n\"with newline\\n\"\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestJsonGetReturnsNullForJsonNull(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	sh := compile(t, `
		func main(): void {
			let body = "{\"a\":null}"
			let v = jsonGet(body, ".a")
			if v == null { echo("null") } else { echo("got: " + v!) }
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "null" {
		t.Errorf("got %q", got)
	}
}

func TestJsonRoundTripWithFetch(t *testing.T) {
	// Build a body with jsonEscape and verify jsonGet sees the string.
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	sh := compile(t, `
		func main(): void {
			let escaped = jsonEscape("user with spaces and \"quotes\"")
			let body = "{\"name\":" + escaped + "}"
			echo(jsonGet(body, ".name") ?? "MISSING")
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != `user with spaces and "quotes"` {
		t.Errorf("got %q", got)
	}
}
