package nativegen_test

import (
	"strings"
	"testing"
)

func TestNativeLenIsRuneCount(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let a: string = "hello"
			let b: string = "héllo"
			let c: string = "aΩ漢"
			echo(str(len(a)) + "," + str(len(b)) + "," + str(len(c)))
		}
	`)
	got := strings.TrimRight(runBin(t, bin), "\n")
	if got != "5,5,3" {
		t.Errorf("got %q want \"5,5,3\"", got)
	}
}

func TestNativeByteLen(t *testing.T) {
	bin := build(t, `
		func main(): void {
			echo(str(byteLen("hello")) + "," + str(byteLen("héllo")) + "," + str(byteLen("aΩ漢")))
		}
	`)
	got := strings.TrimRight(runBin(t, bin), "\n")
	// ASCII: 5; héllo: 6 (é=2 bytes); aΩ漢: 1+2+3=6 bytes.
	if got != "5,6,6" {
		t.Errorf("got %q want \"5,6,6\"", got)
	}
}

func TestNativeSliceIsRuneAware(t *testing.T) {
	bin := build(t, `
		func main(): void {
			echo(slice("héllo", 0, 2))
			echo(slice("aΩ漢", 1, 3))
		}
	`)
	want := "hé\nΩ漢\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeByteSliceUsesBytes(t *testing.T) {
	bin := build(t, `
		func main(): void {
			echo(byteSlice("hello", 0, 3))
			echo(byteSlice("hello", 1, 4))
		}
	`)
	want := "hel\nell\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
