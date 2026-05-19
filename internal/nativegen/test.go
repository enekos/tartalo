package nativegen

import (
	"strconv"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/loader"
)

// emitTestFunctions emits one Go closure per `test "..." { ... }` declaration
// in the entry module. The closures are stored alongside their display names
// in a `_tt_tests` slice that the harness drives from main().
func (g *Generator) emitTestFunctions(entry *loader.Module) {
	if entry == nil {
		return
	}
	idx := 0
	g.currentModule = entry
	for _, d := range entry.File.Decls {
		td, ok := d.(*ast.TestDecl)
		if !ok {
			continue
		}
		idx++
		g.writeLine("func _tt_test_" + itoa(idx) + "() {")
		g.indent++
		for _, s := range td.Body.Stmts {
			g.emitStmt(s)
		}
		g.indent--
		g.writeLine("}")
		g.writeLine("")
	}
}

// emitTestRunnerCall builds the `[]_tt_testCase{...}` slice and hands it to
// `_tt_runTests`. The test bodies were emitted by emitTestFunctions; here we
// just point the runner at them in declaration order.
func (g *Generator) emitTestRunnerCall(entry *loader.Module) {
	g.usesRuntimeTestState = true
	g.addImport("fmt")
	g.addImport("os")
	g.addImport("strings")
	if entry == nil {
		g.writeLine(`_tt_runTests("tests", []_tt_testCase{})`)
		return
	}
	type info struct {
		idx   int
		name  string
		xfail bool
	}
	var tests []info
	for _, d := range entry.File.Decls {
		td, ok := d.(*ast.TestDecl)
		if !ok {
			continue
		}
		tests = append(tests, info{
			idx:   len(tests) + 1,
			name:  td.Name,
			xfail: ast.IsXfailTestName(td.Name),
		})
	}
	suite := entry.File.Path
	if suite == "" {
		suite = "tests"
	}
	var b strings.Builder
	b.WriteString("_tt_runTests(")
	b.WriteString(strconv.Quote(suite))
	b.WriteString(", []_tt_testCase{")
	for i, t := range tests {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("{name: ")
		b.WriteString(strconv.Quote(t.name))
		b.WriteString(", fn: _tt_test_" + itoa(t.idx))
		if t.xfail {
			b.WriteString(", xfail: true")
		}
		b.WriteString("}")
	}
	b.WriteString("})")
	g.writeLine(b.String())
}
