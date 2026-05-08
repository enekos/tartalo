package codegen_test

import (
	"strings"
	"testing"
)

func TestWhileBasic(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let i: number = 0
			while i < 5 {
				echo(str(i))
				i = i + 1
			}
		}
	`)
	out := runShell(t, sh)
	want := "0\n1\n2\n3\n4\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestWhileFalseSkipsBody(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			while false {
				echo("never")
			}
			echo("done")
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "done" {
		t.Errorf("got %q", got)
	}
}

func TestWhileBreak(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let i: number = 0
			while true {
				if i == 3 {
					break
				}
				echo(str(i))
				i = i + 1
			}
			echo("after")
		}
	`)
	out := runShell(t, sh)
	want := "0\n1\n2\nafter\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestWhileContinue(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let i: number = 0
			while i < 5 {
				i = i + 1
				if i % 2 == 0 {
					continue
				}
				echo(str(i))
			}
		}
	`)
	out := runShell(t, sh)
	want := "1\n3\n5\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestForBreak(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			for i in 0..10 {
				if i == 3 {
					break
				}
				echo(str(i))
			}
		}
	`)
	out := runShell(t, sh)
	want := "0\n1\n2\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestForContinue(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			for i in 0..6 {
				if i % 2 == 0 {
					continue
				}
				echo(str(i))
			}
		}
	`)
	out := runShell(t, sh)
	want := "1\n3\n5\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestWhileWithCallInCond(t *testing.T) {
	// Cond involves a function call, ensure prologue runs each iteration.
	sh := compile(t, `
		let counter: number = 0
		func nextAndKeepGoing(): bool {
			counter = counter + 1
			return counter < 4
		}
		func main(): void {
			while nextAndKeepGoing() {
				echo(str(counter))
			}
			echo("final=" + str(counter))
		}
	`)
	out := runShell(t, sh)
	want := "1\n2\n3\nfinal=4\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestNestedWhileBreakOnlyInner(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let i: number = 0
			while i < 3 {
				let j: number = 0
				while j < 3 {
					if j == 1 {
						break
					}
					echo(str(i) + "," + str(j))
					j = j + 1
				}
				i = i + 1
			}
		}
	`)
	out := runShell(t, sh)
	want := "0,0\n1,0\n2,0\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}
