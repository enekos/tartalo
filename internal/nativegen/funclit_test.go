package nativegen_test

import (
	"strings"
	"testing"
)

func TestNativeLambdaInVar(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let dbl: func(number): number = func(x: number): number { return x + x }
			echo(str(dbl(7)))
		}
	`)
	got := strings.TrimRight(runBin(t, bin), "\n")
	if got != "14" {
		t.Errorf("got %q", got)
	}
}

func TestNativeLambdaInMap(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3, 4]
			let ys: number[] = map(xs, func(x: number): number { return x * x })
			for y in ys { echo(str(y)) }
		}
	`)
	want := "1\n4\n9\n16\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeClosureCapturesLocal(t *testing.T) {
	// A closure that captures a local must work even after the defining
	// function returns — Go's native closure semantics carry the binding.
	bin := build(t, `
		func makeAdder(n: number): func(number): number {
			return func(x: number): number { return x + n }
		}
		func main(): void {
			let add5: func(number): number = makeAdder(5)
			let add10: func(number): number = makeAdder(10)
			echo(str(add5(3)))
			echo(str(add10(3)))
		}
	`)
	want := "8\n13\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeClosureWithFilter(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let limit: number = 3
			let xs: number[] = [1, 2, 3, 4, 5, 6]
			let small: number[] = filter(xs, func(x: number): bool { return x <= limit })
			for s in small { echo(str(s)) }
		}
	`)
	want := "1\n2\n3\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeNestedClosure(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let n: number = 100
			let xs: number[] = [1, 2, 3]
			let result: number = reduce(
				xs, 0,
				func(acc: number, x: number): number { return acc + x + n }
			)
			echo(str(result))
		}
	`)
	got := strings.TrimRight(runBin(t, bin), "\n")
	// 0 + (1+100) + (2+100) + (3+100) = 306
	if got != "306" {
		t.Errorf("got %q want 306", got)
	}
}
