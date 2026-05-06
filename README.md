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
tartalo run   [--target=sh|native] [--no-verify] [--trace] <file.tt> [-- args...]
tartalo check <file.tt>...                                       # type-check only, no codegen
tartalo test  [--target=sh|native] [--no-verify] <file-or-dir>   # run every `test "..."` block, recursively for a dir
tartalo fmt   [-l|-d|-w] <file.tt>...                            # format source (default: rewrite in place)
tartalo bench <file.tt> [-n N] [--no-run] [--no-verify]          # time compile phases (and runtime) over N iterations
tartalo lsp                                                      # speak Language Server Protocol over stdio
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

`--trace` (build/run) emits per-statement source-line tracking and an EXIT
trap that prints the last known `.tt` location on a non-zero exit. Off by
default; opt in when debugging a script that aborts under `set -e`. Sh
target only.

By default, sh-target build/run/test pipe the emitted sh through `shellcheck`
before writing or executing it. Pass `--no-verify` (or set
`TARTALO_NO_VERIFY=1`) to skip the safety check. The native target skips
shellcheck — it's an sh-specific guardrail.

Stdlib modules ship inside the binary and are imported via the `tartalo:`
scheme, e.g. `import { padLeft, repeat } from "tartalo:strings/extra"`.

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

Mock builtins (test-only) make hermetic tests easy: `mockExec`, `mockFetch`,
`mockReadFile`, `mockEnv`, `mockNow`, `mockArgs`, `mockReadStdin`. Strict
mode is on by default for `exec`/`fetch`/`readFile` — once a rule is
registered, an unmatched real call fails the test. See SPEC.md for the full
table. Native target supports the full mock set; the sh backend ships with
the four name/value-style mocks (env / now / args / readStdin).
