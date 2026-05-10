package main

import (
	"fmt"
	"os"
	"strings"

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
	out := nativegen.EmitSource([]*loader.Module{mod}, info)
	lines := strings.Count(out, "\n")
	fmt.Printf("lines: %d\n", lines)
}
