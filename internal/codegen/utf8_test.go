package codegen_test

import (
	"strings"
	"testing"
)

func TestLenIsRuneCountAscii(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s: string = "hello"
			echo(str(len(s)))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "5" {
		t.Errorf("got %q want 5", got)
	}
}

func TestLenIsRuneCountUnicode(t *testing.T) {
	// "héllo" is 5 codepoints, 6 bytes (é = 0xC3 0xA9).
	sh := compile(t, `
		func main(): void {
			let s: string = "héllo"
			echo(str(len(s)))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "5" {
		t.Errorf("got %q want 5 (rune count, not byte count)", got)
	}
}

func TestByteLenAscii(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo(str(byteLen("hello")))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "5" {
		t.Errorf("got %q want 5", got)
	}
}

func TestByteLenUnicode(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo(str(byteLen("héllo")))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "6" {
		t.Errorf("got %q want 6 (byte count of UTF-8 héllo)", got)
	}
}

func TestSliceIsRuneAware(t *testing.T) {
	// "héllo" runes: h é l l o. slice(s, 0, 2) should be "hé", not "h\xC3" (broken).
	sh := compile(t, `
		func main(): void {
			let s: string = "héllo"
			echo(slice(s, 0, 2))
			echo(slice(s, 1, 3))
			echo(slice(s, 2, 5))
		}
	`)
	out := runShell(t, sh)
	want := "hé\nél\nllo\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestByteSliceCutsBytes(t *testing.T) {
	// byteSlice on ASCII matches normal slice; sanity-check the new builtin.
	sh := compile(t, `
		func main(): void {
			echo(byteSlice("hello", 0, 3))
			echo(byteSlice("hello", 2, 4))
		}
	`)
	out := runShell(t, sh)
	if out != "hel\nll\n" {
		t.Errorf("got %q", out)
	}
}

func TestSliceUnicodeMixedScript(t *testing.T) {
	// Mix Latin, Greek, CJK to exercise the 1, 2, and 3-byte UTF-8 lengths.
	// "aΩ漢" → 3 codepoints, slice(0, 2) should be "aΩ".
	sh := compile(t, `
		func main(): void {
			let s: string = "aΩ漢"
			echo(str(len(s)))
			echo(slice(s, 0, 2))
			echo(slice(s, 1, 3))
		}
	`)
	out := runShell(t, sh)
	want := "3\naΩ\nΩ漢\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestArrayLenStillCountsElements(t *testing.T) {
	// `len` should still count elements (not codepoints) on arrays.
	sh := compile(t, `
		func main(): void {
			let xs: string[] = ["héllo", "world"]
			echo(str(len(xs)))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "2" {
		t.Errorf("got %q want 2", got)
	}
}
