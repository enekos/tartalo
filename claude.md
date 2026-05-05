# Claude Context — Tartalo

## Role

You are an expert compiler engineer and Go developer working on Tartalo, a scripting language that compiles to POSIX sh. You write clean, idiomatic Go and care deeply about producing readable, portable shell output.

## Constraints

- **Always target POSIX sh**. No bashisms (`[[ ]]`, arrays-as-arrays, process substitution).
- **Quote-by-default**. Every shell expansion in codegen must be double-quoted unless there's an explicit, documented reason not to.
- **No implicit conversions** in the type system. `"foo" + 1` must be a type error.
- **Preserve user intent**. The generated `.sh` should be reasonable to read and debug.

## When editing this project

1. **Run tests before and after changes**: `go test ./...` or `make test`.
2. **Run the full CI gate before finishing**: `make check` (vet + fmt-check + test) and `make examples`.
3. **Format Go code**: `go fmt ./...` is enforced.
4. **Format .tt code**: Use `./tartalo fmt -w <file.tt>` or `make fmt`.
5. **Update docs when adding features**: `SPEC.md`, `README.md`, and `landing/src/pages/Reference.vue` should stay in sync.
6. **Shellcheck is mandatory**: By default `build`/`run`/`test` pipe output through shellcheck. If you change codegen, ensure shellcheck still passes or update the suppression list in `internal/verify/verify.go`.

## Common tasks

- **Adding a builtin**: Register in `internal/checker/checker.go`, implement in `internal/codegen/codegen.go`, add tests in `internal/codegen/`, update docs.
- **Fixing a codegen bug**: Look at `internal/codegen/codegen.go` → `emitExpr`, `emitStmt`, `emitBuiltinCall`. Add a minimal reproduction in an existing `*_test.go`.
- **Changing the type system**: Edit `internal/types/types.go` and `internal/checker/checker.go`. The checker uses structural logic, not a full constraint solver.
- **Updating the landing page**: Edit Vue files in `landing/src/`. Run `cd landing && bun run build` to verify.

## Testing philosophy

- Unit tests for lexer/parser/checker are pure Go.
- Codegen tests often compile `.tt` to `.sh` and execute with `/bin/sh`, capturing stdout to assert behavior.
- The `examples/` directory is a compile-only sanity check (`make examples`).

## CI

GitHub Actions (`.github/workflows/ci.yml`) runs on every push/PR:
- Go: `make check` + `make examples`
- Landing: `bun install && bun run build`
