package nativegen_test

import (
	"strings"
	"testing"
)

func TestNativeVecReductions(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let xs: float[] = [1.0, 2.0, 3.0, 4.0, 5.0]
			echo("sum="  + formatFloat(vSum(xs),  2))
			echo("mean=" + formatFloat(vMean(xs), 2))
			echo("min="  + formatFloat(vMin(xs),  2))
			echo("max="  + formatFloat(vMax(xs),  2))
			echo("var="  + formatFloat(vVar(xs),  2))
			echo("std="  + formatFloat(vStd(xs),  4))
		}
	`)
	got := runBin(t, bin)
	want := "sum=15.00\nmean=3.00\nmin=1.00\nmax=5.00\nvar=2.00\nstd=1.4142\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeVecElementwise(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let a: float[] = [1.0, 2.0, 3.0]
			let b: float[] = [10.0, 20.0, 30.0]
			let added = vAdd(a, b)
			for v in added { echo("+:" + formatFloat(v, 2)) }
			let scaled = vScale(a, 2.5)
			for v in scaled { echo("k:" + formatFloat(v, 2)) }
			echo("dot=" + formatFloat(vDot(a, b), 2))
		}
	`)
	got := runBin(t, bin)
	want := "+:11.00\n+:22.00\n+:33.00\nk:2.50\nk:5.00\nk:7.50\ndot=140.00\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeVecConstructors(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let lin = linspace(0.0, 1.0, 5)
			for v in lin { echo("l:" + formatFloat(v, 2)) }
			let r = arange(0, 5, 1)
			for n in r { echo("r:" + str(n)) }
		}
	`)
	got := runBin(t, bin)
	want := "l:0.00\nl:0.25\nl:0.50\nl:0.75\nl:1.00\nr:0\nr:1\nr:2\nr:3\nr:4\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeVecCumsum(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let xs: float[] = [1.0, 2.0, 3.0, 4.0]
			let cs = cumsum(xs)
			for v in cs { echo(formatFloat(v, 2)) }
		}
	`)
	got := strings.TrimRight(runBin(t, bin), "\n")
	want := "1.00\n3.00\n6.00\n10.00"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
