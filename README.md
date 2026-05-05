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
tartalo build <file.tt> [-o <out.sh>]   # compile to sh
tartalo run   <file.tt>                 # compile to a temp file and exec /bin/sh
tartalo check <file.tt>...              # type-check only, no codegen
```

The compiler resolves `import` statements transitively from the entry file,
so passing the entry file is enough — every reachable module is bundled into
the output.
