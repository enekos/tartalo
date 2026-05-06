package codegen_test

import "testing"

func TestVecReductions(t *testing.T) {
	sh := compile(t, `
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
	out := runShell(t, sh)
	want := "sum=15.00\nmean=3.00\nmin=1.00\nmax=5.00\nvar=2.00\nstd=1.4142\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestVecReductionsEmpty(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: float[] = []
			echo("sum="  + formatFloat(vSum(xs),  2))
			echo("mean=" + formatFloat(vMean(xs), 2))
			echo("min="  + formatFloat(vMin(xs),  2))
			echo("max="  + formatFloat(vMax(xs),  2))
			echo("var="  + formatFloat(vVar(xs),  2))
			echo("std="  + formatFloat(vStd(xs),  2))
		}
	`)
	out := runShell(t, sh)
	want := "sum=0.00\nmean=0.00\nmin=0.00\nmax=0.00\nvar=0.00\nstd=0.00\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestVecElementwise(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a: float[] = [1.0, 2.0, 3.0]
			let b: float[] = [10.0, 20.0, 30.0]
			let added = vAdd(a, b)
			for v in added { echo("+:" + formatFloat(v, 2)) }
			let subbed = vSub(b, a)
			for v in subbed { echo("-:" + formatFloat(v, 2)) }
			let mulled = vMul(a, b)
			for v in mulled { echo("*:" + formatFloat(v, 2)) }
			let scaled = vScale(a, 2.5)
			for v in scaled { echo("k:" + formatFloat(v, 2)) }
		}
	`)
	out := runShell(t, sh)
	want := "+:11.00\n+:22.00\n+:33.00\n-:9.00\n-:18.00\n-:27.00\n*:10.00\n*:40.00\n*:90.00\nk:2.50\nk:5.00\nk:7.50\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestVecDotMismatchedLength(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a: float[] = [1.0, 2.0, 3.0, 4.0]
			let b: float[] = [10.0, 20.0]
			echo(formatFloat(vDot(a, b), 2))
			let c = vAdd(a, b)
			echo(str(len(c)))
		}
	`)
	out := runShell(t, sh)
	want := "50.00\n2\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestVecConstructors(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let lin = linspace(0.0, 1.0, 5)
			for v in lin { echo("l:" + formatFloat(v, 2)) }

			let r = arange(0, 5, 1)
			for n in r { echo("r:" + str(n)) }

			let backwards = arange(5, 0, -1)
			for n in backwards { echo("b:" + str(n)) }
		}
	`)
	out := runShell(t, sh)
	want := "l:0.00\nl:0.25\nl:0.50\nl:0.75\nl:1.00\nr:0\nr:1\nr:2\nr:3\nr:4\nb:5\nb:4\nb:3\nb:2\nb:1\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestVecCumsum(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: float[] = [1.0, 2.0, 3.0, 4.0]
			let cs = cumsum(xs)
			for v in cs { echo(formatFloat(v, 2)) }

			let empty: float[] = []
			let cse = cumsum(empty)
			echo("len=" + str(len(cse)))
		}
	`)
	out := runShell(t, sh)
	want := "1.00\n3.00\n6.00\n10.00\nlen=0\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestVecRejectsNonFloatArray(t *testing.T) {
	src := `
		func main(): void {
			let xs = [1, 2, 3]
			echo(formatFloat(vSum(xs), 2))
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 {
		t.Fatal("expected type error: vSum requires float[], got number[]")
	}
}
