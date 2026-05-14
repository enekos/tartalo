# Tartalo

[![CI](https://github.com/enekos/tartalo/actions/workflows/ci.yml/badge.svg)](https://github.com/enekos/tartalo/actions/workflows/ci.yml)

A small, statically-typed scripting language with two backends: it compiles to
**POSIX sh** for portable scripts, or to a **self-contained native binary** for
shipping cross-platform tools. Think of it as a thin TypeScript-ish layer over
shell scripting: catch type errors at compile time, choose the runtime that
fits.

```tartalo
// hello.tt
func main(): void {
  let who: string = "world"
  echo("Hello, ${who}!")
}
```

```
$ tartalo build hello.tt -o hello.sh                     # POSIX sh
$ sh hello.sh
Hello, world!

$ tartalo build hello.tt --target=native -o hello        # native binary
$ ./hello
Hello, world!
```

See [SPEC.md](SPEC.md) for the language reference.

## Status

Pre-alpha. The compiler pipeline is complete — lexer → parser → type checker → sh emitter — and the CLI supports build, run, test, check, format, benchmark, and LSP modes.

Generic functions are supported with inference-only call sites and
monomorphisation in both backends; see [`SPEC.md`](SPEC.md#generic-functions)
for the syntax and v0 limits.

Maps (`map<K, V>`) ship with a small builtin set — `mapNew` / `mapGet` /
`mapSet` / `mapHas` / `mapDelete` / `mapKeys` / `mapValues` / `mapLen` —
where keys are primitive (`string` / `number` / `bool`) and values are
non-optional primitives. See [`SPEC.md`](SPEC.md#maps) for the rules and
v0 limits.

Concurrency comes in two flavours: `parallel { task { ... } }` for
structured fork-join, and `spawn fn(args)` + typed `chan[T]` mailboxes
for long-lived workers that communicate by message passing. Both lower
to backgrounded subshells + `wait` on the sh backend and to goroutines
+ `sync.WaitGroup` on the native backend. See
[`SPEC.md`](SPEC.md#spawn-and-channels) for the full model and
restrictions.

## Install

Prebuilt binaries for darwin / linux / windows × amd64 / arm64 are attached
to each tagged [release](https://github.com/enekos/tartalo/releases).
Download the archive matching your platform, extract, and put `tartalo` on
your `PATH`. Verify the download with the bundled `SHA256SUMS` file:

```
sha256sum -c SHA256SUMS
```

## Building from source

```
go build -o tartalo ./cmd/tartalo
```

## Testing

Run the Go test suite:

```
make test
```

Run the full CI gate (vet, fmt-check, test, examples):

```
make check
make examples
```

## CLI

```
tartalo build <file.tt> [-o <out>] [--target=sh|native] [--goos=<os>] [--goarch=<arch>] [--no-verify] [--trace]
tartalo run   [--target=sh|native] [--no-verify] [--no-trace] <file.tt> [-- args...]
tartalo check <file.tt>...                                       # type-check only, no codegen
tartalo test  [--target=sh|native] [--no-verify] <file-or-dir>   # run every `test "..."` block, recursively for a dir
tartalo fmt   [-l|-d|-w] <file.tt>...                            # format source (default: rewrite in place)
tartalo bench <file.tt> [-n N] [--no-run] [--no-verify]          # time compile phases (and runtime) over N iterations
tartalo lsp                                                      # Language Server: diagnostics, hover, goto-def, symbols, refs, rename, completion
```

The compiler resolves `import` statements transitively from the entry file,
so passing the entry file is enough — every reachable module is bundled into
the output.

`--target` selects the backend. `sh` (the default) emits a `.sh` script
verified by `shellcheck`. `native` emits a Go program and compiles it with
the host's `go` toolchain into a statically-linked binary; `--goos` and
`--goarch` cross-compile to any platform Go supports. The native target
requires `go` on `PATH` at build time but produces binaries with no runtime
toolchain dependency.

Source-mapped runtime errors are on by default for `tartalo run`: when a
script aborts under `set -e`, the EXIT trap prints a Rust-style frame with
the absolute `.tt` path, the failing source line, and a caret column marker.
Pass `--no-trace` to suppress it (e.g., to capture the bare stderr the
script would emit on its own). For `tartalo build`, source-mapping is opt-in
via `--trace` since shipped scripts usually want the smaller, cleaner
output. Sh target only.

By default, sh-target build/run/test pipe the emitted sh through `shellcheck`
before writing or executing it. Pass `--no-verify` (or set
`TARTALO_NO_VERIFY=1`) to skip the safety check. The native target skips
shellcheck — it's an sh-specific guardrail.

If a `.env` file exists alongside the entry `.tt` file, `tartalo run`,
`tartalo test`, and `tartalo bench` load its `KEY=VALUE`
pairs into the child process's environment before executing. Existing
environment variables take precedence (they aren't overridden by `.env`).
Quoted values, `export ` prefixes, and `#` comments are supported. `tartalo
build` does not load `.env` — it's a runtime concern, not a compile-time
one.

Stdlib modules ship inside the binary and are imported via the `tartalo:`
scheme:

```tartalo
import { padLeft, repeat }       from "tartalo:strings/extra"
import { clamp, gcd, pow }       from "tartalo:numbers/extra"
import { Result, ok, err }       from "tartalo:result/result"
import { getOr, getIntOr }       from "tartalo:json/json"
import { isoNow, since }         from "tartalo:time/time"
import { join3, withExt, stem }  from "tartalo:path/path"
import { readLines, listFiles }  from "tartalo:fs/fs"
import { matches, findOr }       from "tartalo:regex/regex"
```

## Testing

Tests live next to the implementation, Rust-style. A `test "..." { ... }`
block can sit anywhere in any `.tt` file; the compiler strips them from
non-test builds.

```tartalo
func double(n: number): number { return n * 2 }

test "double doubles" { assertEq(double(21), 42) }
```

`tartalo test foo.tt` runs every test in that file. `tartalo test ./` walks
the directory tree, runs every `.tt` file with at least one `test`
declaration, and aggregates per-file results.

Mock builtins (test-only) make hermetic tests easy. They cover every
side-effecting boundary the language exposes:

- **processes**: `mockExec` / `mockExecCalls`, `mockSleep` /
  `mockSleepCalls`
- **filesystem reads**: `mockReadFile`, `mockListDir`, `mockExists`,
  `mockIsFile`, `mockIsDir`, `mockStat`, `mockReadStdin` (+ matching
  `Calls()` recorders)
- **filesystem writes**: `mockWriteFile`, `mockAppendFile`,
  `mockRemoveFile`, `mockMkdir` — block real disk I/O and record what
  the SUT tried to write (`mockWriteFileCalls()` / `mockWriteFileContents()`,
  etc.)
- **network**: `mockFetch` / `mockFetchCalls` — one rule covers
  `fetch`, `fetchTimeout`, `fetchHeaders`, `postJson`, `postForm`,
  `request`
- **ambient inputs**: `mockEnv`, `mockNow`, `mockArgs`

Strict mode is on by default for the recording mocks — once a rule is
registered, an unmatched real call fails the test, so no test can
accidentally write to `/etc` or hit the network. See SPEC.md for the
full table. The native target supports every mock; the sh backend ships
with the four ambient mocks (env / now / args / readStdin) and aborts
with a clear `requires --target=native` message for the rest.
