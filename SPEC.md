# Tartalo Language Spec (v0)

Tartalo is a small, statically-typed scripting language that compiles to **POSIX sh**. It aims to feel like a slim middle-ground between TypeScript and Go, while taking shell scripting seriously as the runtime target.

> Status: pre-alpha. Everything in this document is subject to change.

## Goals

- **Strong static typing.** No undefined-variable surprises, no implicit string-vs-number bugs.
- **Readable shell output.** The generated `.sh` should be POSIX-portable and reasonable to read.
- **Quote-by-default safety.** All expansions are double-quoted in codegen so spaces and globs do not bite.
- **Shell as a first-class concern.** Running commands and using their output is part of the syntax, not a wart.

## Non-goals (for v0)

- Full TS/JS feature parity. No classes, no async.
- Bash-isms (arrays, `[[ ]]`, process substitution). The output is plain `sh`.
- Performance competitive with hand-tuned shell.

## File extension

`.tt`

## Modules

A program may span multiple files. Imports go at the top of a file; everything
else (functions, types, variables) follows. Only declarations prefixed with
`export` are visible outside their module:

```tartalo
// lib/math.tt
export type Pair = { a: number, b: number }

export func sumPair(p: Pair): number {
  return p.a + p.b
}

// (no `export`) — private to this module
func helper(): string { return "shh" }
```

```tartalo
// main.tt
import { Pair, sumPair } from "./lib/math.tt"

func main(): void {
  let p: Pair = Pair{a: 7, b: 35}
  echo(str(sumPair(p)))
}
```

Module paths are interpreted relative to the importing file's directory.
The compiler bundles every reachable file into one `.sh` output, with global
names from imported modules mangled to `__m<id>__<name>` to avoid collisions.
The entry module's symbols keep their plain names for readability.

Constraints in v0:
- Only the named-import form: `import { a, b } from "./path.tt"`.
- Imports must reference names that the target module declared with
  `export`. Cycles are reported as errors.
- Two record types declared with the same name in different modules are
  distinct types; nominal equality is by-pointer, not by-name.

## Lexical structure

- Line comments: `// ...`
- Identifiers: `[A-Za-z_][A-Za-z0-9_]*`
- Numbers: integer literals only in v0 (`42`, `-3`).
- Strings: double-quoted, with `\n \t \\ \" \$` escapes and `${expr}` interpolation.
- Command literals: backticks, e.g. `` `ls -1` ``. Substitutes to a `string` (stdout, trailing newline trimmed).
- Keywords: `let`, `const`, `func`, `return`, `if`, `else`, `for`, `in`, `match`, `type`, `import`, `export`, `test`, `defer`, `parallel`, `task`, `tool`, `agent`, `as`, `null`, `true`, `false`, `string`, `number`, `float`, `bool`, `void`.

## Types (v0)

| Tartalo  | Generated sh representation                                                                |
| -------- | ------------------------------------------------------------------------------------------ |
| `string` | a shell variable holding text                                                              |
| `number` | a shell variable holding a base-10 int                                                     |
| `float`  | a shell variable holding a textual float; arithmetic done via awk                          |
| `bool`   | a shell variable holding `1` (true) or `0` (false) — same encoding as `$(( ))` comparisons |
| `void`   | functions with no return value                                                             |
| `T[]`    | a shell variable holding the elements joined by newlines                                   |
| `func(T...): R` | a shell variable holding the mangled function name (callable via `"$f" args`)       |

There is no implicit conversion. `"foo" + 1` is a type error. Use `str(n)` to convert a number to a string.

> **Caveat for arrays:** because the codegen represents `T[]` as a newline-joined
> string, individual elements must not contain literal newlines. This is enough
> for typical scripting use (filenames, ids, words) and keeps the generated sh
> predictable, but it is a real limitation worth knowing about.

## Declarations

```tartalo
let name: string = "world"
const PI: number = 3        // const → readonly in sh
let active: bool = true

// Type annotations on `let`/`const` are optional; inferred from the initializer.
let inferred = "hello"      // string
let n = 42                  // number
let big = n > 10            // bool
```

Empty array literals always need an annotation, since there is nothing to infer
the element type from:

```tartalo
let xs: string[] = []
```

Function parameter and return types are still always required.

## Optional types

Any non-array, non-optional type `T` can be made nullable with the postfix
`?`:

```tartalo
let x: string? = "hi"        // non-null
let y: string? = null        // null
let z: string  = x ?? "fallback"   // unwrap with default
let w: string  = x!                // forced unwrap (aborts if null)
```

Allowed operations on optional values:

- `expr ?? default` — coalesce. Result is `T` (non-optional). The default
  must have type `T`.
- `expr!` — forced unwrap. Aborts the script with a diagnostic if the
  operand is null.
- `expr == null`, `expr != null` — null check.

Direct equality, ordering, arithmetic, etc. are *rejected* on optional
values — use `??` or `!` first. There is no flow-narrowing in v0, so even
inside an `if x != null { … }` block `x` is still `T?`; use `x!` to access
the underlying value.

`null` may not appear by itself (`let z = null` is rejected); always provide
the type via an annotation, the surrounding context (param/return), or a
non-null sibling expression.

Optional **fields** in records are supported:

```tartalo
type User = {
  name: string,
  nickname: string?,
}

let u = User{name: "alice", nickname: null}
echo(u.nickname ?? u.name)
```

`env(name): string?` — note that the empty string and "unset" are now
distinct: an env var set to `""` returns the empty string (non-null), an
unset var returns `null`.

### Codegen sketch

Each optional value is two shell variables: the value, and a `__null` flag
(1 = null, 0 = present). Function parameters of optional type consume two
positional args; optional fields in records carry their flag inline; the
`__ret` return slot has a sibling `__ret__null`.

## Records

Named record types group a fixed set of fields:

```tartalo
type Person = {
  name: string,
  age: number,
}

func main(): void {
  let p: Person = { name: "Alice", age: 30 }
  echo(p.name + " is " + str(p.age))
  p.age = p.age + 1
  echo(str(p.age))
}
```

Record literals must appear in a context where the expected type is known —
either as the initialiser of an annotated `let`/`const`, the right-hand side
of an assignment to a record-typed variable, the argument of a record-typed
parameter, or the value of a `return` whose function returns a record.

Records are passed and returned by **value**: assigning one record to another
copies every field, and mutations on the copy do not affect the original.

Field types may be:

- a primitive (`string`, `number`, `bool`),
- an optional primitive (`string?`, `number?`, `bool?`),
- another record (nested arbitrarily deep, as long as the type graph is
  acyclic — `type Loop = { next: Loop }` is rejected), or
- an array of primitives (`string[]`, `number[]`, `bool[]`, `float[]`).

```tartalo
type Addr   = { city: string, zip: number }
type Person = { name: string, addr: Addr, tags: string[] }

let p: Person = Person{
  name: "Alice",
  addr: Addr{city: "Madrid", zip: 28001},
  tags: ["admin", "ops"],
}
echo(p.addr.city + " #" + str(len(p.tags)))
```

### v0 limitations

- No optional records (`Addr?`) as fields or values.
- Scalar `float` is not allowed as a record field (use `float[]` if you need
  float storage in a record).
- No structural typing — record types are always referred to by name.
- No equality between records yet — compare individual fields.
- Array elements may be records, but those records cannot themselves contain
  array fields. The row-based encoding uses newlines to separate elements,
  which collides with the newline-joined array representation.

### Record spread

A record literal may begin with a `...source` spread that copies every field
from `source` (which must have the same record type as the literal). Any
named fields after the spread override the corresponding fields from the
source:

```tartalo
type Person = { name: string, age: number }

let alice: Person = Person{name: "Alice", age: 30}
let older: Person = Person{...alice, age: 31}
```

The spread must be the first entry in the literal. Cross-type spread
(copying fields from a structurally-similar but different record type) is
not allowed — use `as` instead.

### Type casts

`expr as TargetRecord` reinterprets a record value as a different record
type when the target's field set is a subset of the source's, with each
shared field's type assignable from source to target. This is useful for
narrowing wide records into a purpose-built shape:

```tartalo
type RawUser   = { name: string, age: number, email: string }
type ShortUser = { name: string, age: number }

let raw: RawUser = RawUser{name: "Alice", age: 30, email: "a@x"}
let short: ShortUser = raw as ShortUser
```

Casts are restricted to record-to-record conversions in v0; primitives,
arrays, and sums use their existing builtins (`str`, `num`, `floatOf`,
`intOf`).

## Arrays of records

`Person[]` is supported when each leaf of the element record is a primitive
or optional primitive (no array leaves):

```tartalo
type Person = { name: string, age: number }

func main(): void {
  let people: Person[] = [
    Person{name: "Alice", age: 30},
    Person{name: "Bob",   age: 25},
  ]
  echo(str(len(people)))
  echo(people[0].name)
  for p in people {
    echo(p.name + "/" + str(p.age))
  }
}
```

### Codegen sketch

The array lives in one shell variable, with rows separated by newlines and
leaf fields within a row separated by ASCII Unit Separator (`\037`,
materialised at script startup as `${__tt_us}`). `xs[i]` extracts a row with
`awk` and splits it back into a fresh record prefix using POSIX parameter
expansion. `for p in xs { ... }` walks each row and binds the loop variable's
leaves the same way. Mutating an element field in place (`xs[i].a = 5`) is
not yet supported.

### Codegen

Each record value is represented as a **name prefix**: a record-typed variable
named `p` lives as the set of shell variables `p__name`, `p__age`, etc. There
is no top-level `p` variable. Nested records flatten by extending the prefix
(`p.addr.city` lives at `p__addr__city`); array fields are a single
newline-joined slot (`p__tags`). Function calls expand record arguments into
one positional parameter per leaf field; record returns write into
`__ret__<leaf>` and the caller copies them into the destination prefix.

## Functions

```tartalo
func greet(name: string): string {
  return "Hello, " + name
}

func main(): void {
  echo(greet("world"))
}
```

Functions compile to sh functions. Parameters are positional. Return values are passed back via a hidden `__ret` variable (sh has no return values in the language sense, only exit codes).

## Control flow

```tartalo
if count > 10 {
  echo("big")
} else if count > 0 {
  echo("small")
} else {
  echo("zero or less")
}

for i in 0..10 {
  echo(str(i))
}

for line in `ls -1` {
  echo(line)
}

for x in [10, 20, 30] {
  echo(str(x))
}
```

`a..b` is a half-open numeric range.

`match` dispatches on a primitive value:

```tartalo
match action {
  "build" | "compile" => echo("compiling")
  "run"               => echo("running")
  ""                  => echo("usage: ACTION=...")
  _                   => echo("unknown: " + action)
}
```

Patterns are literal `string`, `number`, or `bool` values, with `|` for
alternatives and `_` for the wildcard. Arms compile to a sh `case`. String and
numeric patterns are single-quoted, so glob metacharacters in the pattern
match literally.

`match` also dispatches on a sum-typed subject (see "Tagged unions" below)
using variant patterns:

```tartalo
match shape {
  Circle{r}        => echo("circle r=" + str(r))
  Rectangle{w, h}  => echo("rect " + str(w * h))
  Empty            => echo("nothing")
}
```

## Tagged unions (sum types)

A `type` declaration may list `|`-separated variants. Each variant is either
a unit tag or carries a record-style payload:

```tartalo
type Shape =
  Circle{r: number}
  | Rectangle{w: number, h: number}
  | Empty
```

Construction uses the variant name. Unit variants are bare identifiers;
data-carrying variants use the record-literal form:

```tartalo
let s: Shape = Circle{r: 4}
let e: Shape = Empty
```

Destructuring is via `match`. A variant pattern names the variant and lists
the fields to bind into local variables of the arm:

```tartalo
match s {
  Circle{r}       => echo("c " + str(r))
  Rectangle{w, h} => echo("r " + str(w * h))
  Empty           => echo("e")
}
```

### v0 limitations

- Variant fields must be primitives or optional primitives. No nested
  records, arrays, or sums in payloads.
- `match` is a statement, not an expression.
- No exhaustiveness check beyond requiring `_` when a variant is missing.

### Codegen sketch

A sum value at prefix `s` is the set of shell variables `s__tag` (the
variant name as a string), plus `s__<Variant>__<field>` for every variant's
fields. Only the active variant's slots are meaningful at runtime; the
others are zero-initialised so they are safe to read under `set -u`. `match`
on a sum compiles to a `case` over `${s__tag}`, and bindings inside an arm
are copied from the variant's slots into plain locals.

## Defer

`defer { ... }` registers a block to run when the enclosing function exits.
Multiple defers in a single function run in last-registered-first-run
(LIFO) order:

```tartalo
func work(): void {
  defer { echo("a") }
  defer { echo("b") }
  echo("body")     // prints body, then b, then a
}
```

A defer body may not contain `return`, but other side effects are fine.
Defer fires on every explicit `return`, on fall-through end-of-body, and on
the early-return path of the `?` operator. It does **not** fire when the
script is aborted with `exit()`.

### Codegen sketch

Each defer block becomes a generated sh function whose name is pushed onto
a per-function `__tt_defstack` (colon-separated). Before each return the
runtime helper `__tt_run_defers` pops names from the head of the stack and
invokes them. Sh's dynamic-scoped locals make the defer body see the
enclosing function's variables transparently, matching Go's semantics for
the native target where defer maps to `defer func() { ... }()`.

## Parallel tasks

`parallel { task { ... } task { ... } ... }` runs every task block
concurrently and joins them all before continuing past the closing brace.
It is the language's structured-concurrency primitive — fire and join, no
async colours, no scheduler API.

```tartalo
func main(): void {
  parallel {
    task { echo("a") }
    task { echo("b") }
    task { echo("c") }
  }
  echo("done")    // always prints after every task has finished
}
```

Rules (enforced by the checker):

- The body of `parallel { ... }` may only contain `task { ... }` statements.
- A task body may **not** assign to variables declared outside the task.
  Reading them is fine.
- A task body may **not** contain `return`, `defer`, or another `parallel`
  block. (No nested concurrency in v0.)
- `task { ... }` outside of a surrounding `parallel { ... }` is a syntax
  error.

These restrictions exist so the sh and native backends produce the same
observable behaviour. The sh backend lowers each task to a backgrounded
subshell (`( ... ) &`) joined by `wait`; the native backend lowers each
task to a goroutine driven by a `sync.WaitGroup`. Subshells cannot
propagate variable mutations back to the parent, and goroutines that wrote
to shared locals would race — so the language forbids both.

### Codegen sketch

- **sh**: each `task { body }` emits `( body ) &`; after the last task the
  block ends with `wait` (no args, blocks until all backgrounded children
  exit).
- **native**: the whole block is wrapped in a Go `{ ... }` scope holding a
  fresh `sync.WaitGroup`; each task becomes `go func() { defer wg.Done(); body }()`,
  and the block ends with `wg.Wait()`.

## Result and the `?` operator

There is no built-in `Result` type — the user defines their own sum that
matches the Result shape:

```tartalo
type IntResult = Ok{value: number} | Err{error: string}
```

A "Result-shaped sum" is any sum with exactly two variants named `Ok` and
`Err`, where `Ok` has a single field named `value` and `Err` has a single
field named `error`. The `?` postfix operator on a Result-shaped value
short-circuits to the enclosing function's matching `Err`:

```tartalo
func parseInt(s: string): IntResult {
  if s == "bad" { return Err{error: "bad input"} }
  return Ok{value: 1}
}

func double(s: string): IntResult {
  let n: number = parseInt(s)?   // on Err, double returns Err{error: ...}
  return Ok{value: n + n}
}
```

Constraints enforced at type-check time:

- The operand must be Result-shaped.
- The enclosing function's return type must be Result-shaped with the same
  `Err` payload type.
- Defer blocks registered before `?` still run on the early-return path.

## Pipelines

The `|>` operator threads its left-hand side as the first argument of a
function call:

```tartalo
let n: number = 5 |> double()       // double(5)
echo(str(7 |> add(3)))              // add(7, 3)
echo("HELLO" |> lower)              // lower("HELLO") — bare name OK
echo(str(3 |> double() |> plus(1))) // plus(double(3), 1)
```

Pipelines desugar to nested calls at parse time, so they cost nothing at
runtime and play with every other feature (records, sums, optionals,
`?`, defer) by default.

## String interpolation

```tartalo
let who: string = "world"
echo("Hello, ${who}!")
```

Compiles to `echo "Hello, ${who}!"` with proper quoting.

## Commands

Backticks run a command and substitute its stdout (trailing newline stripped):

```tartalo
let files: string = `ls -1`
```

A command in statement position runs for side effects:

```tartalo
`mkdir -p build`
```

## Builtins (v0)

### Core

- `echo(s: string): void` — print line to stdout
- `eprint(s: string): void` — print line to stderr
- `str(n: number | float | bool): string` — convert a scalar to its string representation
- `num(s: string): number` — string → int (errors at runtime if not numeric)
- `len(s | T[]): number` — UTF-8 codepoint (rune) count for strings; element
  count for arrays. For raw byte length use `byteLen`.
- `env(name: string): string?` — read env var (`null` if unset, empty string if set to `""`)
- `exit(code: number): void` — exit with code

### Strings

- `upper(s: string): string`
- `lower(s: string): string`
- `trim(s: string): string` — strips leading/trailing whitespace (space, tab, CR, LF)
- `replace(s, from, to: string): string` — literal substring replace, no regex
- `contains(s, sub: string): bool`
- `startsWith(s, prefix: string): bool`
- `endsWith(s, suffix: string): bool`
- `slice(s: string, start, end: number): string` — half-open `[start, end)`,
  0-based; `start` and `end` are UTF-8 codepoint indices, so the result is
  always a valid UTF-8 string.
- `byteLen(s: string): number` — raw byte length (POSIX `wc -c` semantics).
- `byteSlice(s: string, start, end: number): string` — half-open byte-index
  slice. May return an invalid UTF-8 substring when cutting through a
  multi-byte sequence; prefer `slice` unless you specifically need bytes
  (e.g., for binary protocols).
- `split(s, sep: string): string[]`
- `join(arr: string[], sep: string): string`

### Float

- `floatOf(n: number): float` — widen an integer to a float
- `intOf(f: float): number` — truncate a float toward zero
- `parseFloat(s: string): float?` — parse a float, or `null` if not numeric
- `formatFloat(f: float, decimals: number): string` — format with the given number of decimal places
- `floor(f: float): number` — largest integer ≤ f
- `ceil(f: float): number` — smallest integer ≥ f
- `round(f: float): number` — round to nearest integer (half away from zero)

### Numeric vectors (numpy-lite)

Element-wise and reduction ops on `float[]` (and `arange` on `number[]`).
All reductions return `0` for an empty vector. Binary ops use the shorter
operand's length when sizes don't match.

- `vSum(xs: float[]): float` — sum
- `vMean(xs: float[]): float` — arithmetic mean
- `vMin(xs: float[]): float` — minimum
- `vMax(xs: float[]): float` — maximum
- `vVar(xs: float[]): float` — population variance
- `vStd(xs: float[]): float` — population standard deviation
- `vAdd(a, b: float[]): float[]` — element-wise sum
- `vSub(a, b: float[]): float[]` — element-wise difference
- `vMul(a, b: float[]): float[]` — element-wise product
- `vScale(xs: float[], k: float): float[]` — scalar multiply
- `vDot(a, b: float[]): float` — dot product
- `linspace(start, end: float, n: number): float[]` — `n` evenly-spaced
  samples in `[start, end]` (inclusive)
- `arange(start, end, step: number): number[]` — half-open integer range
- `cumsum(xs: float[]): float[]` — running totals

### Pandas-lite (data ops on record arrays)

A typed array of records `T[]` is the dataframe. CSV I/O uses
`encoding/csv` on the native target; the sh target stubs `readCsv`/`writeCsv`
out with a runtime error directing users to `--target=native`.

- `count(arr: T[], pred: func(T): bool): number` — count elements matching
  the predicate (no allocation, unlike `len(filter(...))`)
- `unique(arr: T[]): T[]` — order-preserving deduplication. T must be a
  primitive (string, number, float, bool); deduplicating record arrays is
  not yet supported.
- `readCsv(path: string): T[]` — parse a CSV file into an array of records.
  T must be inferred from a typed context, e.g.
  `let xs: Person[] = readCsv("p.csv")`. Each record field maps to a
  column by header name; primitive (and optional-primitive) fields only.
  Native target only.
- `writeCsv(rows: T[], path: string): void` — write records as CSV with a
  header row. Same field-type constraints as `readCsv`. Native target only.

### File I/O

- `readFile(path: string): string` — read file contents; aborts the script on error
- `writeFile(path: string, content: string): void` — write `content` (overwriting); aborts on error
- `appendFile(path: string, content: string): void` — append `content`; aborts on error
- `removeFile(path: string): void` — remove a file; idempotent (no error if absent)
- `mkdir(path: string): void` — create a directory and any missing parents; idempotent
- `listDir(path: string): string[]` — list entries (basenames, sorted, including dotfiles)
- `exists(path: string): bool`
- `isFile(path: string): bool`
- `isDir(path: string): bool`
- `stat(path: string): FileInfo` — one-shot metadata bundle. Falls back to BSD `stat -f` when GNU `stat -c` isn't available, so the same script runs on Linux and macOS. For a missing path, `exists` is false and the numeric fields are 0.
- `readStdin(): string` — read all of stdin

The "abort on error" behaviour is intentional for v0; if you need to inspect
the failure, drop down to `exec(...)` which gives you `code`, `stdout`, and
`stderr`. (When optional types arrive, these will pick up `?` variants.)

### Path manipulation (no I/O)

- `pathJoin(a: string, b: string): string` — joins two path segments; if `b`
  is absolute it wins (Node-style)
- `basename(path: string): string`
- `dirname(path: string): string`
- `extname(path: string): string` — extension *including* the leading dot,
  or `""` when the basename has no dot
- `parsePath(path: string): PathParts` — split a path into `{ dir, base, name, ext }` in one go. The `ext` rule matches `extname` (includes the leading dot, or `""` when the basename has no dot).

### Subprocesses and HTTP

- `exec(cmd: string): Process` — run a shell command, capture stdout, stderr, and exit code
- `execTimeout(cmd: string, secs: number): Process` — like `exec` but kills the command after `secs`. Aborts the script if neither `timeout` (GNU) nor `gtimeout` (macOS coreutils) is on PATH. Process.code is `124` on timeout.
- `fetch(url: string): Response` — HTTP GET (via `curl -sS -L`)

### Regex (POSIX ERE via awk)

- `regexMatch(s, pat: string): bool` — `s ~ pat`
- `regexFind(s, pat): string?` — first match, or null
- `regexFindAll(s, pat): string[]` — all non-overlapping matches
- `regexReplace(s, pat, repl: string): string` — `gsub(pat, repl, s)`. Backslashes and `&` in `repl` are escaped before substitution so the replacement is treated as literal text.

### Higher-order

- `map(arr: T[], f: func(T): U): U[]`
- `filter(arr: T[], pred: func(T): bool): T[]`
- `reduce(arr: T[], init: U, f: func(U, T): U): U`

These are typed by hand in the checker (no generics yet). The function
argument is a reference to a top-level user-declared function — pass the
function's name as a value: `map(xs, double)`. Builtins cannot be passed by
reference. Functions are values — you can store them in variables with type
`func(...): R`:

```tartalo
func square(n: number): number { return n * n }
let f: func(number): number = square
echo(str(f(7)))
```

### Generic functions

A function may declare one or more type parameters in `<...>` between its
name and parameter list. Type parameters are unbounded — they accept any
Tartalo type that's legal as an array element, function parameter, or
record field — and the operations a generic body may apply to a value of
type `T` are limited to passthrough (let / return / call), array
construction (`[t1, t2]`), array indexing (`xs[i]`), and the optional
operators (`x ?? d`, `x!`, `x == null`).

```tartalo
func id<T>(x: T): T { return x }
func first<T>(xs: T[]): T { return xs[0] }
func or<T>(x: T?, fallback: T): T { return x ?? fallback }

func main(): void {
  echo(id("hi"))
  echo(str(first([10, 20, 30])))
  let s: string? = "hello"
  echo(or(s, "fallback"))
}
```

Type arguments are **inferred** from the call site — Tartalo has no
syntax for explicit type-argument lists. Every type parameter must be
mentioned by at least one *parameter* type so the checker can deduce a
binding from the supplied arguments; a type parameter that only appears
in the return type (`func nope<T>(): T`) is rejected.

The checker enforces the restrictions above by treating each `<T>` as an
opaque type during body checking. Operations that would require knowing
T's shape (arithmetic, ordering, field access, function calls, etc.)
fail with a regular type-mismatch diagnostic.

### Codegen sketch

Both backends use **monomorphization**: each unique combination of type
arguments produces one specialised copy of the function. Specialised
names use the suffix `__gen__<arg1>__<arg2>...` so they're easy to spot
in the generated output. Functions that the program never calls aren't
emitted, which doubles as dead-code elimination for the generic
declarations themselves.

Generic calls inside another generic function compose via a
fixed-point pass: the outer instantiation's substitution is applied to
the inner call's recorded type arguments before the inner specialisation
is selected.

### v0 limitations

- Generics on `tool` and `agent` declarations are rejected — those
  carry compile-time schemas that must remain monomorphic.
- No explicit type-argument syntax (`f<int>(x)`); inference only.
- No bounded constraints — all type parameters are universally
  quantified. Operations that need a particular shape (arithmetic,
  comparison, callability) are not allowed.
- No generic record / sum types.

### Anonymous functions (closures)

Function literals can appear in any expression position:

```tartalo
let dbl: func(number): number = func(x: number): number { return x + x }
let xs: number[] = [1, 2, 3, 4]
let squares: number[] = map(xs, func(x: number): number { return x * x })
```

Lambdas may capture variables from the enclosing scope:

```tartalo
func main(): void {
  let n: number = 10
  let xs: number[] = [1, 2, 3]
  let added: number[] = map(xs, func(x: number): number { return x + n })
}
```

**Target compatibility note**: on the native target, captures work just
like Go's closures — including for closures that escape their defining
function (`func makeAdder(n) { return func(x) { return x + n } }`). On the
sh target, captures work via dynamic scoping, which is fine for the common
case where the lambda runs *while* the defining frame is still live (e.g.,
inside `map`). A closure that escapes its defining frame on the sh target
will read uninitialised free variables at runtime; if you need escaping
closures, use `--target=native`.

### Process / time

- `args(): string[]` — positional args passed to the script (stable from any call site)
- `now(): number` — current Unix timestamp in seconds (`date +%s`)
- `sleep(seconds: number): void` — POSIX `sleep` (no fractional seconds guarantee)
- `formatTime(secs: number, fmt: string): string` — format a Unix time using `date`. Tries BSD `-r` then GNU `-d @`, so the same script runs on macOS and Linux.

### JSON

The JSON helpers shell out to **`jq`** at runtime, so any host running a
script that uses them must have `jq` on `PATH`.

- `jsonGet(json: string, path: string): string?` — extract a value at a jq path. Both "missing path" and "path → null" surface as `null` on the tartalo side; use `jsonHas` to disambiguate.
- `jsonHas(json: string, path: string): bool` — true iff the path exists *and* its value is non-null.
- `jsonArray(json: string, path: string): string[]` — array elements as a string[]; each element is jq's stringified form (raw for scalars, JSON for objects/arrays).
- `jsonEscape(s: string): string` — encode a string as a JSON string literal *with* surrounding quotes. Convenient when hand-building a request body.

### Test framework

`test "name" { ... }` declares a test. Tests can live in the same `.tt` file
as the implementation they exercise — Rust-style — and they're stripped
from non-test builds, so production binaries stay clean.

Run the tests for a single file with `tartalo test foo.tt`. Pass a
directory and `tartalo test ./` walks it, runs every `.tt` file containing
at least one `test` declaration, and aggregates per-file results. Hidden
directories and `node_modules` are skipped.

#### Assertions

These builtins may only be called inside a `test "..." { ... }` block.

- `assertEq(a, b): void` — abort with a diagnostic if `a != b` (polymorphic over scalar primitives)
- `assertNe(a, b): void` — abort with a diagnostic if `a == b`
- `check(cond: bool): void` — abort with a diagnostic if `cond` is false
- `fail(msg: string): void` — unconditionally abort the test with `msg`
- `skip(msg: string): void` — mark the test as skipped and exit cleanly

#### Mocks

Mocks intercept calls to the side-effecting builtins so tests can run
hermetically. Each mock setter is test-only (the checker rejects calls
outside a `test` body) and per-test: each test starts with a clean slate.

| Setter | Strict? | Behaviour |
|---|---|---|
| `mockExec(pat, resp: Process)` | yes | when `pat` (regex) matches the cmd, return `resp`; with mocks set, an unmatched call fails the test |
| `mockExecCalls(): string[]` | — | cmds the SUT passed to `exec`/`execTimeout` during this test |
| `mockFetch(pat, resp: Response)` | yes | regex over the URL, same shape as `mockExec` |
| `mockFetchCalls(): string[]` | — | URLs the SUT passed to `fetch` during this test |
| `mockReadFile(pat, content: string)` | yes | regex over the path; matched call returns `content` |
| `mockReadFileCalls(): string[]` | — | paths the SUT passed to `readFile` during this test |
| `mockEnv(name, value: string?)` | no | replaces the value for `name` only; `null` simulates "unset"; other names fall through |
| `mockNow(secs: number)` | no | freezes the clock so `now()` returns `secs` |
| `mockArgs(xs: string[])` | no | replaces the result of `args()` for this test |
| `mockReadStdin(s: string)` | no | replaces the result of `readStdin()` for this test |

Strict-mode builtins (exec / fetch / readFile) fail the test on an
unmatched real call once any rule has been registered for that builtin —
preventing accidental network or filesystem hits.

The native backend implements every mock listed above. The sh backend
ships with the four name/value-style mocks (env, now, args, readStdin);
exec, fetch, and readFile mocks abort at runtime with a clear "use
--target=native" message when reached.

## Agent platform

Tartalo doubles as an agent platform. The wedge: agents distributed as a
single self-contained `.sh` (or native binary) — no `pip install`, no
`node_modules`, no Docker. The shell is already the universal tool-calling
protocol; tartalo gives it types, schemas, capability annotations, and
replayable traces.

### Tool & agent declarations

```tartalo
tool searchFiles(pattern: string): string {
  desc("recursively grep the working tree for a pattern")
  return exec("grep -rIn " + pattern + " .").stdout
}

agent assistant(question: string) uses (searchFiles): string !ai {
  desc("answer a question, possibly using tools")
  budget(5)
  let prompt = "Tools: " + agentTools() + "\nQ: " + question
  return llm(prompt)
}
```

`tool` and `agent` parse identically to `func` — same parameter list, same
return type, same body — but each is tagged in the AST so the codegen knows
to register them in the schema table. The first lines of a tool/agent body
may be `desc("...")` and `budget(N)` calls; these are pulled off as
metadata, not executed.

An optional `uses (toolA, toolB, ...)` clause sits between the parameter
list and the return-type colon. It declares which tools the agent may
invoke; `agentTools()` resolves to a JSON array of just those tools'
schemas, suitable for prompt-injecting only the tools that are in scope.
The checker rejects unknown names so a typo can't ship to runtime.

`budget(N)` is enforced at runtime: each `llm()` call inside the agent body
decrements an invocation-local counter, and the program aborts with a
clear error on the (N+1)th call. The counter resets every time the agent
is invoked.

### Effect annotations

Postfix `!effect` markers on the return type record what a function may do.
Standard tags: `!ai !net !fs:read !fs:write !exec !io`. Effects are
currently advisory — they appear in `toolSchemas()` and document intent.
Future work will enforce them via a compile-time `--caps=` capability set.

### Agent-platform builtins

| Builtin | Type | Effect | Notes |
|---|---|---|---|
| `llm(prompt: string): string` | `(string) → string` | `!ai` | Dispatches on `$TARTALO_LLM_PROVIDER`. `kimi` (or `moonshot`) calls Moonshot's OpenAI-compatible chat/completions API using `$KIMI_API_KEY` (overridable via `$TARTALO_KIMI_BASE_URL` / `$TARTALO_KIMI_MODEL`). Set `$TARTALO_LLM_STREAM=1` to switch the kimi path to SSE: deltas are mirrored to stderr as they arrive, and the assembled content is still what the call returns. Anything else pipes the prompt to `$TARTALO_LLM_CMD` (default `claude -p`). The shell target needs `curl` for the kimi path; the native target uses Go's `net/http` directly. In test mode every call must be matched by `mockLlm` or the test fails. |
| `approval(prompt: string): bool` | `(string) → bool` | `!io` | Prints prompt on stderr, reads y/n from `/dev/tty` (falls back to stdin). Returns true for y/Y/yes, else false. |
| `trace(label: string, value: string): void` | `(string,string) → void` | `!fs:write` | Appends one NDJSON record `{ts, label, value}` to `$TARTALO_TRACE` if set; no-op otherwise. |
| `spawnAgent(name: string, input: string): string` | `(string,string) → string` | inherits | Calls a declared agent by name through a compile-time-built `case` dispatcher. No eval, no string-to-function lookup. Aborts with a clear error on unknown names. Restricted to `(string) → string` agents. |
| `callTool(name: string, input: string): string` | `(string,string) → string` | inherits | Same shape as `spawnAgent` but for tools. Useful when an LLM response names the tool to invoke. Restricted to `(string) → string` tools. |
| `agentTools(): string` | `() → string` | none | Returns a JSON array of the schemas of the tools declared in the surrounding agent's `uses (...)` clause; returns `"[]"` outside an agent context. Resolved at compile time per call site. |
| `toolSchemas(): string` | `() → string` | none | Returns a static JSON string with one entry per tool/agent: `{name, kind, params:[{name,type}], returns, description?, effects?, budget?, tools?}`. Built at compile time, stored as a sh constant / Go `const` — every call is O(1). |
| `mockLlm(pat, resp: string): void` | `(string,string) → void` | test-only | Registers a regex → response rule for `llm()` during a test. |
| `mockLlmCalls(): string[]` | `() → string[]` | test-only | Prompts seen this run, in order. |

### Tracing & replay

Setting `TARTALO_TRACE=path` at runtime makes every `trace(...)` call emit
one NDJSON record to that file. Combined with the existing mock family,
this gives you reproducible agent runs: capture once, replay deterministically
under `--target=native` with `mockExec` / `mockFetch` / `mockLlm` rules
filling in for the recorded calls.

### Capabilities (future)

The annotation half of capabilities ships in v1. Enforcement (`--caps=net`
refusing to compile a program whose effects exceed the cap set) is on
deck — the call-graph traversal lives in the checker; what's missing is
the propagation pass and the CLI hook.

### Predeclared types

```tartalo
type Response = {
  status: number,    // HTTP status code; 0 on network failure
  ok: bool,          // true iff 200 ≤ status < 300
  body: string,      // response body
  headers: string,   // raw response headers, one per line
}

type Process = {
  code: number,      // exit code
  ok: bool,          // true iff code == 0
  stdout: string,    // captured stdout
  stderr: string,    // captured stderr
}

type FileInfo = {
  exists: bool,      // false if the path doesn't exist
  isFile: bool,
  isDir: bool,
  size: number,      // bytes; 0 if missing
  mtime: number,     // Unix seconds; 0 if missing
  mode: string,      // octal permission bits, e.g. "644"; "" if missing
}

type PathParts = {
  dir: string,       // dirname(path)
  base: string,      // basename(path) — final component, with extension
  name: string,      // basename minus the last `.ext` (same rule as extname)
  ext: string,       // extension including leading dot, or ""
}
```

`fetch` shells out to `curl -sS -L`; connection/DNS failures produce
`status: 0, ok: false`. `exec` runs the command via `sh -c`, captures
streams to temp files, and uses `|| code=$?` so the host script's `set -e`
doesn't propagate non-zero exits.

## Operators

Arithmetic on `number`: `+ - * / %`
String: `+` (concat), `==`, `!=`, and ordering `< <= > >=` (compiled via `awk`'s
lexicographic comparison so it works on locales/POSIX builds).
Comparison on `number`: `== != < <= > >=`
Boolean: `&& || !`
Indexing on arrays: `arr[i]` (0-based)
Grouping: `( ... )`
Postfix cast: `expr as Type` (record-to-record only — see "Type casts")
Optional unwrap: `expr ?? default`, `expr!`, `expr?` (Result short-circuit)
Record spread: `Foo{...source, field: value}` (see "Record spread")

## Compilation model

```
                                          ┌─→  sh emitter   →  source.sh
source.tt  →  lexer  →  parser  →  checker┤
                                          └─→  Go emitter   →  go build  →  binary
```

Two backends share the same frontend (lexer, parser, checker). The default
`--target=sh` produces `#!/bin/sh` with `set -eu`; `--target=native` emits
a self-contained Go program and compiles it with the host's `go build`,
producing a statically-linked native executable.

`bool` in the sh backend follows POSIX exit-code convention (0 = true) so
boolean tests can pass through to native shell when useful. The native
backend uses Go's native `bool`; only `str(true)` / `str(false)` deliberately
produce `"1"` / `"0"` to keep cross-backend output identical.

### Native target

```
tartalo build foo.tt --target=native -o foo
tartalo build foo.tt --target=native --goos=linux --goarch=arm64 -o foo
tartalo run   --target=native foo.tt -- args...
tartalo test  --target=native foo.tt
```

Requirements: a `go` toolchain on `PATH` at compile time. The resulting
binary has no runtime dependency on Go (or on a shell). Cross-compilation
uses Go's `GOOS` / `GOARCH` machinery: every (os, arch) pair Go supports
works, including `darwin/arm64`, `linux/amd64`, `linux/arm64`, and
`windows/amd64`.

Type mapping:

| Tartalo | Go |
|---|---|
| `string` / `number` / `float` / `bool` | `string` / `int64` / `float64` / `bool` |
| `T[]` | `[]T` |
| `T?` | `*T` (nil = none) |
| record `type Foo = {...}` | `type Tt_Foo struct {...}` |
| `func(a: T): R` | `func(a T) R` |

Both backends produce byte-identical stdout for the supplied test fixtures
and example programs. Documented divergences:

- **Regex.** The sh backend runs POSIX ERE through awk; the native backend
  uses Go's `regexp` (RE2). For the patterns Tartalo programs actually use
  (character classes, `+`, `?`, `|`, groups) the two agree, but RE2 has no
  backreferences, so a pattern that uses `\1` is rejected at runtime by
  the native backend with a clear panic.
- **JSON.** The sh backend shells out to `jq`; the native backend implements
  the subset of jq paths Tartalo programs use (`.`, `.field`, `.field.nested`,
  `.field[N]`) without depending on `jq` being on `PATH`.
- **Backtick command literals.** Both backends route through a shell —
  `/bin/sh -c` on POSIX, `cmd /c` on Windows. Pipelines that depend on
  POSIX-only utilities will not survive on a Windows target.
