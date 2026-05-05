# Tartalo

[![CI](https://github.com/enekos/tartalo/actions/workflows/ci.yml/badge.svg)](https://github.com/enekos/tartalo/actions/workflows/ci.yml)

A small, statically-typed scripting language that compiles to **POSIX sh**. Think of it as a thin TypeScript-ish layer over shell scripting: catch type errors at compile time, get readable `.sh` files at the other end.

```tartalo
// hello.tt
func main(): void {
  let who: string = "world"
  echo("Hello, ${who}!")
}
```

```
$ tartalo build hello.tt -o hello.sh
$ sh hello.sh
Hello, world!
```

See [SPEC.md](SPEC.md) for the language reference.

## Status

Pre-alpha. The compiler pipeline is complete — lexer → parser → type checker → sh emitter — and the CLI supports build, run, test, check, format, benchmark, and LSP modes.

## Building

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
tartalo build <file.tt> [-o <out.sh>] [--no-verify] [--trace]   # compile to sh
tartalo run   [--no-verify] [--trace] <file.tt> [-- args...]    # compile to a temp file and exec /bin/sh
tartalo check <file.tt>...                                      # type-check only, no codegen
tartalo test  <file.tt> [--no-verify]                           # run all `test "..."` declarations in the entry module
tartalo fmt   [-l|-d|-w] <file.tt>...                           # format source (default: rewrite in place)
tartalo bench <file.tt> [-n N] [--no-run] [--no-verify]         # time compile phases (and runtime) over N iterations
tartalo lsp                                                     # speak Language Server Protocol over stdio
```

The compiler resolves `import` statements transitively from the entry file,
so passing the entry file is enough — every reachable module is bundled into
the output.

`--trace` (build/run) emits per-statement source-line tracking and an EXIT
trap that prints the last known `.tt` location on a non-zero exit. Off by
default; opt in when debugging a script that aborts under `set -e`.

By default, build/run/test pipe the emitted sh through `shellcheck` before
writing or executing it. Pass `--no-verify` (or set `TARTALO_NO_VERIFY=1`)
to skip the safety check.

Stdlib modules ship inside the binary and are imported via the `tartalo:`
scheme, e.g. `import { padLeft, repeat } from "tartalo:strings/extra"`.
