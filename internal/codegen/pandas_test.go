package codegen_test

import (
	"strings"
	"testing"
)

func TestCount(t *testing.T) {
	sh := compile(t, `
		func isEven(n: number): bool { return n % 2 == 0 }
		func main(): void {
			let nums = [1, 2, 3, 4, 5, 6, 7, 8]
			echo("evens=" + str(count(nums, isEven)))
			let empty: number[] = []
			echo("empty=" + str(count(empty, isEven)))
		}
	`)
	out := runShell(t, sh)
	want := "evens=4\nempty=0\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestUniqueStrings(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs = ["a", "b", "a", "c", "b", "d"]
			let u = unique(xs)
			for x in u { echo(x) }
		}
	`)
	out := runShell(t, sh)
	want := "a\nb\nc\nd\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestUniqueNumbers(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs = [3, 1, 2, 1, 3, 2, 1, 4]
			let u = unique(xs)
			for x in u { echo(str(x)) }
		}
	`)
	out := runShell(t, sh)
	want := "3\n1\n2\n4\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestUniqueEmpty(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: string[] = []
			let u = unique(xs)
			echo("len=" + str(len(u)))
		}
	`)
	out := runShell(t, sh)
	want := "len=0\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestUniqueRejectsRecordArray(t *testing.T) {
	src := `
		type P = { name: string }
		func main(): void {
			let xs: P[] = []
			let u = unique(xs)
			echo(str(len(u)))
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 {
		t.Fatal("expected error: unique rejects record arrays")
	}
}

func TestReadCsvShStub(t *testing.T) {
	// The sh backend stubs out CSV with a runtime error directing users to
	// --target=native. The script should still type-check and emit cleanly.
	sh := compile(t, `
		type Row = { name: string, age: number }
		func main(): void {
			let xs: Row[] = readCsv("/dev/null")
			echo(str(len(xs)))
		}
	`)
	if !strings.Contains(sh, "requires --target=native") {
		t.Errorf("expected stub diagnostic in emitted sh; got:\n%s", sh)
	}
}

func TestReadCsvRejectsWithoutContext(t *testing.T) {
	// readCsv without a typed LHS — the checker can't infer T, must error.
	src := `
		func main(): void {
			echo(str(len(readCsv("/dev/null"))))
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 {
		t.Fatal("expected error: readCsv without typed context")
	}
}

func TestReadCsvRejectsArrayOfPrimitive(t *testing.T) {
	src := `
		func main(): void {
			let xs: string[] = readCsv("/dev/null")
			echo(str(len(xs)))
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 {
		t.Fatal("expected error: readCsv requires record element type")
	}
}
