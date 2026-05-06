package nativegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeCount(t *testing.T) {
	bin := build(t, `
		func gtFive(n: number): bool { return n > 5 }
		func main(): void {
			let xs = [1, 3, 5, 7, 9, 11]
			echo(str(count(xs, gtFive)))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "3" {
		t.Errorf("got %q", got)
	}
}

func TestNativeUnique(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let xs = ["a", "b", "a", "c", "b"]
			let u = unique(xs)
			for x in u { echo(x) }
			let ns = [1, 2, 1, 3, 2]
			let un = unique(ns)
			for n in un { echo(str(n)) }
		}
	`)
	got := runBin(t, bin)
	want := "a\nb\nc\n1\n2\n3\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeReadWriteCsvRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.csv")
	out := filepath.Join(dir, "out.csv")
	const csv = "name,age,active\n" +
		"alice,30,true\n" +
		"bob,25,false\n" +
		"\"carol, the third\",42,true\n"
	if err := os.WriteFile(in, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := build(t, `
		type P = { name: string, age: number, active: bool }
		func main(): void {
			let xs: P[] = readCsv("`+in+`")
			echo("read=" + str(len(xs)))
			for p in xs { echo(p.name + "|" + str(p.age)) }
			writeCsv(xs, "`+out+`")
			echo("done")
		}
	`)
	got := runBin(t, bin)
	want := "read=3\nalice|30\nbob|25\ncarol, the third|42\ndone\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// Header preserved, comma in name properly quoted, types serialized.
	if !strings.Contains(string(written), "\"carol, the third\"") {
		t.Errorf("written CSV did not preserve quoted comma: %s", written)
	}
	if !strings.HasPrefix(string(written), "name,age,active\n") {
		t.Errorf("written CSV missing header: %s", written)
	}
}

func TestNativeReadCsvOptionalFields(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.csv")
	const csv = "name,age\nalice,30\nbob,\ncarol,42\n"
	if err := os.WriteFile(in, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := build(t, `
		type P = { name: string, age: number? }
		func main(): void {
			let xs: P[] = readCsv("`+in+`")
			for p in xs {
				if p.age == null { echo(p.name + "=null") }
				else { echo(p.name + "=" + str(p.age!)) }
			}
		}
	`)
	got := runBin(t, bin)
	want := "alice=30\nbob=null\ncarol=42\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
