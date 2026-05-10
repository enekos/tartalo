package main

import (
	"fmt"
	"os"
	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/nativegen"
	"github.com/enekos/tartalo/internal/parser"
)

func main() {
	src, _ := os.ReadFile(os.Args[1])
	toks, _ := lexer.New("test.tt", string(src)).Tokenize()
	file, _ := parser.New(toks).Parse("test.tt")
	mod := &loader.Module{File: file, IsEntry: true}
	info, _ := checker.New().Check([]*loader.Module{mod})
	g := nativegen.New(info)
	_ = g.EmitModules([]*loader.Module{mod})
	
	fmt.Printf("usesRuntimeUnwrap: %v\n", g.usesRuntimeUnwrap)
	fmt.Printf("usesRuntimePtr: %v\n", g.usesRuntimePtr)
	fmt.Printf("usesRuntimeCoalesce: %v\n", g.usesRuntimeCoalesce)
	fmt.Printf("usesRuntimeShellOut: %v\n", g.usesRuntimeShellOut)
	fmt.Printf("usesRuntimeArgs: %v\n", g.usesRuntimeArgs)
	fmt.Printf("usesRuntimeExec: %v\n", g.usesRuntimeExec)
	fmt.Printf("usesRuntimeExecTimeout: %v\n", g.usesRuntimeExecTimeout)
	fmt.Printf("usesRuntimeFile: %v\n", g.usesRuntimeFile)
	fmt.Printf("usesRuntimePath: %v\n", g.usesRuntimePath)
	fmt.Printf("usesRuntimeStat: %v\n", g.usesRuntimeStat)
	fmt.Printf("usesRuntimeJSON: %v\n", g.usesRuntimeJSON)
	fmt.Printf("usesRuntimeRegex: %v\n", g.usesRuntimeRegex)
	fmt.Printf("usesRuntimeFormatTime: %v\n", g.usesRuntimeFormatTime)
	fmt.Printf("usesRuntimeFloat: %v\n", g.usesRuntimeFloat)
	fmt.Printf("usesRuntimeVec: %v\n", g.usesRuntimeVec)
	fmt.Printf("usesRuntimeHigherOrder: %v\n", g.usesRuntimeHigherOrder)
	fmt.Printf("usesRuntimeFetch: %v\n", g.usesRuntimeFetch)
	fmt.Printf("usesRuntimeTestState: %v\n", g.usesRuntimeTestState)
	fmt.Printf("usesRuntimeEvalState: %v\n", g.usesRuntimeEvalState)
	fmt.Printf("usesRuntimeEnv: %v\n", g.usesRuntimeEnv)
	fmt.Printf("usesRuntimeNow: %v\n", g.usesRuntimeNow)
	fmt.Printf("usesRuntimeTry: %v\n", g.usesRuntimeTry)
	fmt.Printf("usesRuntimeTypeError: %v\n", g.usesRuntimeTypeError)
	fmt.Printf("csvReaders: %d\n", len(g.csvReaders))
	fmt.Printf("csvWriters: %d\n", len(g.csvWriters))
	fmt.Printf("agents: %d\n", len(g.agents))
	fmt.Printf("tools: %d\n", len(g.tools))
}
