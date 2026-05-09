package loader_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/codegen"
	"github.com/enekos/tartalo/internal/loader"
)

// writeFile is a test helper that creates a file at the given path with the
// given content, creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// compileEntry runs the full pipeline (loader → checker → codegen) on a file.
// Returns the bundled sh.
func compileEntry(t *testing.T, entry string) (string, error) {
	t.Helper()
	modules, errs := loader.Load(entry)
	if len(errs) > 0 {
		return "", combineErrors(errs)
	}
	info, cerrs := checker.New().Check(modules)
	if len(cerrs) > 0 {
		return "", combineErrors(cerrs)
	}
	return codegen.New(info).EmitModules(modules), nil
}

func combineErrors(errs []error) error {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return errString(strings.Join(parts, "\n"))
}

type errString string

func (e errString) Error() string { return string(e) }

func runScript(t *testing.T, sh string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.sh")
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("/bin/sh", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n--script--\n%s\n--out--\n%s", err, sh, out)
	}
	return string(out)
}

func TestImportFunctionAndType(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib", "math.tt"), `
		export type Pair = { a: number, b: number }
		export func sumPair(p: Pair): number {
			return p.a + p.b
		}
	`)
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { Pair, sumPair } from "./lib/math.tt"
		func main(): void {
			let p: Pair = Pair{a: 7, b: 35}
			echo(str(sumPair(p)))
		}
	`)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "42" {
		t.Errorf("got %q", got)
	}
}

func TestPrivateNamesNotImportable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.tt"), `
		func privateHelper(): string { return "shh" }
		export func publicFn(): string { return privateHelper() }
	`)
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { privateHelper } from "./lib.tt"
		func main(): void { echo(privateHelper()) }
	`)
	_, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err == nil || !strings.Contains(err.Error(), `no exported name "privateHelper"`) {
		t.Fatalf("expected 'no exported name', got %v", err)
	}
}

func TestImportUnknownNameErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.tt"), `
		export func known(): void { echo("hi") }
	`)
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { unknown } from "./lib.tt"
		func main(): void {}
	`)
	_, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err == nil || !strings.Contains(err.Error(), `no exported name "unknown"`) {
		t.Fatalf("expected unknown-name error, got %v", err)
	}
}

func TestNoMissingImportPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { a } from "./does-not-exist.tt"
		func main(): void {}
	`)
	_, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err == nil || !strings.Contains(err.Error(), "cannot find module") {
		t.Fatalf("expected missing-module error, got %v", err)
	}
}

func TestImportCycleErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.tt"), `
		import { b } from "./b.tt"
		export func a(): void { b() }
	`)
	writeFile(t, filepath.Join(dir, "b.tt"), `
		import { a } from "./a.tt"
		export func b(): void { a() }
	`)
	_, err := compileEntry(t, filepath.Join(dir, "a.tt"))
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestNamesFromDifferentModulesDontCollide(t *testing.T) {
	// Both lib1 and lib2 export a private helper named the same as a private
	// name in main, and they each define a `format` function with different
	// signatures. Mangling should keep them all distinct.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib1.tt"), `
		export func format(s: string): string { return "[" + s + "]" }
	`)
	writeFile(t, filepath.Join(dir, "lib2.tt"), `
		export func format(n: number): string { return "(" + str(n) + ")" }
	`)
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { format } from "./lib1.tt"
		// Note: we only import format from lib1; lib2's format is private to lib2.
		func main(): void { echo(format("hi")) }
	`)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "[hi]" {
		t.Errorf("got %q", got)
	}
}

func TestImportingTypeAcrossModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.tt"), `
		export type Item = { name: string, qty: number }
		export func make(n: string, q: number): Item { return Item{name: n, qty: q} }
	`)
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { Item, make } from "./lib.tt"
		func main(): void {
			let it: Item = make("apple", 3)
			echo(it.name + " x" + str(it.qty))
		}
	`)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "apple x3" {
		t.Errorf("got %q", got)
	}
}

func TestDiamondImportLoadsSharedDepOnce(t *testing.T) {
	// main imports left + right; both import shared. Shared must resolve to
	// the same *Module pointer (single ID) regardless of which path reaches it
	// first under the parallel loader.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shared.tt"), `
		export func tag(s: string): string { return "[" + s + "]" }
	`)
	writeFile(t, filepath.Join(dir, "left.tt"), `
		import { tag } from "./shared.tt"
		export func leftSay(s: string): string { return tag("L:" + s) }
	`)
	writeFile(t, filepath.Join(dir, "right.tt"), `
		import { tag } from "./shared.tt"
		export func rightSay(s: string): string { return tag("R:" + s) }
	`)
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { leftSay } from "./left.tt"
		import { rightSay } from "./right.tt"
		func main(): void {
			echo(leftSay("hi"))
			echo(rightSay("hi"))
		}
	`)
	modules, errs := loader.Load(filepath.Join(dir, "main.tt"))
	if len(errs) > 0 {
		t.Fatalf("loader errs: %v", errs)
	}
	// 4 modules: shared, left, right, main.
	if len(modules) != 4 {
		t.Fatalf("expected 4 modules, got %d", len(modules))
	}
	// Shared dep should appear exactly once.
	sharedCount := 0
	for _, m := range modules {
		if filepath.Base(m.AbsPath) == "shared.tt" {
			sharedCount++
		}
	}
	if sharedCount != 1 {
		t.Fatalf("expected shared.tt to appear once, got %d", sharedCount)
	}
	// And both left/right should reference the same pointer.
	var left, right *loader.Module
	for _, m := range modules {
		switch filepath.Base(m.AbsPath) {
		case "left.tt":
			left = m
		case "right.tt":
			right = m
		}
	}
	if left == nil || right == nil {
		t.Fatal("missing left/right module")
	}
	if left.Imports[0].Module != right.Imports[0].Module {
		t.Fatal("left and right resolved to distinct shared modules")
	}

	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	want := "[L:hi]\n[R:hi]\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestStdlibImport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { padLeft } from "tartalo:strings/extra"
		func main(): void {
			echo(padLeft("7", 3, "0"))
		}
	`)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "007" {
		t.Errorf("got %q", got)
	}
}

func TestStdlibStringsExtra(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { capitalize, removePrefix, removeSuffix, count, truncate, indent } from "tartalo:strings/extra"
		func main(): void {
			echo(capitalize("alice"))
			echo(removePrefix("foo:bar", "foo:"))
			echo(removeSuffix("name.tt", ".tt"))
			echo(str(count("xxxx", "xx")))
			echo(truncate("abcdef", 5, ".."))
			echo(indent("a\nb", "> "))
		}
	`)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	want := "Alice\nbar\nname\n2\nabc..\n> a\n> b\n"
	if out != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
}

func TestStdlibNumbersExtra(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { clamp, abs, gcd, pow, isEven, addNum, mulNum } from "tartalo:numbers/extra"
		func main(): void {
			echo(str(clamp(15, 0, 10)))
			echo(str(abs(-7)))
			echo(str(gcd(48, 18)))
			echo(str(pow(3, 4)))
			let xs = [1, 2, 3, 4]
			echo("evens=" + str(len(filter(xs, isEven))))
			echo("sum=" + str(reduce(xs, 0, addNum)))
			echo("prod=" + str(reduce(xs, 1, mulNum)))
		}
	`)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	want := "10\n7\n6\n81\nevens=2\nsum=10\nprod=24\n"
	if out != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
}

func TestStdlibResult(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { Result, ok, err, isOk, isErr, unwrapOr, errorOf } from "tartalo:result/result"
		func mk(s: string): Result {
			if s == "bad" { return err("nope") }
			return ok("got " + s)
		}
		func main(): void {
			let r1: Result = mk("foo")
			let r2: Result = mk("bad")
			echo(unwrapOr(r1, "?"))
			echo(unwrapOr(r2, "default"))
			if isOk(r1)  { echo("r1 ok") }
			if isErr(r2) { echo("r2 err") }
			echo(errorOf(r2) ?? "")
		}
	`)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	want := "got foo\ndefault\nr1 ok\nr2 err\nnope\n"
	if out != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
}

func TestStdlibPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { join3, withExt, stripExt, isAbs, stem } from "tartalo:path/path"
		func main(): void {
			echo(join3("a", "b", "c.txt"))
			echo(withExt("foo/bar.txt", ".log"))
			echo(stripExt("foo/bar.txt"))
			echo(stem("/var/log/x.gz"))
			if isAbs("/etc")  { echo("abs") } else { echo("rel") }
			if isAbs("./foo") { echo("abs") } else { echo("rel") }
		}
	`)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	want := "a/b/c.txt\nfoo/bar.log\nfoo/bar\nx\nabs\nrel\n"
	if out != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
}

func TestStdlibRegex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { matches, findOr, replaceAll, hasAny, hasAll, countMatches } from "tartalo:regex/regex"
		func main(): void {
			if matches("hello42", "[0-9]+") { echo("digit") }
			echo(findOr("abc-def", "[a-z]+", "?"))
			echo(replaceAll("a a a", "a", "b"))
			echo(str(countMatches("ababab", "a")))
			if hasAny("hello", ["xyz", "ell"]) { echo("any") }
			if hasAll("hello", ["he", "llo"]) { echo("all") }
		}
	`)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	want := "digit\nabc\nb b b\n3\nany\nall\n"
	if out != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
}

func TestStdlibTime(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { unixNow, since, formatNow } from "tartalo:time/time"
		func main(): void {
			let t: number = unixNow()
			if t > 0 { echo("got time") }
			echo(str(since(t - 100) >= 100))
			let y: string = formatNow("%Y")
			if len(y) == 4 { echo("4-digit year") }
		}
	`)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	want := "got time\n1\n4-digit year\n"
	if out != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
}

func TestStdlibFs(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	src := `
		import { readLines, writeLines, ensureDir, listFiles, readFileOr } from "tartalo:fs/fs"
		func main(): void {
			let dir: string = "` + work + `"
			ensureDir(dir)
			writeLines(dir + "/a.txt", ["one", "two", "three"])
			let lines: string[] = readLines(dir + "/a.txt")
			echo(str(len(lines)))
			for l in lines { echo(l) }
			echo(readFileOr("/no/such/file", "[empty]"))
			let files: string[] = listFiles(dir)
			echo(str(len(files)))
		}
	`
	writeFile(t, filepath.Join(dir, "main.tt"), src)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	want := "3\none\ntwo\nthree\n[empty]\n1\n"
	if out != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
}

func TestStdlibJson(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { getOr, getIntOr, getBoolOr, escape } from "tartalo:json/json"
		func main(): void {
			echo(getOr("{\"name\":\"Alice\"}", ".name", "?"))
			echo(getOr("{\"name\":\"Alice\"}", ".missing", "fallback"))
			echo(str(getIntOr("{\"n\":42}", ".n", -1)))
			echo(str(getIntOr("{\"x\":1}", ".n", -1)))
			if getBoolOr("{\"ok\":true}", ".ok", false) { echo("yes") }
			echo(escape("a\"b"))
		}
	`)
	sh, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatal(err)
	}
	out := runScript(t, sh)
	want := "Alice\nfallback\n42\n-1\nyes\n\"a\\\"b\"\n"
	if out != want {
		t.Errorf("got %q\nwant %q", out, want)
	}
}

func TestStdlibImportMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { whatever } from "tartalo:nope/missing"
		func main(): void {}
	`)
	_, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err == nil || !strings.Contains(err.Error(), "stdlib module") {
		t.Fatalf("expected stdlib-not-found error, got %v", err)
	}
}

func TestRedeclareImportedNameErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.tt"), `
		export func helper(): string { return "x" }
	`)
	writeFile(t, filepath.Join(dir, "main.tt"), `
		import { helper } from "./lib.tt"
		func helper(): void {}
		func main(): void {}
	`)
	_, err := compileEntry(t, filepath.Join(dir, "main.tt"))
	if err == nil || !strings.Contains(err.Error(), `duplicate name "helper"`) {
		t.Fatalf("expected duplicate-name error, got %v", err)
	}
}
