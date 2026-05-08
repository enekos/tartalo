package nativegen_test

import (
	"strings"
	"testing"
)

func TestNativeWhileBasic(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let i: number = 0
			while i < 5 {
				echo(str(i))
				i = i + 1
			}
		}
	`)
	want := "0\n1\n2\n3\n4\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeWhileBreak(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let i: number = 0
			while true {
				if i == 3 { break }
				echo(str(i))
				i = i + 1
			}
			echo("after")
		}
	`)
	want := "0\n1\n2\nafter\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeWhileContinue(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let i: number = 0
			while i < 5 {
				i = i + 1
				if i % 2 == 0 { continue }
				echo(str(i))
			}
		}
	`)
	want := "1\n3\n5\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeForBreakContinue(t *testing.T) {
	bin := build(t, `
		func main(): void {
			for i in 0..6 {
				if i == 5 { break }
				if i % 2 == 0 { continue }
				echo(str(i))
			}
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "1\n3" {
		t.Errorf("got %q", got)
	}
}
