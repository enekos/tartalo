package codegen_test

import (
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/codegen"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/parser"
)

var benchSrc = `
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

func BenchmarkCompilePipeline(b *testing.B) {
	for i := 0; i < b.N; i++ {
		toks, _ := lexer.New("bench.tt", benchSrc).Tokenize()
		file, _ := parser.New(toks).Parse("bench.tt")
		info, _ := checker.New().CheckFile(file)
		_ = codegen.New(info).Emit(file)
	}
}

func BenchmarkLexer(b *testing.B) {
	for i := 0; i < b.N; i++ {
		lexer.New("bench.tt", benchSrc).Tokenize()
	}
}

func BenchmarkParser(b *testing.B) {
	toks, _ := lexer.New("bench.tt", benchSrc).Tokenize()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parser.New(toks).Parse("bench.tt")
	}
}

func BenchmarkChecker(b *testing.B) {
	toks, _ := lexer.New("bench.tt", benchSrc).Tokenize()
	file, _ := parser.New(toks).Parse("bench.tt")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		checker.New().CheckFile(file)
	}
}

func BenchmarkCodegen(b *testing.B) {
	toks, _ := lexer.New("bench.tt", benchSrc).Tokenize()
	file, _ := parser.New(toks).Parse("bench.tt")
	info, _ := checker.New().CheckFile(file)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		codegen.New(info).Emit(file)
	}
}
