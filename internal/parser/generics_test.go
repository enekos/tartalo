package parser_test

import (
	"testing"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/parser"
)

func parseDecls(t *testing.T, src string) []ast.Decl {
	t.Helper()
	toks, lerrs := lexer.New("t.tt", src).Tokenize()
	if len(lerrs) > 0 {
		t.Fatalf("lex errors: %v", lerrs)
	}
	f, perrs := parser.New(toks).Parse("t.tt")
	if len(perrs) > 0 {
		t.Fatalf("parse errors: %v", perrs)
	}
	return f.Decls
}

func TestParseGenericFuncSingleParam(t *testing.T) {
	decls := parseDecls(t, `func id<T>(x: T): T { return x }`)
	fd, ok := decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("decl 0 is not FuncDecl, got %T", decls[0])
	}
	if len(fd.TypeParams) != 1 || fd.TypeParams[0].Name != "T" {
		t.Errorf("want one type param T, got %+v", fd.TypeParams)
	}
}

func TestParseGenericFuncMultipleParams(t *testing.T) {
	decls := parseDecls(t, `func mapOver<T, U>(xs: T[], f: func(T): U): U[] {
		let result: U[] = []
		return result
	}`)
	fd := decls[0].(*ast.FuncDecl)
	if len(fd.TypeParams) != 2 {
		t.Fatalf("want 2 type params, got %d", len(fd.TypeParams))
	}
	if fd.TypeParams[0].Name != "T" || fd.TypeParams[1].Name != "U" {
		t.Errorf("got %+v", fd.TypeParams)
	}
}

func TestParseNonGenericFuncStillWorks(t *testing.T) {
	decls := parseDecls(t, `func plain(n: number): number { return n }`)
	fd := decls[0].(*ast.FuncDecl)
	if len(fd.TypeParams) != 0 {
		t.Errorf("expected no type params, got %+v", fd.TypeParams)
	}
}

func TestParseGenericEmptyParamListErrors(t *testing.T) {
	toks, _ := lexer.New("t.tt", `func id<>(x: number): number { return x }`).Tokenize()
	_, errs := parser.New(toks).Parse("t.tt")
	if len(errs) == 0 {
		t.Fatal("want parse error for empty type-parameter list")
	}
	found := false
	for _, e := range errs {
		if contains(e.Error(), "type parameter list cannot be empty") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing expected error, got %v", errs)
	}
}

// contains is a tiny strings.Contains stand-in to avoid pulling strings into
// every test file's import block.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
