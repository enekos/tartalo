package codegen_test

import (
	"testing"
)

func TestSumUnitVariant(t *testing.T) {
	sh := compile(t, `
		type Color = Red | Green | Blue
		func main(): void {
			let c: Color = Green
			match c {
				Red => echo("r")
				Green => echo("g")
				Blue => echo("b")
			}
		}
	`)
	out := runShell(t, sh)
	if out != "g\n" {
		t.Errorf("got %q want %q", out, "g\n")
	}
}

func TestSumPayloadAndBindings(t *testing.T) {
	sh := compile(t, `
		type Shape =
		  Circle{r: number}
		  | Rectangle{w: number, h: number}
		  | Empty

		func area(s: Shape): number {
			match s {
				Circle{r} => return r * r * 3
				Rectangle{w, h} => return w * h
				Empty => return 0
			}
			return -1
		}

		func main(): void {
			echo(str(area(Circle{r: 4})))
			echo(str(area(Rectangle{w: 5, h: 6})))
			echo(str(area(Empty)))
		}
	`)
	out := runShell(t, sh)
	want := "48\n30\n0\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestSumPassByValue(t *testing.T) {
	sh := compile(t, `
		type Maybe = Some{v: number} | None
		func describe(m: Maybe): string {
			match m {
				Some{v} => return "got " + str(v)
				None => return "nothing"
			}
			return "?"
		}
		func main(): void {
			echo(describe(Some{v: 7}))
			echo(describe(None))
		}
	`)
	out := runShell(t, sh)
	want := "got 7\nnothing\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestSumReturned(t *testing.T) {
	sh := compile(t, `
		type Step = Done | Next{n: number}
		func bump(x: number): Step {
			if x >= 3 { return Done }
			return Next{n: x + 1}
		}
		func main(): void {
			let s: Step = bump(1)
			match s {
				Done => echo("done")
				Next{n} => echo("next:" + str(n))
			}
		}
	`)
	out := runShell(t, sh)
	if out != "next:2\n" {
		t.Errorf("got %q want %q", out, "next:2\n")
	}
}

func TestSumStringPayload(t *testing.T) {
	sh := compile(t, `
		type Event = Click{at: string} | Quit
		func main(): void {
			let e: Event = Click{at: "10,20"}
			match e {
				Click{at} => echo("click@" + at)
				Quit => echo("quit")
			}
		}
	`)
	out := runShell(t, sh)
	if out != "click@10,20\n" {
		t.Errorf("got %q want %q", out, "click@10,20\n")
	}
}

func TestSumMatchWildcard(t *testing.T) {
	sh := compile(t, `
		type Tag = A | B | C
		func name(t: Tag): string {
			match t {
				A => return "a"
				_ => return "other"
			}
			return "?"
		}
		func main(): void {
			echo(name(A))
			echo(name(B))
		}
	`)
	out := runShell(t, sh)
	want := "a\nother\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}
