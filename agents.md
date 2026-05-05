# Tartalo — Agent Guide

## What is this?

Tartalo is a small, statically-typed scripting language that compiles to POSIX sh. It has a Go-based compiler (lexer → parser → type checker → sh emitter) and a Vue-based landing page.

## Quick commands

```bash
# Build the compiler
go build -o tartalo ./cmd/tartalo

# Run the Go test suite
make test

# Full CI gate (vet, fmt-check, test, examples)
make check
make examples

# Format all .tt examples and .go files
make fmt

# Build the landing page
cd landing && bun install && bun run build
```

## Project layout

```
cmd/tartalo/        CLI entrypoint (build, run, check, test, fmt, bench, lsp)
internal/
  ast/              AST node definitions
  token/            Token types
  lexer/            Tokenizer
  parser/           Recursive-descent parser
  checker/          Type checker + builtin registry
  codegen/          sh emitter (the bulk of the compiler)
  loader/           Import resolution / module graph
  format/           Source formatter
  lsp/              Language server protocol implementation
  verify/           shellcheck integration
  stdlib/           Embedded stdlib .tt files
  types/            Type system definitions
landing/            Vue 3 + Vite landing page
examples/           Example .tt scripts
```

## Key conventions

- **Go**: Standard Go style. `go fmt` is enforced in CI.
- **Tartalo (.tt)**: The compiler is the source of truth for formatting. `make fmt` runs `./tartalo fmt` on all examples.
- **Tests**: Go tests are table-driven. Codegen tests often compile `.tt` snippets to sh and run them via `/bin/sh` to verify output.
- **Builtin registry**: All builtins are registered in `internal/checker/checker.go` via `mk()` helpers.
- **Code generation targets**: Pure POSIX `sh` with `set -eu`. No bashisms. Arrays are newline-joined strings.
- **Error handling**: Compiler errors are accumulated and returned as a slice, not panicked. The CLI wraps them in `compileErrors`.

## Adding a builtin

1. Register the signature in `internal/checker/checker.go` (find the appropriate section).
2. Add the implementation in `internal/codegen/codegen.go` (look for `emitBuiltinCall`).
3. Add tests in `internal/codegen/*_test.go`.
4. Document in `SPEC.md` and update the landing page (`landing/src/pages/Reference.vue`).

## Adding a CLI command

1. Add the case in `cmd/tartalo/main.go` → `run()`.
2. Implement a `cmdXxx()` function.
3. Update `README.md` and `printUsage()`.

## CI

GitHub Actions runs `make check` and `make examples` on every push/PR. The landing page build is also verified in CI.
