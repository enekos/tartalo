package nativegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/nativegen"
	"github.com/enekos/tartalo/internal/parser"
)

// build compiles the supplied Tartalo source to a native executable in t's
// temp dir and returns the executable's path. The toolchain is required:
// any test file using build will fall back with t.Skip if `go` is missing.
func build(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping native build test")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.tt")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	// Run the standard frontend so we exercise the same path the CLI does.
	toks, lerrs := lexer.New(srcPath, src).Tokenize()
	if len(lerrs) > 0 {
		t.Fatalf("lex errors: %v", lerrs)
	}
	file, perrs := parser.New(toks).Parse(srcPath)
	if len(perrs) > 0 {
		t.Fatalf("parse errors: %v", perrs)
	}
	mod := &loader.Module{File: file, IsEntry: true}
	info, cerrs := checker.New().Check([]*loader.Module{mod})
	if len(cerrs) > 0 {
		t.Fatalf("type errors: %v", cerrs)
	}
	bin := filepath.Join(dir, "prog")
	if err := nativegen.Build([]*loader.Module{mod}, info, nativegen.BuildOptions{Output: bin}); err != nil {
		t.Fatalf("native build: %v\n--source--\n%s", err, nativegen.EmitSource([]*loader.Module{mod}, info))
	}
	return bin
}

func runBin(t *testing.T, bin string, env ...string) string {
	t.Helper()
	cmd := exec.Command(bin)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("binary failed: %v\n--output--\n%s", err, out)
	}
	return string(out)
}

func TestNativeHelloWorld(t *testing.T) {
	bin := build(t, `
		func greet(who: string): string {
			return "Hello, " + who + "!"
		}
		func main(): void {
			let who: string = "world"
			echo(greet(who))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "Hello, world!" {
		t.Errorf("got %q", got)
	}
}

func TestNativeArithmetic(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let a: number = 3
			let b: number = 4
			echo(str(a * a + b * b))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "25" {
		t.Errorf("got %q", got)
	}
}

func TestNativeFizzBuzzSlice(t *testing.T) {
	bin := build(t, `
		func main(): void {
			for i in 1..6 {
				if i % 3 == 0 {
					echo("Fizz")
				} else {
					echo(str(i))
				}
			}
		}
	`)
	want := "1\n2\nFizz\n4\n5\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeStringInterpolation(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let name: string = "world"
			let n: number = 42
			echo("Hello, ${name}! count=${n}")
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "Hello, world! count=42" {
		t.Errorf("got %q", got)
	}
}

func TestNativeArrayOps(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let xs = [10, 20, 30]
			echo(str(len(xs)))
			echo(str(xs[1]))
			let total = 0
			for x in xs { total = total + x }
			echo(str(total))
		}
	`)
	want := "3\n20\n60\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeOptionalCoalesce(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let host = env("TT_TEST_HOST") ?? "fallback"
			echo(host)
		}
	`)
	if got := strings.TrimRight(runBin(t, bin, "TT_TEST_HOST=set"), "\n"); got != "set" {
		t.Errorf("with env: got %q want %q", got, "set")
	}
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "fallback" {
		t.Errorf("without env: got %q want %q", got, "fallback")
	}
}

func TestNativeOptionalNullCheck(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let key = env("TT_TEST_OPT")
			if key == null {
				echo("absent")
			} else {
				echo("present:" + key!)
			}
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "absent" {
		t.Errorf("absent: got %q", got)
	}
	if got := strings.TrimRight(runBin(t, bin, "TT_TEST_OPT=hi"), "\n"); got != "present:hi" {
		t.Errorf("present: got %q", got)
	}
}

func TestNativeRecord(t *testing.T) {
	bin := build(t, `
		type P = { name: string, age: number }
		func describe(p: P): string {
			return p.name + " is " + str(p.age)
		}
		func main(): void {
			let alice = P{name: "alice", age: 30}
			echo(describe(alice))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "alice is 30" {
		t.Errorf("got %q", got)
	}
}

func TestNativeRecordPassByValue(t *testing.T) {
	bin := build(t, `
		type Box = { v: number }
		func bumped(b: Box): Box {
			return Box{v: b.v + 1}
		}
		func main(): void {
			let a = Box{v: 10}
			let b = bumped(a)
			echo(str(a.v))
			echo(str(b.v))
		}
	`)
	want := "10\n11\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativePipelineChained(t *testing.T) {
	bin := build(t, `
		func double(x: number): number { return x * 2 }
		func plus(x: number, y: number): number { return x + y }
		func main(): void {
			echo(str(3 |> double() |> plus(1)))
			echo("HELLO" |> lower)
		}
	`)
	want := "7\nhello\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeResultTry(t *testing.T) {
	bin := build(t, `
		type IntResult = Ok{value: number} | Err{error: string}
		func parseInt(s: string): IntResult {
			if s == "bad" { return Err{error: "bad input " + s} }
			return Ok{value: 1}
		}
		func double(s: string): IntResult {
			let n: number = parseInt(s)?
			return Ok{value: n + n}
		}
		func main(): void {
			match double("ok") {
				Ok{value} => echo("ok:" + str(value))
				Err{error} => echo("err:" + error)
			}
			match double("bad") {
				Ok{value} => echo("ok:" + str(value))
				Err{error} => echo("err:" + error)
			}
		}
	`)
	want := "ok:2\nerr:bad input bad\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeResultTryRunsDefer(t *testing.T) {
	bin := build(t, `
		type R = Ok{value: number} | Err{error: string}
		func failing(): R { return Err{error: "boom"} }
		func work(): R {
			defer { echo("cleanup") }
			let v: number = failing()?
			return Ok{value: v}
		}
		func main(): void {
			match work() {
				Ok{value} => echo("ok:" + str(value))
				Err{error} => echo("err:" + error)
			}
		}
	`)
	want := "cleanup\nerr:boom\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeSumPayloadAndBindings(t *testing.T) {
	bin := build(t, `
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
	want := "48\n30\n0\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeSumStringPayload(t *testing.T) {
	bin := build(t, `
		type Event = Click{at: string} | Quit
		func main(): void {
			let e: Event = Click{at: "10,20"}
			match e {
				Click{at} => echo("click@" + at)
				Quit => echo("quit")
			}
		}
	`)
	if got := runBin(t, bin); got != "click@10,20\n" {
		t.Errorf("got %q", got)
	}
}

func TestNativeDeferLIFO(t *testing.T) {
	bin := build(t, `
		func main(): void {
			defer { echo("first") }
			defer { echo("second") }
			defer { echo("third") }
			echo("body")
		}
	`)
	want := "body\nthird\nsecond\nfirst\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeDeferSeesLocals(t *testing.T) {
	bin := build(t, `
		func work(): void {
			let n: number = 0
			defer { echo("n=" + str(n)) }
			n = 42
		}
		func main(): void { work() }
	`)
	want := "n=42\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeArrayOfRecords(t *testing.T) {
	bin := build(t, `
		type Person = { name: string, age: number }
		func main(): void {
			let people: Person[] = [
				Person{name: "Alice", age: 30},
				Person{name: "Bob", age: 25},
				Person{name: "Carol", age: 41},
			]
			echo(str(len(people)))
			echo(people[0].name + ":" + str(people[0].age))
			for p in people { echo(p.name + "/" + str(p.age)) }
		}
	`)
	want := "3\nAlice:30\nAlice/30\nBob/25\nCarol/41\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeArrayOfRecordsNested(t *testing.T) {
	bin := build(t, `
		type Addr = { city: string, zip: number }
		type Person = { name: string, addr: Addr }
		func main(): void {
			let xs: Person[] = [
				Person{name: "Alice", addr: Addr{city: "Madrid", zip: 28001}},
				Person{name: "Bob", addr: Addr{city: "Bilbao", zip: 48000}},
			]
			for p in xs {
				echo(p.name + "/" + p.addr.city + "/" + str(p.addr.zip))
			}
		}
	`)
	want := "Alice/Madrid/28001\nBob/Bilbao/48000\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeMatchPatterns(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let action = env("TT_TEST_ACTION") ?? ""
			match action {
				"build" | "compile" => echo("c")
				"run" => echo("r")
				_ => echo("u")
			}
		}
	`)
	cases := []struct{ env, want string }{
		{"TT_TEST_ACTION=build", "c\n"},
		{"TT_TEST_ACTION=compile", "c\n"},
		{"TT_TEST_ACTION=run", "r\n"},
		{"TT_TEST_ACTION=other", "u\n"},
	}
	for _, c := range cases {
		if got := runBin(t, bin, c.env); got != c.want {
			t.Errorf("env=%s: got %q want %q", c.env, got, c.want)
		}
	}
}

func TestNativeStringStdlib(t *testing.T) {
	bin := build(t, `
		func main(): void {
			echo(upper("hi"))
			echo(lower("HI"))
			echo(trim("  spaced  "))
			echo(replace("a.b.c", ".", "/"))
			if startsWith("foobar", "foo") { echo("ok-prefix") }
			if endsWith("foo.tt", ".tt") { echo("ok-suffix") }
			if contains("hello world", "lo wo") { echo("ok-contains") }
			let parts = split("x,y,z", ",")
			echo(join(parts, "+"))
			echo(slice("abcdef", 1, 4))
		}
	`)
	want := "HI\nhi\nspaced\na/b/c\nok-prefix\nok-suffix\nok-contains\nx+y+z\nbcd\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeNestedCalls(t *testing.T) {
	bin := build(t, `
		func id(s: string): string { return s }
		func main(): void {
			echo(id(id("hello")) + " " + id("world"))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestNativeNegativeNumbers(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let a = -5
			let b = a + 10
			echo(str(a))
			echo(str(b))
			echo(str(-a))
			echo(str(0 - 7))
		}
	`)
	want := "-5\n5\n5\n-7\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeBoolStrings(t *testing.T) {
	// The native backend matches sh: bools stringify as "1"/"0".
	bin := build(t, `
		func main(): void {
			echo(str(true))
			echo(str(false))
		}
	`)
	want := "1\n0\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeRegex(t *testing.T) {
	bin := build(t, `
		func main(): void {
			if regexMatch("hello123world", "[0-9]+") { echo("match") }
			let f = regexFind("a42b", "[0-9]+")
			echo(f ?? "none")
			let all = regexFindAll("a1 b22 c333", "[0-9]+")
			echo(str(len(all)))
			for n in all { echo(n) }
			echo(regexReplace("foo bar", "foo", "BAZ"))
		}
	`)
	want := "match\n42\n3\n1\n22\n333\nBAZ bar\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeJSON(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let body = "{\"name\":\"alice\",\"age\":30,\"tags\":[\"x\",\"y\"],\"nested\":{\"key\":\"val\"}}"
			echo(jsonGet(body, ".name") ?? "<m>")
			echo(jsonGet(body, ".age") ?? "<m>")
			echo(jsonGet(body, ".missing") ?? "<m>")
			echo(jsonGet(body, ".nested.key") ?? "<m>")
			echo(jsonGet(body, ".tags[0]") ?? "<m>")
			if jsonHas(body, ".name") { echo("has") }
			let arr = jsonArray(body, ".tags")
			echo(str(len(arr)))
		}
	`)
	want := "alice\n30\n<m>\nval\nx\nhas\n2\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeFileIO(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let p = env("TT_TEST_FILE")!
			writeFile(p, "first\n")
			appendFile(p, "second\n")
			echo(readFile(p))
			removeFile(p)
			if !exists(p) { echo("gone") }
		}
	`)
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	got := runBin(t, bin, "TT_TEST_FILE="+path)
	want := "first\nsecond\ngone\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativePath(t *testing.T) {
	bin := build(t, `
		func main(): void {
			echo(basename("/a/b/c"))
			echo(dirname("/a/b/c"))
			echo(extname("foo.tar.gz"))
			echo(extname("Makefile"))
			echo(pathJoin("/a", "b"))
			echo(pathJoin("/a/", "b"))
			echo(pathJoin("/a", "/b"))
			let pp = parsePath("/x/y/z.txt")
			echo(pp.dir)
			echo(pp.base)
			echo(pp.name)
			echo(pp.ext)
		}
	`)
	want := "c\n/a/b\n.gz\n\n/a/b\n/a/b\n/b\n/x/y\nz.txt\nz\n.txt\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeFloats(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let f = parseFloat("3.14") ?? 0.0
			echo(formatFloat(f, 2))
			echo(formatFloat(floor(3.7), 0))
			echo(formatFloat(ceil(3.2), 0))
			echo(str(intOf(2.99)))
			echo(formatFloat(floatOf(7), 1))
		}
	`)
	want := "3.14\n3\n4\n2\n7.0\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeMapFilterReduce(t *testing.T) {
	bin := build(t, `
		func double(n: number): number { return n * 2 }
		func isEven(n: number): bool { return n % 2 == 0 }
		func add(a: number, b: number): number { return a + b }
		func main(): void {
			let xs = [1, 2, 3, 4, 5]
			let doubled = map(xs, double)
			for n in doubled { echo(str(n)) }
			let evens = filter(xs, isEven)
			echo(str(len(evens)))
			let total = reduce(xs, 0, add)
			echo(str(total))
		}
	`)
	want := "2\n4\n6\n8\n10\n2\n15\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeMapAcrossTypes(t *testing.T) {
	bin := build(t, `
		func toStr(n: number): string { return "n=" + str(n) }
		func main(): void {
			let xs = [10, 20, 30]
			let strs = map(xs, toStr)
			for s in strs { echo(s) }
		}
	`)
	want := "n=10\nn=20\nn=30\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeReduceFloatAccumulator(t *testing.T) {
	bin := build(t, `
		func sumFloat(acc: float, n: float): float { return acc + n }
		func main(): void {
			let xs: float[] = [1.5, 2.5, 3.0]
			let total: float = reduce(xs, 0.0, sumFloat)
			echo(formatFloat(total, 1))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "7.0" {
		t.Errorf("got %q", got)
	}
}

func TestNativeTestHarnessPassing(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	src := `
		test "one plus one" {
			assertEq(1 + 1, 2)
		}
		test "string concat" {
			assertEq("a" + "b", "ab")
		}
	`
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "t.tt")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	toks, _ := lexer.New(srcPath, src).Tokenize()
	file, _ := parser.New(toks).Parse(srcPath)
	mod := &loader.Module{File: file, IsEntry: true}
	info, cerrs := checker.New().Check([]*loader.Module{mod})
	if len(cerrs) > 0 {
		t.Fatalf("check: %v", cerrs)
	}
	bin := filepath.Join(dir, "tests")
	if err := nativegen.BuildTest([]*loader.Module{mod}, info, nativegen.BuildOptions{Output: bin}); err != nil {
		t.Fatalf("buildTest: %v", err)
	}
	cmd := exec.Command(bin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test bin failed: %v\n%s", err, out)
	}
	got := string(out)
	for _, want := range []string{"one plus one", "string concat", "2 passed", "(2 total)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestNativeTestHarnessFailing(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	src := `
		test "passes" { assertEq(1, 1) }
		test "fails" { assertEq("x", "y") }
		test "skipped" { skip("nope") }
	`
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "t.tt")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	toks, _ := lexer.New(srcPath, src).Tokenize()
	file, _ := parser.New(toks).Parse(srcPath)
	mod := &loader.Module{File: file, IsEntry: true}
	info, _ := checker.New().Check([]*loader.Module{mod})
	bin := filepath.Join(dir, "tests")
	if err := nativegen.BuildTest([]*loader.Module{mod}, info, nativegen.BuildOptions{Output: bin}); err != nil {
		t.Fatalf("buildTest: %v", err)
	}
	cmd := exec.Command(bin)
	out, _ := cmd.CombinedOutput()
	got := string(out)
	if exitCode := cmd.ProcessState.ExitCode(); exitCode == 0 {
		t.Fatalf("expected non-zero exit on failing tests, got 0\nstdout:\n%s", got)
	}
	for _, want := range []string{"1 failed", "1 passed", "1 skipped", "(3 total)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestNativeExec(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let r = exec("echo hello && false")
			echo(trim(r.stdout))
			echo(str(r.code))
			let t = execTimeout("echo fast", 5)
			echo(trim(t.stdout))
		}
	`)
	want := "hello\n1\nfast\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// buildTestMode compiles `src` as a test binary and returns the path. Used
// by mock tests below — they need EmitTest semantics (mock state, harness)
// rather than the EmitRun shape produced by `build`.
func buildTestMode(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping native test-mode build")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.tt")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	toks, lerrs := lexer.New("prog.tt", src).Tokenize()
	if len(lerrs) > 0 {
		t.Fatalf("lex: %v", lerrs)
	}
	file, perrs := parser.New(toks).Parse("prog.tt")
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs)
	}
	mod := &loader.Module{File: file, IsEntry: true}
	info, cerrs := checker.New().Check([]*loader.Module{mod})
	if len(cerrs) > 0 {
		t.Fatalf("check: %v", cerrs)
	}
	bin := filepath.Join(dir, "tests")
	if err := nativegen.BuildTest([]*loader.Module{mod}, info, nativegen.BuildOptions{Output: bin}); err != nil {
		t.Fatalf("buildTest: %v\n--source--\n%s", err, nativegen.EmitSourceTest([]*loader.Module{mod}, info))
	}
	return bin
}

// runTestBin executes a test binary with NO_COLOR forced so output is
// stable, returns the combined stdout+stderr, and reports the exit code.
func runTestBin(t *testing.T, bin string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func TestNativeMockExecMatch(t *testing.T) {
	bin := buildTestMode(t, `
		test "exec is mocked" {
			mockExec("git", Process{code: 0, ok: true, stdout: "abc Subject", stderr: ""})
			let r = exec("git log -1")
			assertEq(r.stdout, "abc Subject")
			assertEq(len(mockExecCalls()), 1)
		}
	`)
	out, code := runTestBin(t, bin)
	if code != 0 {
		t.Errorf("expected pass, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "1 passed") {
		t.Errorf("expected 1 passed, got:\n%s", out)
	}
}

func TestNativeMockExecStrictFail(t *testing.T) {
	bin := buildTestMode(t, `
		test "strict exec fails on miss" {
			mockExec("ls", Process{code: 0, ok: true, stdout: "", stderr: ""})
			let r = exec("rm -rf /")
			fail("unreachable")
		}
	`)
	out, code := runTestBin(t, bin)
	if code == 0 {
		t.Errorf("expected non-zero exit, got 0\n%s", out)
	}
	if !strings.Contains(out, "exec: no mock matched: rm -rf /") {
		t.Errorf("expected strict failure message, got:\n%s", out)
	}
}

func TestNativeMockEnv(t *testing.T) {
	bin := buildTestMode(t, `
		test "override sets value" {
			mockEnv("TT_X", "yes")
			assertEq(env("TT_X") ?? "<u>", "yes")
		}
		test "null override marks unset" {
			mockEnv("HOME", null)
			if env("HOME") == null { check(true) } else { fail("expected unset") }
		}
		test "non-mocked names fall through" {
			mockEnv("OTHER", "x")
			assertEq(env("OTHER") ?? "<u>", "x")
		}
	`)
	out, code := runTestBin(t, bin)
	if code != 0 {
		t.Errorf("expected pass, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "3 passed") {
		t.Errorf("expected 3 passed, got:\n%s", out)
	}
}

func TestNativeMockNowAndArgs(t *testing.T) {
	bin := buildTestMode(t, `
		test "now is frozen" {
			mockNow(1700000000)
			assertEq(now(), 1700000000)
		}
		test "args are overridden" {
			mockArgs(["a", "b", "c"])
			let xs = args()
			assertEq(len(xs), 3)
			assertEq(xs[2], "c")
		}
	`)
	out, code := runTestBin(t, bin)
	if code != 0 {
		t.Errorf("expected pass, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "2 passed") {
		t.Errorf("expected 2 passed, got:\n%s", out)
	}
}

func TestNativeMockReadFileRegex(t *testing.T) {
	bin := buildTestMode(t, `
		test "regex pattern matches multiple paths" {
			mockReadFile("/etc/.*", "fake content")
			assertEq(readFile("/etc/foo"), "fake content")
			assertEq(readFile("/etc/bar"), "fake content")
			assertEq(len(mockReadFileCalls()), 2)
		}
	`)
	out, code := runTestBin(t, bin)
	if code != 0 {
		t.Errorf("expected pass, got exit %d:\n%s", code, out)
	}
}

func TestNativeMockResetBetweenTests(t *testing.T) {
	bin := buildTestMode(t, `
		test "first test mocks exec" {
			mockExec("ls", Process{code: 0, ok: true, stdout: "fake", stderr: ""})
			let r = exec("ls")
			assertEq(r.stdout, "fake")
		}
		test "second test sees clean state" {
			// If state leaked, mockExecCalls would already have 1 entry.
			assertEq(len(mockExecCalls()), 0)
		}
	`)
	out, code := runTestBin(t, bin)
	if code != 0 {
		t.Errorf("expected pass, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "2 passed") {
		t.Errorf("expected 2 passed, got:\n%s", out)
	}
}

// TestNativeCalcTestParity runs examples/calc_test.tt through both test
// harnesses and checks the output is byte-identical. This pins down the
// banner text, per-test pass/fail glyphs, skip formatting, and final
// summary to whatever the sh harness produces.
func TestNativeCalcTestParity(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	path := filepath.Join(repoRoot, "examples", "calc_test.tt")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Loader passes filepath.Base for parsing so File.Path stays short; the
	// test harness emits this in its banner. Mirror that.
	short := filepath.Base(path)
	toks, _ := lexer.New(short, string(src)).Tokenize()
	file, _ := parser.New(toks).Parse(short)
	mod := &loader.Module{File: file, IsEntry: true, AbsPath: path}
	info, cerrs := checker.New().Check([]*loader.Module{mod})
	if len(cerrs) > 0 {
		t.Fatalf("check: %v", cerrs)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "tests")
	if err := nativegen.BuildTest([]*loader.Module{mod}, info, nativegen.BuildOptions{Output: bin}); err != nil {
		t.Fatalf("buildTest: %v", err)
	}
	// Force NO_COLOR so both backends produce plain ASCII output.
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	gotNative, _ := cmd.CombinedOutput()
	tartaloBin := filepath.Join(dir, "tartalo")
	b := exec.Command("go", "build", "-o", tartaloBin, "./cmd/tartalo")
	b.Dir = repoRoot
	if out, err := b.CombinedOutput(); err != nil {
		t.Fatalf("build tartalo: %v\n%s", err, out)
	}
	run := exec.Command(tartaloBin, "test", "--no-verify", path)
	run.Env = append(os.Environ(), "NO_COLOR=1")
	gotSh, _ := run.CombinedOutput()
	if string(gotNative) != string(gotSh) {
		t.Errorf("test output differs:\n--native--\n%s\n--sh--\n%s", gotNative, gotSh)
	}
}

// TestNativeExamplesParity is the strongest correctness signal: every
// committed .tt example must produce identical output under the sh and
// native backends. We restrict to examples that don't depend on external
// services (fetch, api) or features still on the M3 backlog.
func TestNativeExamplesParity(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping examples parity test")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	examples := []struct {
		file string
		env  []string
	}{
		{"hello.tt", nil},
		{"fizzbuzz.tt", nil},
		{"array.tt", nil},
		{"sum.tt", nil},
		{"strings.tt", nil},
		{"record.tt", nil},
		{"config.tt", nil},
		{"match.tt", []string{"ACTION=run"}},
		{"git-summary.tt", nil},
		{"files.tt", []string{"DIR=examples"}},
	}
	for _, ex := range examples {
		t.Run(ex.file, func(t *testing.T) {
			path := filepath.Join(repoRoot, "examples", ex.file)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			toks, lerrs := lexer.New(path, string(src)).Tokenize()
			if len(lerrs) > 0 {
				t.Fatalf("lex: %v", lerrs)
			}
			file, perrs := parser.New(toks).Parse(path)
			if len(perrs) > 0 {
				t.Fatalf("parse: %v", perrs)
			}
			mod := &loader.Module{File: file, IsEntry: true, AbsPath: path}
			info, cerrs := checker.New().Check([]*loader.Module{mod})
			if len(cerrs) > 0 {
				t.Fatalf("check: %v", cerrs)
			}
			dir := t.TempDir()
			bin := filepath.Join(dir, "ex")
			if err := nativegen.Build([]*loader.Module{mod}, info, nativegen.BuildOptions{Output: bin}); err != nil {
				t.Fatalf("native build: %v", err)
			}
			cmd := exec.Command(bin)
			cmd.Dir = repoRoot
			if ex.env != nil {
				cmd.Env = append(os.Environ(), ex.env...)
			}
			gotNative, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("native run: %v\n%s", err, gotNative)
			}
			// Run sh backend for comparison.
			tartaloBin := filepath.Join(dir, "tartalo")
			b := exec.Command("go", "build", "-o", tartaloBin, "./cmd/tartalo")
			b.Dir = repoRoot
			if out, err := b.CombinedOutput(); err != nil {
				t.Fatalf("build tartalo: %v\n%s", err, out)
			}
			run := exec.Command(tartaloBin, "run", "--no-verify", path)
			run.Dir = repoRoot
			if ex.env != nil {
				run.Env = append(os.Environ(), ex.env...)
			}
			gotSh, err := run.CombinedOutput()
			if err != nil {
				t.Fatalf("sh run: %v\n%s", err, gotSh)
			}
			if string(gotNative) != string(gotSh) {
				t.Errorf("output differs:\n--native--\n%s\n--sh--\n%s", gotNative, gotSh)
			}
		})
	}
}
