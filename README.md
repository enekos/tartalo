# Tartalo

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

Pre-alpha. The compiler is being built bottom-up: lexer → parser → type checker → sh emitter.

## Building

```
go build -o tartalo ./cmd/tartalo
```

## CLI

```
tartalo build <file.tt> [-o <out.sh>] [--trace]   # compile to sh
tartalo run   [--trace] <file.tt>                 # compile to a temp file and exec /bin/sh
tartalo check <file.tt>...                        # type-check only, no codegen
tartalo test  <file.tt>                           # run all `test "..."` declarations in the entry module
tartalo fmt   [-l|-d|-w] <file.tt>...             # format source (default: rewrite in place)
tartalo bench <file.tt> [-n N]                    # time compile phases (and runtime) over N iterations
tartalo lsp                                       # speak Language Server Protocol over stdio
```

The compiler resolves `import` statements transitively from the entry file,
so passing the entry file is enough — every reachable module is bundled into
the output.

`--trace` (build/run) emits per-statement source-line tracking and an EXIT
trap that prints the last known `.tt` location on a non-zero exit. Off by
default; opt in when debugging a script that aborts under `set -e`.

Stdlib modules ship inside the binary and are imported via the `tartalo:`
scheme, e.g. `import { padLeft, repeat } from "tartalo:strings/extra"`.
