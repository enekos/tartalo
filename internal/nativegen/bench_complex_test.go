package nativegen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/nativegen"
	"github.com/enekos/tartalo/internal/parser"
)

// benchCase holds a loaded module ready for repeated nativegen emission.
type benchCase struct {
	name    string
	modules []*loader.Module
	info    *checker.TypeInfo
}

func mustLoad(name, src string) *benchCase {
	toks, lerrs := lexer.New(name, src).Tokenize()
	if len(lerrs) > 0 {
		panic(lerrs)
	}
	file, perrs := parser.New(toks).Parse(name)
	if len(perrs) > 0 {
		panic(perrs)
	}
	mod := &loader.Module{File: file, IsEntry: true, AbsPath: filepath.Join("/Users/enekos/tartalo", name)}
	info, cerrs := checker.New().Check([]*loader.Module{mod})
	if len(cerrs) > 0 {
		panic(cerrs)
	}
	return &benchCase{name: name, modules: []*loader.Module{mod}, info: info}
}

func loadFile(path string) *benchCase {
	src, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	name := filepath.Base(path)
	return mustLoad(name, string(src))
}

var (
	benchHello    = loadFile("../../examples/hello.tt")
	benchFizzBuzz = loadFile("../../examples/fizzbuzz.tt")
	benchArray    = loadFile("../../examples/array.tt")
	benchStrings  = loadFile("../../examples/strings.tt")
	benchRecord   = loadFile("../../examples/record.tt")
	benchMatch    = loadFile("../../examples/match.tt")
	benchNumpy    = loadFile("../../examples/numpy.tt")
	benchPandas   = loadFile("../../examples/pandas.tt")
	benchPerf     = loadFile("../../scripts/bench_perf.tt")

	benchAgentDemo = mustLoad("agent_demo.tt", `
// agent_demo.tt — a self-contained agent that uses every piece of the
// agent-platform surface.

tool listFiles(): string {
  desc("list files in the current working directory")
  return exec("ls").stdout
}

tool greet(name: string): string {
  desc("greet someone by name")
  return "Hello, " + name + "!"
}

agent assistant(question: string) uses (listFiles, greet): string !ai {
  desc("answer a question, possibly using tools")
  budget(2)
  trace("agent.start", question)
  let prompt = "Tools: " + agentTools() + "\nQ: " + question
  let answer = llm(prompt)
  trace("agent.end", answer)
  return answer
}

func main(): void {
  echo("== schemas ==")
  echo(toolSchemas())
  echo("")
  echo("== greet (direct) ==")
  echo(greet("Tartalo"))
  echo("")
  echo("== greet (via callTool) ==")
  echo(callTool("greet", "world"))
  echo("")
  echo("== assistant ==")
  let question = "what is 2+2?"
  let answer = spawnAgent("assistant", question)
  echo(answer)
}
`)

	benchMega = mustLoad("mega.tt", `
type Point = { x: number, y: number }
type Person = { name: string, age: number }

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

func double(n: number): number { return n * 2 }
func isEven(n: number): bool { return n % 2 == 0 }
func add(a: number, b: number): number { return a + b }

func buildStrings(n: number): string {
  let result = ""
  for i in 0..n {
    result = result + "a"
  }
  return result
}

func greet(who: string): string {
  return "Hello, " + who + "!"
}

func describe(p: Person): string {
  return p.name + " is " + str(p.age)
}

func dist(p: Point): number {
  return p.x + p.y
}

func work(n: number): number {
  let result = 0
  for i in 0..n {
    if i % 2 == 0 {
      result = result + i
    } else {
      result = result - i
    }
  }
  return result
}

func main(): void {
  let f10 = fib(10)
  let xs: number[] = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
  let s = sumArray(xs)
  let str10 = buildStrings(10)
  let p = Point{ x: 10, y: 20 }
  let d = dist(p)
  let alice = Person{ name: "Alice", age: 30 }
  let desc = describe(alice)
  let doubled = map(xs, double)
  let evens = filter(xs, isEven)
  let total = reduce(xs, 0, add)
  let w = work(100)
  let greeting = greet("world")
  let parts = split("a,b,c", ",")
  let joined = join(parts, "+")
  let trimmed = trim("  hello  ")
  let uppered = upper("hello")
  let lowered = lower("HELLO")
  let replaced = replace("a.b.c", ".", "/")
  let sliced = slice("abcdef", 1, 4)
  let maxVal = 0
  for i in 0..100 {
    if i > maxVal { maxVal = i }
  }
  let ok = false
  if f10 == 55 {
    if s == 55 {
      if len(str10) == 10 {
        if d == 30 {
          if total == 55 {
            if w == 50 {
              if greeting == "Hello, world!" {
                if joined == "a+b+c" {
                  if trimmed == "hello" {
                    if uppered == "HELLO" {
                      if lowered == "hello" {
                        if replaced == "a/b/c" {
                          if sliced == "bcd" {
                            ok = true
                          }
                        }
                      }
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
  if ok { echo("ok") }
}
`)
)

func BenchmarkNativegenSimple(b *testing.B) {
	c := benchPerf
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}
}

func BenchmarkNativegenHello(b *testing.B) {
	c := benchHello
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}
}

func BenchmarkNativegenFizzBuzz(b *testing.B) {
	c := benchFizzBuzz
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}
}

func BenchmarkNativegenArray(b *testing.B) {
	c := benchArray
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}
}

func BenchmarkNativegenStrings(b *testing.B) {
	c := benchStrings
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}
}

func BenchmarkNativegenRecord(b *testing.B) {
	c := benchRecord
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}
}

func BenchmarkNativegenMatch(b *testing.B) {
	c := benchMatch
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}
}

func BenchmarkNativegenNumpy(b *testing.B) {
	c := benchNumpy
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}
}

func BenchmarkNativegenPandas(b *testing.B) {
	c := benchPandas
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}
}

func BenchmarkNativegenAgentDemo(b *testing.B) {
	c := benchAgentDemo
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}
}

func BenchmarkNativegenMega(b *testing.B) {
	c := benchMega
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}
}

// BenchmarkNativegenSuite runs all benchmarks in one loop and reports
// a composite score weighted by script complexity. This is the primary
// autoresearch metric.
func BenchmarkNativegenSuite(b *testing.B) {
	cases := []*benchCase{
		benchHello,
		benchFizzBuzz,
		benchArray,
		benchStrings,
		benchRecord,
		benchMatch,
		benchNumpy,
		benchPandas,
		benchAgentDemo,
		benchMega,
	}

	// Pre-warm to ensure stable measurements.
	for _, c := range cases {
		_ = nativegen.New(c.info).EmitModules(c.modules)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, c := range cases {
			_ = nativegen.New(c.info).EmitModules(c.modules)
		}
	}
}

// BenchmarkNativegenSuiteAllocs is the same suite with allocation reporting.
func BenchmarkNativegenSuiteAllocs(b *testing.B) {
	cases := []*benchCase{
		benchHello,
		benchFizzBuzz,
		benchArray,
		benchStrings,
		benchRecord,
		benchMatch,
		benchNumpy,
		benchPandas,
		benchAgentDemo,
		benchMega,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, c := range cases {
			_ = nativegen.New(c.info).EmitModules(c.modules)
		}
	}
}

// BenchmarkNativegenOutputSize measures the generated Go source size for
// each complex script. Not a time benchmark — runs once.
func BenchmarkNativegenOutputSize(b *testing.B) {
	cases := []*benchCase{
		benchHello,
		benchFizzBuzz,
		benchArray,
		benchStrings,
		benchRecord,
		benchMatch,
		benchNumpy,
		benchPandas,
		benchAgentDemo,
		benchMega,
	}

	for _, c := range cases {
		out := nativegen.New(c.info).EmitModules(c.modules)
		b.Run(c.name, func(b *testing.B) {
			b.ReportMetric(float64(len(out)), "output_bytes")
			b.ReportMetric(float64(strings.Count(out, "\n")), "output_lines")
			for i := 0; i < b.N; i++ {
				_ = nativegen.New(c.info).EmitModules(c.modules)
			}
		})
	}
}
