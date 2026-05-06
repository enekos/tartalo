package codegen_test

import (
	"strings"
	"testing"
)

func TestPipelineSingleArg(t *testing.T) {
	sh := compile(t, `
		func double(x: number): number { return x * 2 }
		func main(): void {
			let result: number = 5 |> double()
			echo(str(result))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "10" {
		t.Errorf("got %q", got)
	}
}

func TestPipelineMultiArg(t *testing.T) {
	sh := compile(t, `
		func add(a: number, b: number): number { return a + b }
		func main(): void {
			echo(str(7 |> add(3)))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "10" {
		t.Errorf("got %q", got)
	}
}

func TestPipelineChained(t *testing.T) {
	sh := compile(t, `
		func double(x: number): number { return x * 2 }
		func plus(x: number, y: number): number { return x + y }
		func main(): void {
			echo(str(3 |> double() |> plus(1)))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "7" {
		t.Errorf("got %q", got)
	}
}

func TestPipelineBareIdent(t *testing.T) {
	sh := compile(t, `
		func double(x: number): number { return x * 2 }
		func main(): void {
			echo(str(4 |> double))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "8" {
		t.Errorf("got %q", got)
	}
}

func TestPipelineWithStrings(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo("HELLO" |> lower)
			echo("hello" |> upper)
		}
	`)
	out := runShell(t, sh)
	want := "hello\nHELLO\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}
