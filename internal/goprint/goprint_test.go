package goprint_test

import (
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/goprint"
)

func TestImportsEmptyWritesNothing(t *testing.T) {
	var b strings.Builder
	goprint.Imports(&b, nil)
	if b.Len() != 0 {
		t.Fatalf("expected empty output, got %q", b.String())
	}
}

func TestImportsSingle(t *testing.T) {
	var b strings.Builder
	goprint.Imports(&b, []string{"fmt"})
	want := "import \"fmt\"\n\n"
	if b.String() != want {
		t.Fatalf("got %q want %q", b.String(), want)
	}
}

func TestImportsGroupedSortedDedup(t *testing.T) {
	var b strings.Builder
	goprint.Imports(&b, []string{"strings", "fmt", "fmt", "os"})
	want := "import (\n\t\"fmt\"\n\t\"os\"\n\t\"strings\"\n)\n\n"
	if b.String() != want {
		t.Fatalf("got %q want %q", b.String(), want)
	}
}

func TestStructAlignsTypeColumn(t *testing.T) {
	buf := goprint.NewBuf(64)
	goprint.Struct(buf, "Tt_Response", []goprint.StructField{
		{Name: "F_status", Type: "int64"},
		{Name: "F_ok", Type: "bool"},
		{Name: "F_body", Type: "string"},
		{Name: "F_followRedirects", Type: "bool"},
	})
	got := buf.String()
	want := "type Tt_Response struct {\n" +
		"\tF_status          int64\n" +
		"\tF_ok              bool\n" +
		"\tF_body            string\n" +
		"\tF_followRedirects bool\n" +
		"}\n\n"
	if got != want {
		t.Fatalf("aligned struct mismatch:\n--got--\n%s\n--want--\n%s", got, want)
	}
}

func TestBinAtomsNoParens(t *testing.T) {
	e := goprint.Bin{
		Op:  "+",
		Lhs: goprint.Atom{Text: "a"},
		Rhs: goprint.Atom{Text: "b"},
	}
	if got := goprint.RenderString(e); got != "a + b" {
		t.Fatalf("got %q", got)
	}
}

func TestBinHigherPrecChildNoParens(t *testing.T) {
	// (a*b) + c — child has higher precedence; no parens needed.
	e := goprint.Bin{
		Op: "+",
		Lhs: goprint.Bin{
			Op:  "*",
			Lhs: goprint.Atom{Text: "a"},
			Rhs: goprint.Atom{Text: "b"},
		},
		Rhs: goprint.Atom{Text: "c"},
	}
	if got := goprint.RenderString(e); got != "a * b + c" {
		t.Fatalf("got %q", got)
	}
}

func TestBinLowerPrecChildLeftWraps(t *testing.T) {
	// (a||b) * c — child has lower precedence; must wrap.
	e := goprint.Bin{
		Op: "*",
		Lhs: goprint.Bin{
			Op:  "||",
			Lhs: goprint.Atom{Text: "a"},
			Rhs: goprint.Atom{Text: "b"},
		},
		Rhs: goprint.Atom{Text: "c"},
	}
	if got := goprint.RenderString(e); got != "(a || b) * c" {
		t.Fatalf("got %q", got)
	}
}

func TestBinSameOpRightAssocWraps(t *testing.T) {
	// a - (b - c): for safety, right-side same-prec wraps so subtraction
	// retains its grouping.
	e := goprint.Bin{
		Op:  "-",
		Lhs: goprint.Atom{Text: "a"},
		Rhs: goprint.Bin{
			Op:  "-",
			Lhs: goprint.Atom{Text: "b"},
			Rhs: goprint.Atom{Text: "c"},
		},
	}
	if got := goprint.RenderString(e); got != "a - (b - c)" {
		t.Fatalf("got %q", got)
	}
}

func TestBinSameOpLeftAssocNoWrap(t *testing.T) {
	// (a - b) - c: left side same prec — no wrap needed.
	e := goprint.Bin{
		Op: "-",
		Lhs: goprint.Bin{
			Op:  "-",
			Lhs: goprint.Atom{Text: "a"},
			Rhs: goprint.Atom{Text: "b"},
		},
		Rhs: goprint.Atom{Text: "c"},
	}
	if got := goprint.RenderString(e); got != "a - b - c" {
		t.Fatalf("got %q", got)
	}
}

func TestUnAtomNoWrap(t *testing.T) {
	e := goprint.Un{Op: "!", Operand: goprint.Atom{Text: "ok"}}
	if got := goprint.RenderString(e); got != "!ok" {
		t.Fatalf("got %q", got)
	}
}

func TestUnBinaryOperandWraps(t *testing.T) {
	e := goprint.Un{
		Op: "!",
		Operand: goprint.Bin{
			Op:  "==",
			Lhs: goprint.Atom{Text: "a"},
			Rhs: goprint.Atom{Text: "b"},
		},
	}
	if got := goprint.RenderString(e); got != "!(a == b)" {
		t.Fatalf("got %q", got)
	}
}

func TestUnUnaryOperandWrapsAvoidsLexerCollision(t *testing.T) {
	// Without parens this would tokenise as `--x` (DEC) and fail to parse.
	e := goprint.Un{
		Op:      "-",
		Operand: goprint.Un{Op: "-", Operand: goprint.Atom{Text: "x"}},
	}
	if got := goprint.RenderString(e); got != "-(-x)" {
		t.Fatalf("got %q", got)
	}
}

func TestRawWithDeclaredPrecActsAsBinary(t *testing.T) {
	// A pre-rendered low-precedence operand should still trigger wrapping
	// when nested under a tighter parent.
	e := goprint.Bin{
		Op:  "*",
		Lhs: goprint.Raw{Text: "a + b", P: goprint.PrecAdd},
		Rhs: goprint.Atom{Text: "c"},
	}
	if got := goprint.RenderString(e); got != "(a + b) * c" {
		t.Fatalf("got %q", got)
	}
}

func TestIIFERendersMultiLine(t *testing.T) {
	got := goprint.IIFE{
		ReturnType: "int64",
		Body: []string{
			`_v := tt_x`,
			`if _v < 0 { return -_v }`,
			`return _v`,
		},
	}.String()
	want := "func() int64 {\n" +
		"\t_v := tt_x\n" +
		"\tif _v < 0 { return -_v }\n" +
		"\treturn _v\n" +
		"}()"
	if got != want {
		t.Fatalf("IIFE mismatch:\n--got--\n%s\n--want--\n%s", got, want)
	}
}

func TestIIFEVoid(t *testing.T) {
	got := goprint.IIFE{
		Body: []string{`os.Exit(0)`},
	}.String()
	want := "func() {\n\tos.Exit(0)\n}()"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBufLineRespectsIndent(t *testing.T) {
	buf := goprint.NewBuf(0)
	buf.Line("func f() {")
	buf.Indent()
	buf.Line("return 1")
	buf.Dedent()
	buf.Line("}")
	want := "func f() {\n\treturn 1\n}\n"
	if buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}
