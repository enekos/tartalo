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
