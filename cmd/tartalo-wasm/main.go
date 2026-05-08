//go:build js && wasm

// tartalo-wasm exposes the compiler as a single JS function on the global
// object: `tartaloCompile(source: string) -> { sh: string, errors: string[],
// rendered: string }`. Imports are not supported — the playground compiles a
// single file in isolation. Anything multi-module belongs in the CLI.
package main

import (
	"strings"
	"syscall/js"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/codegen"
	"github.com/enekos/tartalo/internal/diag"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/parser"
)

const playgroundFile = "playground.tt"

func main() {
	js.Global().Set("tartaloCompile", js.FuncOf(compile))
	select {} // keep the runtime alive so the exported func stays callable
}

func compile(_ js.Value, args []js.Value) any {
	if len(args) < 1 {
		return result("", []error{}, "tartaloCompile: expected (source: string)")
	}
	src := args[0].String()

	toks, lerrs := lexer.New(playgroundFile, src).Tokenize()
	if len(lerrs) > 0 {
		return result("", lerrs, renderErrs(lerrs, src))
	}
	file, perrs := parser.New(toks).Parse(playgroundFile)
	if len(perrs) > 0 {
		return result("", perrs, renderErrs(perrs, src))
	}
	if file == nil {
		return result("", []error{}, "parse failed")
	}
	if len(file.Imports) > 0 {
		err := diag.New(file.Imports[0].PathPos, "imports are not supported in the web playground")
		return result("", []error{err}, renderErrs([]error{err}, src))
	}

	mod := &loader.Module{
		AbsPath:    playgroundFile,
		File:       file,
		IsEntry:    true,
		Source:     src,
		SourceName: playgroundFile,
	}
	info, cerrs := checker.New().Check([]*loader.Module{mod})
	if len(cerrs) > 0 {
		return result("", cerrs, renderErrs(cerrs, src))
	}
	sh := codegen.New(info).EmitModules([]*loader.Module{mod})
	return result(sh, nil, "")
}

func result(sh string, errs []error, rendered string) any {
	out := map[string]any{"sh": sh, "rendered": rendered}
	msgs := make([]any, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	out["errors"] = msgs
	return js.ValueOf(out)
}

func renderErrs(errs []error, src string) string {
	var b strings.Builder
	srcs := diag.MapSources{playgroundFile: src}
	diag.Render(&b, diag.FromErrors(errs), srcs, false)
	return b.String()
}
