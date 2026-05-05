package checker_test

import (
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/parser"
)

func check(t *testing.T, src string) []error {
	t.Helper()
	toks, lerrs := lexer.New("t.tt", src).Tokenize()
	if len(lerrs) > 0 {
		t.Fatalf("lex errors: %v", lerrs)
	}
	file, perrs := parser.New(toks).Parse("t.tt")
	if len(perrs) > 0 {
		t.Fatalf("parse errors: %v", perrs)
	}
	_, cerrs := checker.New().CheckFile(file)
	return cerrs
}

func wantError(t *testing.T, src, contains string) {
	t.Helper()
	errs := check(t, src)
	if len(errs) == 0 {
		t.Fatalf("expected an error containing %q, got none", contains)
	}
	for _, e := range errs {
		if strings.Contains(e.Error(), contains) {
			return
		}
	}
	t.Fatalf("expected error containing %q, got: %v", contains, errs)
}

func wantOk(t *testing.T, src string) {
	t.Helper()
	errs := check(t, src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestRejectStringPlusNumber(t *testing.T) {
	wantError(t, `let x: string = "a" + 1`,
		"+ requires both operands to be numeric or both to be string")
}

func TestRejectAnnotationMismatch(t *testing.T) {
	wantError(t, `let x: number = "hi"`, "type mismatch")
}

func TestRejectUndefined(t *testing.T) {
	wantError(t, `let x: number = nope`, `undefined name "nope"`)
}

func TestRejectArityMismatch(t *testing.T) {
	wantError(t, `
		func id(s: string): string { return s }
		func main(): void { echo(id()) }
	`, `expects 1 argument`)
}

func TestRejectArgTypeMismatch(t *testing.T) {
	wantError(t, `
		func main(): void { echo(42) }
	`, `argument 1 to "echo"`)
}

func TestRejectAssignToConst(t *testing.T) {
	wantError(t, `
		func main(): void {
			const k: number = 1
			k = 2
		}
	`, `cannot assign to const`)
}

func TestRejectVoidReturn(t *testing.T) {
	wantError(t, `
		func main(): void { return 1 }
	`, `void function cannot return a value`)
}

func TestRejectMissingReturnValue(t *testing.T) {
	wantError(t, `
		func f(): number { return }
		func main(): void {}
	`, `function returns number, return statement has no value`)
}

func TestAcceptForwardReference(t *testing.T) {
	// main calls f before f is declared in source order
	wantOk(t, `
		func main(): void { echo(f()) }
		func f(): string { return "ok" }
	`)
}

func TestAcceptStringConcat(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let a: string = "a"
			let b: string = "b"
			let c: string = a + b
			echo(c)
		}
	`)
}

func TestAcceptComplexBoolExpr(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let x: number = 5
			let ok: bool = (x > 1 && x < 10) || x == 0
			if ok { echo("yes") }
		}
	`)
}

func TestAcceptStringOrdering(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let a: string = "a"
			let b: string = "b"
			if a < b { echo("yes") }
		}
	`)
}

func TestRejectMixedComparison(t *testing.T) {
	wantError(t, `
		func main(): void {
			let a: string = "a"
			let n: number = 1
			if a < n { echo("nope") }
		}
	`, `requires numeric or string operands`)
}

// --- Optionals & null ------------------------------------------------------

func TestAcceptNullToOptional(t *testing.T) {
	wantOk(t, `func main(): void { let x: string? = null }`)
}

func TestAcceptAutoWrapStringToOptional(t *testing.T) {
	wantOk(t, `func main(): void { let x: string? = "hi" }`)
}

func TestRejectNullToNonOptional(t *testing.T) {
	wantError(t, `func main(): void { let x: string = null }`, "type mismatch")
}

func TestRejectInferFromBareNull(t *testing.T) {
	wantError(t, `func main(): void { let x = null }`, "cannot infer type")
}

// Note: `T??` is a defensive guard in the checker; the parser can't produce
// a nested *ast.OptionalType from source today (the second `?` is consumed as
// the start of the `??` coalesce operator). The check is kept as a safety net
// in case the grammar evolves.

func TestRejectOptionalArray(t *testing.T) {
	wantError(t, `func main(): void { let x: string[]? = null }`, "optional arrays")
}

func TestRejectArrayOfOptional(t *testing.T) {
	wantError(t, `func main(): void { let x: string?[] = [] }`, "arrays of optionals")
}

func TestRejectVoidOptional(t *testing.T) {
	wantError(t, `func main(): void { let x: void? = null }`, "void cannot be made optional")
}

// --- ?? coalesce ------------------------------------------------------------

func TestAcceptCoalesceWithDefault(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let x: string? = null
			let y: string = x ?? "default"
			echo(y)
		}
	`)
}

func TestRejectCoalesceOnNonOptional(t *testing.T) {
	wantError(t, `
		func main(): void {
			let x: string = "hi"
			let y: string = x ?? "default"
		}
	`, "?? requires an optional")
}

func TestRejectCoalesceWithNull(t *testing.T) {
	wantError(t, `
		func main(): void {
			let x: string? = null
			let y: string = x ?? null
		}
	`, "?? right-hand side cannot be null")
}

func TestRejectCoalesceTypeMismatch(t *testing.T) {
	wantError(t, `
		func main(): void {
			let x: string? = null
			let y: string = x ?? 42
		}
	`, "?? type mismatch")
}

// --- ! unwrap ---------------------------------------------------------------

func TestAcceptUnwrapOptional(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let x: string? = "hi"
			let y: string = x!
			echo(y)
		}
	`)
}

func TestRejectUnwrapNonOptional(t *testing.T) {
	wantError(t, `
		func main(): void {
			let x: string = "hi"
			let y: string = x!
		}
	`, "! requires an optional")
}

// --- Null comparisons -------------------------------------------------------

func TestAcceptOptionalEqNull(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let x: string? = null
			if x == null { echo("nil") }
			if null != x { echo("set") }
		}
	`)
}

func TestRejectNonOptionalEqNull(t *testing.T) {
	wantError(t, `
		func main(): void {
			let x: string = "hi"
			if x == null { echo("?") }
		}
	`, "only optional types are nullable")
}

func TestRejectOptionalDirectEquality(t *testing.T) {
	wantError(t, `
		func main(): void {
			let a: string? = "hi"
			let b: string? = "bye"
			if a == b { echo("?") }
		}
	`, "cannot use == on optional values directly")
}

// --- Records ---------------------------------------------------------------

func TestAcceptRecordDeclAndUse(t *testing.T) {
	wantOk(t, `
		type Point = { x: number, y: number }
		func main(): void {
			let p: Point = Point{x: 1, y: 2}
			echo(str(p.x))
		}
	`)
}

func TestRejectMissingRecordField(t *testing.T) {
	wantError(t, `
		type Point = { x: number, y: number }
		func main(): void {
			let p: Point = Point{x: 1}
		}
	`, `missing field "y"`)
}

func TestRejectUnknownRecordField(t *testing.T) {
	wantError(t, `
		type Point = { x: number, y: number }
		func main(): void {
			let p: Point = Point{x: 1, y: 2, z: 3}
		}
	`, `has no field "z"`)
}

func TestRejectDuplicateRecordFieldInLiteral(t *testing.T) {
	wantError(t, `
		type Point = { x: number, y: number }
		func main(): void {
			let p: Point = Point{x: 1, x: 2, y: 3}
		}
	`, "duplicate field")
}

func TestRejectDuplicateRecordFieldInDecl(t *testing.T) {
	wantError(t, `
		type Point = { x: number, x: number }
		func main(): void {}
	`, "duplicate field")
}

func TestRejectFieldAccessOnNonRecord(t *testing.T) {
	wantError(t, `
		func main(): void {
			let s: string = "hi"
			echo(s.length)
		}
	`, "field access requires a record")
}

func TestRejectUnknownFieldAccess(t *testing.T) {
	wantError(t, `
		type Point = { x: number, y: number }
		func main(): void {
			let p: Point = Point{x: 1, y: 2}
			echo(str(p.z))
		}
	`, `has no field "z"`)
}

func TestRejectAnonymousRecordType(t *testing.T) {
	wantError(t, `
		func main(): void {
			let p: { x: number } = Point{x: 1}
		}
	`, "anonymous record types are not supported")
}

func TestRejectRecordOfRecordField(t *testing.T) {
	wantError(t, `
		type Inner = { v: number }
		type Outer = { i: Inner }
		func main(): void {}
	`, "v0 records only support primitive fields")
}

func TestAcceptOptionalPrimitiveField(t *testing.T) {
	wantOk(t, `
		type User = { name: string, nickname: string? }
		func main(): void {
			let u: User = User{name: "alice", nickname: null}
			echo(u.name)
		}
	`)
}

func TestRejectFieldAssignNonRecord(t *testing.T) {
	wantError(t, `
		func main(): void {
			let s: string = "hi"
			s.x = 1
		}
	`, "field assignment requires a record")
}

func TestAcceptFieldAssignment(t *testing.T) {
	wantOk(t, `
		type Point = { x: number, y: number }
		func main(): void {
			let p: Point = Point{x: 0, y: 0}
			p.x = 5
		}
	`)
}

func TestRejectFieldAssignTypeMismatch(t *testing.T) {
	wantError(t, `
		type Point = { x: number, y: number }
		func main(): void {
			let p: Point = Point{x: 0, y: 0}
			p.x = "five"
		}
	`, `expected number, got string`)
}

func TestRejectRedeclareType(t *testing.T) {
	wantError(t, `
		type T = { x: number }
		type T = { y: number }
		func main(): void {}
	`, "redeclaration of type")
}

func TestRejectRedeclarePredeclaredType(t *testing.T) {
	wantError(t, `
		type Response = { x: number }
		func main(): void {}
	`, "cannot redeclare predeclared type")
}

// --- Arrays ----------------------------------------------------------------

func TestAcceptArrayLitAndIndex(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3]
			echo(str(xs[0]))
		}
	`)
}

func TestRejectMixedArrayElements(t *testing.T) {
	wantError(t, `
		func main(): void {
			let xs = [1, "two", 3]
		}
	`, `array element 2: expected number, got string`)
}

func TestRejectInferEmptyArray(t *testing.T) {
	wantError(t, `
		func main(): void {
			let xs = []
		}
	`, "cannot infer type of empty array")
}

func TestAcceptAnnotatedEmptyArray(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let xs: string[] = []
			echo(str(len(xs)))
		}
	`)
}

func TestRejectIndexWithNonNumber(t *testing.T) {
	wantError(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3]
			echo(str(xs["zero"]))
		}
	`, "index must be number")
}

func TestRejectIndexNonArray(t *testing.T) {
	wantError(t, `
		func main(): void {
			let n: number = 5
			echo(str(n[0]))
		}
	`, "indexing requires an array")
}

func TestRejectArrayOfRecord(t *testing.T) {
	wantError(t, `
		type P = { x: number }
		func main(): void {
			let xs: P[] = []
		}
	`, "arrays of records")
}

// --- For-in ----------------------------------------------------------------

func TestAcceptForOverRange(t *testing.T) {
	wantOk(t, `
		func main(): void {
			for i in 0..10 { echo(str(i)) }
		}
	`)
}

func TestAcceptForOverArray(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3]
			for x in xs { echo(str(x)) }
		}
	`)
}

func TestAcceptForOverString(t *testing.T) {
	wantOk(t, `
		func main(): void {
			for c in "abc" { echo(c) }
		}
	`)
}

func TestRejectForOverNonIterable(t *testing.T) {
	wantError(t, `
		func main(): void {
			let n: number = 5
			for x in n { echo(str(x)) }
		}
	`, "for-in iterable must be a range, array, or string")
}

func TestRejectRangeWithNonNumber(t *testing.T) {
	wantError(t, `
		func main(): void {
			for i in "a".."z" { echo(i) }
		}
	`, "range start must be number")
}

func TestRejectBareRangeOutsideFor(t *testing.T) {
	wantError(t, `
		func main(): void {
			let r = 1..10
		}
	`, "range expression is only allowed as a for-in iterator")
}

// --- Match -----------------------------------------------------------------

func TestAcceptMatchStringSubject(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let s: string = "go"
			match s {
				"go" | "run" => echo("running")
				"" => echo("empty")
				_ => echo("other")
			}
		}
	`)
}

func TestAcceptMatchNumberSubject(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let n: number = 1
			match n {
				1 => echo("one")
				_ => echo("other")
			}
		}
	`)
}

func TestRejectMatchFloatSubject(t *testing.T) {
	wantError(t, `
		func main(): void {
			let f: float = floatOf(1)
			match f {
				_ => echo("any")
			}
		}
	`, "match subject must be a primitive (string, number, bool)")
}

func TestRejectMatchPatternTypeMismatch(t *testing.T) {
	wantError(t, `
		func main(): void {
			let n: number = 1
			match n {
				"one" => echo("?")
				_ => echo("else")
			}
		}
	`, "pattern type string does not match subject type number")
}

func TestRejectMatchPatternWithInterpolation(t *testing.T) {
	wantError(t, `
		func main(): void {
			let s: string = "x"
			let v: string = "y"
			match s {
				"hello ${v}" => echo("greet")
				_ => echo("else")
			}
		}
	`, "match pattern strings cannot contain interpolations")
}

// --- Higher-order builtins -------------------------------------------------

func TestAcceptMap(t *testing.T) {
	wantOk(t, `
		func double(n: number): number { return n * 2 }
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let ys: number[] = map(xs, double)
			echo(str(ys[0]))
		}
	`)
}

func TestRejectMapWrongArity(t *testing.T) {
	wantError(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let ys: number[] = map(xs)
		}
	`, "map expects 2 arguments")
}

func TestRejectMapNonArray(t *testing.T) {
	wantError(t, `
		func double(n: number): number { return n * 2 }
		func main(): void {
			let s: string = "hi"
			let ys: number[] = map(s, double)
		}
	`, "map: first argument must be an array")
}

func TestRejectMapWrongFuncSignature(t *testing.T) {
	wantError(t, `
		func parse(s: string): number { return num(s) }
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let ys: number[] = map(xs, parse)
		}
	`, "map: function must take one parameter of type number")
}

func TestAcceptFilter(t *testing.T) {
	wantOk(t, `
		func isPositive(n: number): bool { return n > 0 }
		func main(): void {
			let xs: number[] = [-1, 1, -2, 2]
			let ys: number[] = filter(xs, isPositive)
			echo(str(len(ys)))
		}
	`)
}

func TestRejectFilterNonBoolPredicate(t *testing.T) {
	wantError(t, `
		func double(n: number): number { return n * 2 }
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let ys: number[] = filter(xs, double)
		}
	`, "filter: predicate must return bool")
}

func TestAcceptReduce(t *testing.T) {
	wantOk(t, `
		func add(acc: number, x: number): number { return acc + x }
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let total: number = reduce(xs, 0, add)
			echo(str(total))
		}
	`)
}

func TestRejectReduceWrongArity(t *testing.T) {
	wantError(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let total: number = reduce(xs, 0)
		}
	`, "reduce expects 3 arguments")
}

func TestRejectReduceMismatchedAccumulator(t *testing.T) {
	wantError(t, `
		func add(acc: string, x: number): string { return acc + str(x) }
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let total: string = reduce(xs, 0, add)
		}
	`, "initial value type")
}

// --- str / len polymorphism ------------------------------------------------

func TestAcceptStrOfNumber(t *testing.T) {
	wantOk(t, `func main(): void { echo(str(42)) }`)
}

func TestAcceptStrOfBool(t *testing.T) {
	wantOk(t, `func main(): void { echo(str(true)) }`)
}

func TestRejectStrOfString(t *testing.T) {
	wantError(t, `func main(): void { echo(str("hi")) }`,
		"str requires a number, float, or bool")
}

func TestAcceptLenOfArrayAndString(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3]
			echo(str(len(xs)))
			echo(str(len("abc")))
		}
	`)
}

func TestRejectLenOfNumber(t *testing.T) {
	wantError(t, `func main(): void { echo(str(len(42))) }`,
		"len requires a string or array")
}

// --- Test builtins context restriction -------------------------------------

func TestAcceptAssertEqInsideTest(t *testing.T) {
	wantOk(t, `
		test "x" { assertEq(1, 1) }
		func main(): void {}
	`)
}

func TestRejectAssertEqOutsideTest(t *testing.T) {
	wantError(t, `
		func main(): void { assertEq(1, 1) }
	`, `assertEq may only be called inside a `+"`test"+` `)
}

func TestRejectAssertEqMismatchedTypes(t *testing.T) {
	wantError(t, `
		test "x" { assertEq(1, "one") }
		func main(): void {}
	`, "must have the same type")
}

func TestAcceptAssertEqNumberFloat(t *testing.T) {
	// Numeric widening is allowed for assert: comparing int 1 to float 1.0.
	wantOk(t, `
		test "x" { assertEq(1, floatOf(1)) }
		func main(): void {}
	`)
}

func TestRejectCheckNonBool(t *testing.T) {
	wantError(t, `
		test "x" { check(1) }
		func main(): void {}
	`, "check: argument must be bool")
}

func TestRejectFailNonString(t *testing.T) {
	wantError(t, `
		test "x" { fail(42) }
		func main(): void {}
	`, "fail: argument must be string")
}

func TestRejectDuplicateTestNames(t *testing.T) {
	wantError(t, `
		test "same" { check(true) }
		test "same" { check(true) }
		func main(): void {}
	`, "duplicate test name")
}

// --- Numeric coercion / float ----------------------------------------------

func TestAcceptNumberToFloat(t *testing.T) {
	wantOk(t, `func main(): void { let f: float = 1 }`)
}

func TestAcceptIntPlusFloat(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let f: float = 1 + floatOf(2)
		}
	`)
}

func TestRejectFloatModulo(t *testing.T) {
	wantError(t, `
		func main(): void {
			let f: float = floatOf(5)
			let r: number = floor(f) % 2
			let bad: float = f % 2
		}
	`, "use intOf() to truncate")
}

func TestRejectFloatToNumberAssign(t *testing.T) {
	wantError(t, `
		func main(): void {
			let f: float = floatOf(1)
			let n: number = f
		}
	`, "type mismatch")
}

// --- Equality / comparison --------------------------------------------------

func TestRejectEqDifferentTypes(t *testing.T) {
	wantError(t, `
		func main(): void {
			let a: string = "1"
			let n: number = 1
			if a == n { echo("?") }
		}
	`, "requires operands of the same type")
}

func TestAcceptCrossNumericEquality(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let n: number = 1
			let f: float = floatOf(1)
			if n == f { echo("eq") }
		}
	`)
}

// --- Unary -----------------------------------------------------------------

func TestRejectBangOnNumber(t *testing.T) {
	wantError(t, `
		func main(): void {
			let n: number = 1
			if !n { echo("?") }
		}
	`, "unary ! requires bool")
}

func TestRejectMinusOnString(t *testing.T) {
	wantError(t, `
		func main(): void {
			let s: string = "hi"
			let n: number = -s
		}
	`, "unary - requires numeric")
}

// --- If condition ----------------------------------------------------------

func TestRejectNonBoolIfCondition(t *testing.T) {
	wantError(t, `
		func main(): void {
			let n: number = 1
			if n { echo("?") }
		}
	`, "if condition must be bool")
}

// --- Function calls --------------------------------------------------------

func TestRejectCallNonFunction(t *testing.T) {
	wantError(t, `
		func main(): void {
			let x: number = 1
			x()
		}
	`, "is not a function")
}

func TestRejectIndirectCall(t *testing.T) {
	// v0 only allows direct (named) calls; calling an expression that produces
	// a function value isn't supported.
	wantError(t, `
		type T = { x: number }
		func main(): void {
			let t: T = T{x: 1}
			t.x()
		}
	`, "only direct function calls are supported")
}

func TestRejectAssignToFunction(t *testing.T) {
	wantError(t, `
		func f(): number { return 1 }
		func main(): void {
			f = 2
		}
	`, "cannot assign to function")
}

func TestRejectRedeclareName(t *testing.T) {
	wantError(t, `
		func main(): void {
			let x: number = 1
			let x: number = 2
		}
	`, `redeclaration of "x"`)
}

func TestRejectDuplicateParam(t *testing.T) {
	wantError(t, `
		func f(a: number, a: string): void {}
		func main(): void {}
	`, "duplicate parameter")
}

// --- String / command interpolation ----------------------------------------

func TestAcceptStringInterpolation(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let n: number = 1
			let s: string = "n=${n}, b=${true}"
			echo(s)
		}
	`)
}

func TestRejectInterpolateNonScalar(t *testing.T) {
	wantError(t, `
		type P = { x: number }
		func main(): void {
			let p: P = P{x: 1}
			let s: string = "${p}"
		}
	`, "cannot interpolate value of type P into string")
}

// --- Inferred types --------------------------------------------------------

func TestAcceptInferStringFromCmdLit(t *testing.T) {
	wantOk(t, "func main(): void { let out = `echo hi`; echo(out) }")
}

func TestRejectInferFromVoidCall(t *testing.T) {
	wantError(t, `
		func main(): void {
			let x = exit(1)
		}
	`, "initializer has type void")
}
