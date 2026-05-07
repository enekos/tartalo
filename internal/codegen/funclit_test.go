package codegen_test

import (
	"strings"
	"testing"
)

func TestLambdaInVarThenCall(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let dbl: func(number): number = func(x: number): number { return x + x }
			echo(str(dbl(7)))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "14" {
		t.Errorf("got %q", got)
	}
}

func TestLambdaPassedToMap(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3, 4]
			let ys: number[] = map(xs, func(x: number): number { return x * x })
			for y in ys {
				echo(str(y))
			}
		}
	`)
	want := "1\n4\n9\n16\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestLambdaWithFilter(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3, 4, 5, 6]
			let evens: number[] = filter(xs, func(x: number): bool { return x % 2 == 0 })
			for e in evens {
				echo(str(e))
			}
		}
	`)
	want := "2\n4\n6\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestLambdaWithReduce(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3, 4, 5]
			let sum: number = reduce(xs, 0, func(acc: number, x: number): number { return acc + x })
			echo(str(sum))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "15" {
		t.Errorf("got %q", got)
	}
}

func TestLambdaCapturesViaDynamicScope(t *testing.T) {
	// On the sh target, captures work as long as the lambda is invoked
	// while the defining frame is still on the dynamic call stack — which
	// is the case for map() since it runs inline, before the enclosing
	// function returns.
	sh := compile(t, `
		func main(): void {
			let n: number = 10
			let xs: number[] = [1, 2, 3]
			let ys: number[] = map(xs, func(x: number): number { return x + n })
			for y in ys {
				echo(str(y))
			}
		}
	`)
	want := "11\n12\n13\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
