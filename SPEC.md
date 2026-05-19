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
- Keywords: `let`, `const`, `func`, `return`, `if`, `else`, `for`, `in`, `while`, `break`, `continue`, `match`, `type`, `import`, `export`, `test`, `defer`, `parallel`, `task`, `as`, `null`, `true`, `false`, `string`, `number`, `float`, `bool`, `void`.

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
| `chan[T]` | a shell variable holding the path of a temp directory that backs the message queue (T must be a scalar primitive) |

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

## Maps

`map<K, V>` is an associative type. Keys must be a primitive `string`,
`number`, or `bool`. Values may be a primitive (`string`, `number`, `float`,
`bool`) on both backends, or a record on `--target=native` (the sh backend
rejects record-valued maps at runtime with a hint to switch targets — its
flat-string encoding can't represent composite values yet). Maps are
constructed empty and populated through the `mapSet` builtin; both `mapSet`
and `mapDelete` return a new map rather than mutating the operand, mirroring
how arrays are passed in Tartalo.

```tartalo
func main(): void {
  let m0: map<string, number> = mapNew()
  let m1: map<string, number> = mapSet(m0, "alice", 30)
  let m2: map<string, number> = mapSet(m1, "bob",   25)

  if mapHas(m2, "alice") {
    echo("alice is " + str(mapGet(m2, "alice") ?? -1))
  }

  for k in mapKeys(m2) {
    echo(k + " => " + str(mapGet(m2, k) ?? 0))
  }
  echo("size: " + str(mapLen(m2)))
}
```

`mapNew()` requires a typed context (a `let`/`const` annotation, an assign
target, or a parameter type) — there is no other way for the checker to
infer K and V.

### Map builtins

- `mapNew(): map<K, V>` — empty map; needs a typed context.
- `mapGet(m: map<K, V>, k: K): V?` — value at `k`, or `null` if missing.
- `mapSet(m: map<K, V>, k: K, v: V): map<K, V>` — copy of `m` with `k → v`.
- `mapDelete(m: map<K, V>, k: K): map<K, V>` — copy of `m` without `k`.
- `mapHas(m: map<K, V>, k: K): bool` — true iff `k` is present.
- `mapKeys(m: map<K, V>): K[]` — keys in **sorted-by-key** order.
- `mapValues(m: map<K, V>): V[]` — values in the same key order as `mapKeys`.
- `mapLen(m: map<K, V>): number` — number of entries.

### v0 limitations

- No map literal syntax. Build via `mapNew()` + `mapSet`.
- Values cannot be optional, arrays, sums, or other maps. Records are
  supported on the native backend only.
- Maps cannot be record fields, array elements, or other map values.
- Iteration order is sorted-by-key, not insertion order. Both backends agree
  on this so cross-target stdout stays byte-identical.

### Codegen sketch

- **sh**: each map is one shell variable encoded as a flat string. Pairs are
  separated by ASCII Record Separator (`\036`); within a pair, key and value
  are separated by ASCII Unit Separator (`\037`). Every operation is an
  `awk` one-liner over that string. `mapSet` and `mapDelete` build a new
  string rather than mutating in place, so reassignment back into the
  original variable (`m = mapSet(m, k, v)`) is the standard idiom.
- **native**: `map<K, V>` lowers to Go's `map[K]V`. `mapSet`/`mapDelete`
  copy the map before mutating to match the sh backend's value-style
  semantics; `mapKeys`/`mapValues` sort the keys for the same reason.

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

while count > 0 {
  echo(str(count))
  count = count - 1
}
```

`for x in iter { ... }` walks an iterable and binds each element to a fresh
local `x` scoped to the body. The element type is inferred from the
iterable; `x` shadows any outer binding for the duration of the loop. There
are four legal iterables in v0:

- **Numeric range** — `start..end` is half-open: `start` is included,
  `end` is not. `start` and `end` are `number` expressions; the loop
  variable is `number`. `for i in 0..3 { ... }` runs for `i = 0, 1, 2`.
  An empty range (`start >= end`) skips the body. Steps other than 1 are
  not supported in v0; build them by hand with `while`.
- **Array** — any `T[]` value, including arrays of records. The loop
  variable has type `T` and is bound by value, so mutations inside the
  body do not write back to the array. (Array-of-record elements bind a
  fresh record-prefix copy per iteration; see "Arrays of records" above.)
- **String** — a `string` is iterated **line by line** (split on `\n`).
  An empty string runs the body zero times. Useful for processing the
  output of a backtick command or a file read into a single string.
- **Command literal** — `` `cmd` `` runs the command, captures stdout, and
  iterates its lines. Equivalent to assigning the command result to a
  string and iterating that. `for line in `ls -1` { ... }`.

Mixing iterable kinds in one loop is not supported: each form has its own
codegen path and the checker pins the element type at compile time.

`a..b` is a half-open numeric range — only legal as the iterable in a
`for ... in` loop.

`while cond { ... }` re-runs its body as long as the boolean condition is
true. The condition is evaluated on each iteration, so any side effects in
the expression (calls, command substitutions) fire each pass.

`break` exits the innermost enclosing `for`/`while` loop; `continue` skips
to the next iteration. Both are statements and only legal inside a loop —
the checker rejects a stray `break` or `continue` at file or function
scope. They also cannot break/continue across a `task { ... }` boundary,
since each task runs as its own subshell or goroutine.

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

## Spawn and channels

`parallel { task { ... } }` is the structured-concurrency primitive — every
task joins before the block returns. For long-lived workers that outlive a
single block and communicate with each other or with the spawning function,
v1 adds `spawn` and typed channels.

```tartalo
func producer(ch: chan[string]): void {
  send(ch, "hello")
  send(ch, "world")
  closeChan(ch)
}

func main(): void {
  let ch: chan[string] = chan()
  spawn producer(ch)
  while true {
    let m: string? = recv(ch)
    if m == null { break }
    echo(m!)
  }
  waitAll()
}
```

### `spawn`

`spawn fn(args)` starts a new worker — a function call that runs
concurrently with its caller. The arguments are evaluated in the spawning
scope before the worker starts, then captured into the new context. Spawn
is a statement; it has no value.

Rules (enforced by the checker):

- The target must be a user-declared function (not a builtin).
- The target must return `void`. To return a value, send it on a channel.
- Generic functions cannot be spawned in v1.
- `spawn` is only valid inside a function body.

`waitAll()` blocks until every worker spawned by the program has returned.
v1 has no per-worker handle, so you can only join them all at once. Pair
`spawn` with `waitAll()` (or with channel-driven coordination) so the
program doesn't exit while workers are still running.

### `chan[T]`

A channel is a typed mailbox carrying values of type `T`. T is restricted
to scalar primitives — `string`, `number`, `float`, `bool` — so the sh
backend can serialise each message as a single text line. Arrays, records,
optionals, and maps are not allowed as channel element types in v1.

| Tartalo | Meaning |
|---|---|
| `let ch: chan[string] = chan()` | create a channel |
| `send(ch, v)` | send a value (blocks if the buffer is full on native) |
| `let m: string? = recv(ch)` | receive; `null` after close-and-drained |
| `closeChan(ch)` | signal "no more sends" |
| `waitAll()` | join every spawned worker |

`chan()` requires a typed context — the LHS annotation supplies `T`, just
like `mapNew()` for maps. The element type cannot be inferred from
arguments because `chan()` takes none.

Channel rules:

- A `recv` on a closed-and-drained channel returns `null`. Once you've
  seen the first `null`, the channel will only ever return `null`.
- A `send` on a closed channel aborts the script with a runtime error.
- Strings sent on a channel may not contain newlines on the sh backend
  (the queue file uses newline as the message separator). The native
  backend has no such restriction.
- `closeChan` is intended to be called once, by the producer (or by the
  consumer once all producers have completed). Closing twice is harmless
  on both backends; sending after close aborts.

### Codegen sketch

- **sh**: a channel is a freshly-`mktemp`d directory holding a queue file
  (one message per line), a `closed` marker file, and a `lock` directory
  used as a POSIX-portable mutex (`mkdir` is atomic, `rmdir` releases).
  `recv` polls the queue at 50ms (with a 1s fallback for very old shells)
  when empty until either a value lands or `closed` exists. `spawn`
  evaluates args in the parent and emits `( fn args ) &`. `waitAll()` is
  the POSIX `wait` builtin with no arguments. An EXIT trap removes every
  channel directory the script created so /tmp doesn't leak on abnormal
  exit.
- **native**: a channel is `make(chan T, 1<<14)` — buffered to mirror
  the sh backend's unbounded file-queue (programs that exceed the buffer
  block on send instead of erroring; pick a different design if you need
  unbounded). `spawn` becomes `_tt_spawn(func() { fn(args) })`, where
  `_tt_spawn` does `Add(1)+go+Done()` against a global `sync.WaitGroup`.
  `waitAll()` calls `Wait()` on that WaitGroup. `recv` uses a comma-ok
  receive (`v, ok := <-ch`) so the close signal becomes the `null`
  optional.

### Why no `select` in v1

Go-style `select { case ... }` is the obvious next primitive, but the sh
backend has no portable timed-read: `read -t` is non-POSIX and `mkfifo`
plus `kill -0` games are too fragile to be worth shipping. Until we have
a viable sh lowering, v1 sticks to one-channel-at-a-time `recv`. Workarounds:

- For "either of two channels", merge them at the producer side onto a
  single channel and tag each message.
- For timeouts, `spawn` a worker that sends a sentinel after `sleep(n)`,
  then `recv` from the merged channel.

## Result and the `?` operator

A "Result-shaped sum" is any sum with exactly two variants named `Ok` and
`Err`, where `Ok` has a single field named `value` and `Err` has a single
field named `error`. The `?` postfix operator on a Result-shaped value
short-circuits to the enclosing function's matching `Err`.

For string-error pipelines, the stdlib ships a canonical Result with
constructor and accessor helpers:

```tartalo
import { Result, ok, err, unwrapOr } from "tartalo:result/result"

func parseAge(s: string): Result {
  if s == "bad" { return err("invalid age") }
  return ok("age=" + s)
}

func formatAge(s: string): Result {
  let v: string = parseAge(s)?       // Err short-circuits formatAge
  return ok("[" + v + "]")
}

func main(): void {
  echo(unwrapOr(formatAge("42"),  "?"))
  echo(unwrapOr(formatAge("bad"), "?"))
}
```

For other (T, E) shapes (`Result<Person, MyError>`, etc.) declare your own
sum with the same Ok/Err shape and `?` will work on it directly:

```tartalo
type IntResult = Ok{value: number} | Err{error: string}

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
- `env(name: string): string?` — read env var (`null` if unset, empty string if set to `""`).
  When invoked via `tartalo run` / `test` / `bench`, a `.env` file
  in the same directory as the entry `.tt` is auto-loaded into the child
  process's environment before execution; existing env vars take precedence
  over `.env` entries. Supported syntax: `KEY=VALUE` lines, optional
  `export ` prefix, `#` comments, double-quoted values with `\n\r\t\\\"`
  escapes, single-quoted values taken literally.
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

### Boundary type assertions

These convert a string from the untyped world (shell exec output, fetch
bodies, env vars, `readFile` contents) into a concretely typed value at
the trust boundary. On a mismatch the script aborts with a runtime type
error citing the call site:

```
tartalo: type error at FILE:LINE:COL: expected EXPECTED, got VALUE
```

Use them sparingly — internal Tartalo code is already type-checked, so
the static signature does the work. They earn their keep where the
checker can't see (untyped strings crossing in from outside).

- `asInt(s: string): number` — assert decimal int (`-?[0-9]+`); aborts otherwise
- `asFloat(s: string): float` — assert float (matches `parseFloat` grammar); aborts otherwise
- `asBool(s: string): bool` — assert exactly `"true"` or `"false"`; aborts otherwise
- `asString(s: string): string` — runtime no-op; documents the boundary check

```tartalo
let p = exec("wc -l < data.csv")
let lines: number = asInt(trim(p.stdout))   // proceeds typed, or aborts here
```

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
- `fetch(url: string): Response` — HTTP GET (via `curl -sS -L`).
- `fetchTimeout(url: string, secs: number): Response` — same shape as `fetch`, but caps wall-clock with `curl --max-time`. A timeout surfaces as `status: 0, ok: false`.
- `fetchHeaders(url: string, headers: string[]): Response` — GET with caller-supplied request headers. Each entry is a raw `Name: value` line.
- `postJson(url: string, body: string): Response` — POST `body` with `Content-Type: application/json`.
- `postForm(url: string, body: string): Response` — POST `body` with `Content-Type: application/x-www-form-urlencoded`.
- `request(opts: Request): Response` — fully-typed request. The `Request` predeclared type is `{ url, method, headers: string[], body, timeout, followRedirects, insecure, user, password }`. `timeout` of `0` keeps the runtime default (30s on native; curl's default on sh). `followRedirects: false` returns the redirect response itself instead of following it. `insecure: true` skips TLS verification. A non-empty `user` triggers HTTP basic auth.
- `header(r: Response, name: string): string?` — case-insensitive lookup against `r.headers`; null when the header is absent.
- `urlEncode(s: string): string` — percent-encode per RFC 3986 (unreserved set is `A-Za-z0-9-._~`).

### Regex (POSIX ERE via awk)

- `regexMatch(s, pat: string): bool` — `s ~ pat`
- `regexFind(s, pat): string?` — first match, or null
- `regexFindAll(s, pat): string[]` — all non-overlapping matches
- `regexReplace(s, pat, repl: string): string` — `gsub(pat, repl, s)`. Backslashes and `&` in `repl` are escaped before substitution so the replacement is treated as literal text.

### Concurrency (spawn / channels)

These builtins back the `spawn` statement and `chan[T]` type. See the
[Spawn and channels](#spawn-and-channels) section for the full model.

- `chan(): chan[T]` — create a channel; `T` is supplied by the LHS
  type annotation (e.g., `let ch: chan[string] = chan()`)
- `send(ch: chan[T], v: T): void` — send a message; on the native
  backend this can block when the buffer (16384 messages) is full
- `recv(ch: chan[T]): T?` — receive; returns `null` once the channel is
  closed and drained
- `closeChan(ch: chan[T]): void` — signal "no more sends"; subsequent
  sends abort the script
- `waitAll(): void` — block until every spawned worker has returned

`T` must be a scalar primitive (`string`, `number`, `float`, `bool`)
in v1.

### Higher-order

- `map(arr: T[], f: func(T): U): U[]`
- `filter(arr: T[], pred: func(T): bool): T[]`
- `reduce(arr: T[], init: U, f: func(U, T): U): U`
- `fold(arr: T[], init: U, f: func(U, T): U): U` — alias of `reduce`
- `zip(xs: T[], ys: U[], f: func(T, U): V): V[]` — zipWith form (v1 has no
  tuple type). Stops at `min(|xs|, |ys|)`.
- `awk(xs: float[] | number[], expr: <string literal>): float[]` — escape
  hatch for vectorised numeric work. The expression is embedded verbatim
  into a single awk process and `x` is the current element; the literal
  requirement is enforced by the checker so the expression cannot be
  built at runtime. Useful for awk-only functions like `sqrt`, or for
  fusing a chain of per-element ops into one awk invocation. On the
  `--target=native` backend, `awk` is not supported (calls panic at
  runtime); use `map` with a Tartalo function instead.

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

Combined with the `|>` pipeline operator (see [Pipelines](#pipelines)) these
read top-to-bottom:

```tartalo
let totals: number[] = zip(prices, qtys, mul)
let grand: number = totals |> fold(0, add)
let roots: float[] = xs |> awk("sqrt(x)")
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

#### Expected-fail tests (`xfail`)

A test whose name starts with one of the markers below is *expected to
fail*. The runner inverts its verdict:

- the test failing at runtime is recorded as `xfail` and counted as a
  success — it does not contribute to the failed count
- the test passing unexpectedly is reported as "unexpected pass" and fails
  the suite

```tartalo
test "xfail: arithmetic is still broken" {
  assertEq(1 + 1, 3)        // expected to fail; suite stays green
}

test "expected fail: known issue #42" {
  fail("not yet fixed")
}

test "[xfail] also recognised" {
  check(false)
}
```

The marker is recognised case-insensitively, with optional leading
whitespace. Both the sh and native backends report the same xfail and
unexpected-pass counts in the summary line.

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

Four kinds of mock are bundled.

**Processes** (`exec`, `execTimeout`, `sleep`):

| Setter | Strict? | Behaviour |
|---|---|---|
| `mockExec(pat, resp: Process)` | yes | when `pat` (regex) matches the cmd, return `resp`; with mocks set, an unmatched call fails the test |
| `mockExecCalls(): string[]` | — | cmds the SUT passed to `exec`/`execTimeout` during this test |
| `mockSleep()` | — | makes every `sleep(n)` a no-op for this test; durations are still recorded |
| `mockSleepCalls(): number[]` | — | seconds the SUT passed to `sleep()` (in call order) |

**Filesystem — read side** (`readFile`, `listDir`, `exists`, `isFile`, `isDir`, `stat`, `readStdin`):

| Setter | Strict? | Behaviour |
|---|---|---|
| `mockReadFile(pat, content: string)` | yes | regex over the path; matched call returns `content` |
| `mockReadFileCalls(): string[]` | — | paths the SUT passed to `readFile` during this test |
| `mockListDir(pat, entries: string[])` | yes | matched paths return `entries`; unmatched paths fail the test once any rule is set |
| `mockListDirCalls(): string[]` | — | paths the SUT passed to `listDir` |
| `mockExists(pat, value: bool)` | yes | replaces the answer of `exists()` for matching paths |
| `mockExistsCalls(): string[]` | — | paths the SUT passed to `exists` |
| `mockIsFile(pat, value: bool)` | yes | replaces the answer of `isFile()` |
| `mockIsFileCalls(): string[]` | — | paths the SUT passed to `isFile` |
| `mockIsDir(pat, value: bool)` | yes | replaces the answer of `isDir()` |
| `mockIsDirCalls(): string[]` | — | paths the SUT passed to `isDir` |
| `mockStat(pat, info: FileInfo)` | yes | matched paths return `info`; unmatched fail once any rule is set |
| `mockStatCalls(): string[]` | — | paths the SUT passed to `stat` |
| `mockReadStdin(s: string)` | no | replaces the result of `readStdin()` for this test |

**Filesystem — write side** (`writeFile`, `appendFile`, `removeFile`, `mkdir`). All four void builtins share the same shape: registering a regex with `mockX(pat)` blocks real disk I/O on matching paths (the call is silently swallowed) and fails the test on an unmatched path. Recorders return the recorded path list; for the content-bearing builtins, a parallel array of the data the SUT tried to write is also exposed.

| Setter | Strict? | Behaviour |
|---|---|---|
| `mockWriteFile(pat)` | yes | matching `writeFile()` calls are intercepted instead of hitting disk |
| `mockWriteFileCalls(): string[]` | — | paths passed to `writeFile()` (in call order) |
| `mockWriteFileContents(): string[]` | — | parallel array of contents passed to `writeFile()` |
| `mockAppendFile(pat)` | yes | matching `appendFile()` calls are intercepted |
| `mockAppendFileCalls(): string[]` | — | paths passed to `appendFile()` |
| `mockAppendFileContents(): string[]` | — | parallel array of contents passed to `appendFile()` |
| `mockRemoveFile(pat)` | yes | matching `removeFile()` calls are intercepted |
| `mockRemoveFileCalls(): string[]` | — | paths passed to `removeFile()` |
| `mockMkdir(pat)` | yes | matching `mkdir()` calls are intercepted |
| `mockMkdirCalls(): string[]` | — | paths passed to `mkdir()` |

**Network** (`fetch`, `fetchTimeout`, `fetchHeaders`, `postJson`, `postForm`, `request`):

| Setter | Strict? | Behaviour |
|---|---|---|
| `mockFetch(pat, resp: Response)` | yes | regex over the URL — covers every fetch-family builtin |
| `mockFetchCalls(): string[]` | — | URLs the SUT passed to `fetch`/`fetchTimeout`/`fetchHeaders`/`postJson`/`postForm`/`request` |

**Ambient inputs** (`env`, `now`, `args`):

| Setter | Strict? | Behaviour |
|---|---|---|
| `mockEnv(name, value: string?)` | no | replaces the value for `name` only; `null` simulates "unset"; other names fall through |
| `mockNow(secs: number)` | no | freezes the clock so `now()` returns `secs` |
| `mockArgs(xs: string[])` | no | replaces the result of `args()` for this test |

Strict-mode builtins fail the test on an unmatched real call once *any*
rule has been registered for that builtin — preventing accidental
network, subprocess, or filesystem hits. The fall-through builtins
(`env`, `now`, `args`, `readStdin`, `sleep`) replace only what the test
asks for and leave the rest of the program's behaviour unchanged.

The native backend implements every mock listed above. The sh backend
ships with the four name/value-style mocks (env, now, args, readStdin);
every other mock aborts at runtime with a clear "use --target=native"
message when reached, so suites can still compile against both backends
and pick the right target at run time.

## Predeclared types

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
`status: 0, ok: false`. The same applies to the rest of the fetch
family (`fetchTimeout`, `fetchHeaders`, `postJson`, `postForm`,
`request`) — they share the same `__tt_request` curl shell helper on
the sh backend and the same `_tt_request_real` Go function on native,
so transport / DNS failures look identical regardless of which entry
point you call. `exec` runs the command via `sh -c`, captures
streams to temp files, and uses `|| code=$?` so the host script's `set -e`
doesn't propagate non-zero exits.

The `Request` predeclared type matches the parameter shape of `request`:

```tartalo
type Request = {
  url: string,
  method: string,            // "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"
  headers: string[],         // ["Content-Type: application/json", "Authorization: Bearer xxx"]
  body: string,              // "" for no body
  timeout: number,           // seconds; 0 leaves the runtime default
  followRedirects: bool,
  insecure: bool,             // skip TLS verification
  user: string,               // "" disables basic auth
  password: string,
}
```

Test mocks: `mockFetch(pat, resp)` matches `pat` (regex) against the
request URL. The same store is consulted by every fetch-family entry
point on the native target — that means a single `mockFetch` rule covers
`fetch`, `fetchTimeout`, `fetchHeaders`, `postJson`, `postForm`, and
`request`. `mockFetchCalls()` returns every URL the SUT hit, in order.

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

## Diagnostics

Every error reported by `tartalo check` carries a stable identifier of the
form `TT-XYZNNN`. Codes do not change between releases; the human-readable
message can. Use the code when looking something up programmatically.

```text
error[TT-NAM001]: undefined name "user"
  --> foo.tt:3:8
   |
 3 |     echo(user)
   |          ^^^^
```

`tartalo check --json` emits a structured diagnostics packet for editors,
agents, and CI:

```json
{
  "schemaVersion": 1,
  "ok": false,
  "diagnostics": [
    {
      "code": "TT-NAM001",
      "severity": "error",
      "message": "undefined name \"user\"",
      "path": "foo.tt",
      "line": 3,
      "column": 8,
      "explain": "tartalo explain TT-NAM001"
    }
  ]
}
```

Each record carries the stable `code`, the human message, the source span,
and an `explain` field whose value is the literal command an agent can run
to load the long-form explanation. The packet's `ok` field is `true` only
when no diagnostics were produced; the process exits non-zero on errors.

`tartalo explain <code>` prints a markdown explanation of a code:

```sh
tartalo explain TT-NAM001
tartalo explain --list           # every documented code
```

Code prefixes:

| Prefix     | Category                                             |
|------------|------------------------------------------------------|
| `TT-LEX`   | lexer                                                |
| `TT-PAR`   | parser                                               |
| `TT-IMP`   | imports, cycles, no-export                           |
| `TT-NAM`   | undeclared / duplicate / redeclaration               |
| `TT-TYP`   | type mismatch, bad type expression                   |
| `TT-OPT`   | optionals and `null`                                 |
| `TT-FLD`   | record fields                                        |
| `TT-VAR`   | sum / variants                                       |
| `TT-MAP`   | map operations                                       |
| `TT-CALL`  | call arity / argument mismatch                       |
| `TT-CTL`   | break / continue / return / defer placement          |
| `TT-MUT`   | assignment / mutability                              |
| `TT-RNG`   | for-range / iterables                                |
| `TT-GEN`   | generic functions / type parameters                  |
| `TT-RES`   | Result `?` operator                                  |
| `TT-CON`   | concurrency (parallel / task / spawn / chan)         |
| `TT-CST`   | `as` cast                                            |
| `TT-INF`   | type-inference failure                               |
| `TT-SPRD`  | record spread                                        |
| `TT-MCK`   | test-only API used outside a test                    |
| `TT-UNS`   | intentional v0 limitations                           |

## Host readiness

`tartalo doctor` audits PATH for the host tools the emitted scripts and the
native pipeline depend on:

```sh
tartalo doctor          # human-readable
tartalo doctor --json   # structured shape for CI
```

The checked tools are `sh`, `awk`, `jq`, `curl`, `shellcheck`,
`timeout`/`gtimeout`, and `go`. Required tools (`sh`, `awk`) cause a
non-zero exit when missing; optional tools are reported with an install
hint but do not fail the audit.

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
