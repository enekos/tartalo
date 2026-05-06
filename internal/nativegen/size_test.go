package nativegen

import (
	"fmt"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/parser"
)

var sizeBenchSrc = `
func fib(n: number): number {
  if n <= 1 { return n }
  return fib(n - 1) + fib(n - 2)
}

func sumArray(xs: number[]): number {
  let total = 0
  for x in xs {
    total = total + x
  }
  return total
}

func main(): void {
  let f10 = fib(10)
  let xs: number[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
  let s = sumArray(xs)
  let maxVal = 0
  for i in 0..100 {
    if i > maxVal {
      maxVal = i
    }
  }
  echo(str(f10))
}
`

func TestOutputSize(t *testing.T) {
	toks, _ := lexer.New("bench.tt", sizeBenchSrc).Tokenize()
	file, _ := parser.New(toks).Parse("bench.tt")
	mod := &loader.Module{File: file, IsEntry: true}
	info, _ := checker.New().Check([]*loader.Module{mod})
	out := New(info).EmitModules([]*loader.Module{mod})
	fmt.Println("nativegen output size:", len(out))
}
