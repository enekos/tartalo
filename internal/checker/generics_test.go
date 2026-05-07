package checker_test

import "testing"

func TestGenericInferenceFromArg(t *testing.T) {
	wantOk(t, `
		func id<T>(x: T): T { return x }
		func main(): void {
			let a: string = id("hi")
			let b: number = id(7)
			echo(a)
			echo(str(b))
		}
	`)
}

func TestGenericRejectIncompatibleInference(t *testing.T) {
	wantError(t, `
		func first<T>(a: T, b: T): T { return a }
		func main(): void {
			echo(first("a", 1))
		}
	`, "argument 2")
}

func TestGenericRejectArithmeticOnTypeVar(t *testing.T) {
	wantError(t, `
		func add<T>(a: T, b: T): T { return a + b }
		func main(): void {}
	`, "+ requires both operands to be numeric or both to be string")
}

func TestGenericRejectUninferableTypeParam(t *testing.T) {
	wantError(t, `
		func nope<T>(): T {
			let x: number = 1
			return x
		}
		func main(): void {}
	`, "return type mismatch")
}

func TestGenericArrayElement(t *testing.T) {
	wantOk(t, `
		func first<T>(xs: T[]): T { return xs[0] }
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let n: number = first(xs)
			echo(str(n))
		}
	`)
}

func TestGenericRejectDuplicateTypeParam(t *testing.T) {
	wantError(t, `
		func id<T, T>(x: T, y: T): T { return x }
	`, "duplicate type parameter")
}

func TestGenericInferenceWithFunction(t *testing.T) {
	wantOk(t, `
		func plusOne(n: number): number { return n + 1 }
		func apply<T>(x: T, f: func(T): T): T { return f(x) }
		func main(): void {
			let n: number = apply(7, plusOne)
			echo(str(n))
		}
	`)
}

func TestGenericInferenceNullArg(t *testing.T) {
	wantOk(t, `
		func or<T>(x: T?, fallback: T): T { return x ?? fallback }
		func main(): void {
			let s: string? = "hi"
			let v: string = or(s, "x")
			echo(v)
		}
	`)
}
