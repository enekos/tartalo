# Deferred Optimization Ideas for Tartalo Codegen

## High-Impact (complex but valuable)

- **Ident cache in compileIdent**: Cache exprValue results keyed by *ast.Ident pointer to avoid repeated name mangling and type switching for hot variables.
- **Constant folding in codegen**: Fold constant arithmetic (e.g., `1 + 2` -> `3`) at codegen time to reduce generated expression complexity.
- **Dead store elimination**: Remove assignments to variables that are never read before being overwritten again.
- **Loop unrolling for small constant ranges**: Unroll `for i in 0..4` into 4 straight-line statements.
- **Inline small functions**: Functions with <N lines could be inlined to eliminate call overhead.

## Medium-Impact (moderate complexity)

- **Fast-path simple literals in emitAssign/emitReturn**: Same pattern as emitVarDecl fast-path — skip compileExpr for int/bool/string literals.
- **Optimize array literal iteration**: When `for x in [1,2,3]` is used, inline the array body directly into the heredoc without a temp.
- **Skip __ret temp for nested expressions**: When a function call is used inside a larger expression (e.g., `fib(10) + 1`), still avoid the temp by using `__ret` directly in the expression.
- **Bare $var in emitAssign for simple identifiers**: Same as emitReturn optimization — `x=$y` instead of `x=$((y))` for simple numeric variables.

## Low-Impact (easy wins)

- **More fmt.Sprintf -> string concat**: Replace remaining hot-path fmt.Sprintf calls with direct concatenation.
- **Pre-allocate strings.Builder capacity**: Estimate output size and pre-allocate to reduce reallocs during emission.
- **Optimize writeLines empty check**: Early return when slice is empty to avoid loop overhead.
