package nativegen_test

import (
	"strings"
	"testing"
)

func TestNativeGenericIdentity(t *testing.T) {
	bin := build(t, `
		func id<T>(x: T): T { return x }
		func main(): void {
			echo(id("hello"))
			echo(str(id(42)))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "hello\n42" {
		t.Errorf("got %q", got)
	}
}

func TestNativeGenericArrayBuilder(t *testing.T) {
	bin := build(t, `
		func twice<T>(x: T): T[] {
			let pair: T[] = [x, x]
			return pair
		}
		func main(): void {
			let xs: number[] = twice(7)
			for x in xs { echo(str(x)) }
			let ys: string[] = twice("hi")
			for y in ys { echo(y) }
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "7\n7\nhi\nhi" {
		t.Errorf("got %q", got)
	}
}

func TestNativeGenericFirstOnRecordArray(t *testing.T) {
	bin := build(t, `
		type Person = { name: string, age: number }
		func first<T>(xs: T[]): T { return xs[0] }
		func main(): void {
			let nums: number[] = [10, 20, 30]
			echo(str(first(nums)))
			let people: Person[] = [
				Person{name: "Alice", age: 30},
				Person{name: "Bob",   age: 25},
			]
			let p: Person = first(people)
			echo(p.name + "/" + str(p.age))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "10\nAlice/30" {
		t.Errorf("got %q", got)
	}
}

func TestNativeGenericNestedInstantiation(t *testing.T) {
	bin := build(t, `
		func box<T>(x: T): T[] { return [x] }
		func wrap<U>(x: U): U[] { return box(x) }
		func main(): void {
			let xs: number[] = wrap(42)
			let ys: string[] = wrap("hi")
			echo(str(xs[0]))
			echo(ys[0])
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "42\nhi" {
		t.Errorf("got %q", got)
	}
}

func TestNativeGenericCoalesce(t *testing.T) {
	bin := build(t, `
		func or<T>(x: T?, fallback: T): T { return x ?? fallback }
		func main(): void {
			let a: string? = "hello"
			let b: string? = null
			echo(or(a, "fallback"))
			echo(or(b, "fallback"))
			let n: number? = null
			echo(str(or(n, 99)))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "hello\nfallback\n99" {
		t.Errorf("got %q", got)
	}
}
