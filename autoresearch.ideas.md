# Deferred Optimization Ideas for Tartalo Codegen

## Completed (kept)

- **Predeclared types as constant string** — single WriteString instead of 4 writeLine calls
- **sort.Strings in writeImportsTo** — avoid fmt.Fprintf per import
- **Fast-path 0-arg calls in compileCall** — avoid strings.Builder for `fn()`
- **Fast-path 0/1 param functions in emitFunc** — direct WriteString instead of Builder loop
- **Skip coerce in emitReturn when types match** — pointer equality check avoids types.Equal call
- **Remove dead goTypeCache field** — clean up unused map allocation
- **int64Lit lookup table (0-19)** — avoid strconv.FormatInt for common literals
- **Skip coerce in compileArrayLit for matching element types** — pointer equality fast-path
- **Hoist pointer equality check to top of coerce** — avoids types.Equal call for matching primitives
- **Unroll writeLine indent loop for 0-3** — avoid loop overhead for common indent levels
- **Fast-path 1-2 imports without parentheses** — avoid sort.Strings and multi-line formatting
- **Stack buffer for 1-arg calls in compileCall** — [64]byte avoids strings.Builder allocation
- **Avoid args slice in compileBuiltin for 0-arg builtins** — conditional creation avoids empty slice alloc
- **Stack arrays for args and argTypes in compileBuiltin** — [4] arrays avoid slice allocations for <=4 args
- **Stack buffer for compileIdent local names** — [32]byte avoids runtime string concatenation
- **binExpr helper with stack buffer for compileBinary** — [64]byte avoids string concat for binary expressions
- **Direct writes to g.out in emitVarDecl/emitAssign/emitReturn/emitIf** — avoid intermediate string allocations for common emission patterns

## Completed (discarded)

- **Inlined writeLine in emitVarDecl** — code bloat outweighed function call savings
- **Reorder compileExpr type switch** — Go type switch is hash-based, order doesn't matter
- **Preallocate imports map capacity** — larger map increased iteration cost
- **Remove argTypes slice in compileBuiltin** — repeated map lookups cost more than one slice alloc
- **Custom itoa64 for integer literals** — strconv.FormatInt outperforms custom implementation
- **Fast-path 1-arg calls with string concat** — string concatenation slower than Builder
- **Combine hasGlobals scan with Pass 1** — extra branch in hot loop dominates
- **strings.Builder in emitFor** — Builder allocation exceeds concatenation savings
- **Skip MangledName for entry modules** — nil/IsEntry check already cheap
- **Fast-path primitive singletons in goType** — extra switch adds overhead
- **Preallocate compileArrayLit body Builder** — Grow overhead exceeds reallocation savings
- **Strip redundant parens from binary expressions** — string slice operations add overhead
- **Batch emitVarDecl declaration and discard** — string concatenation with embedded newline allocates more
- **strings.Repeat in writeLine** — strings.Repeat allocates a new string per call
- **Inline toString fast-paths** — extra branches add overhead
- **Optimize joinComma** — benchmark doesn't exercise joinComma
- **Inline MangledName fast-path** — branch overhead exceeds function call savings
- **Inline writeLine body in emitVarDecl** — code bloat outweighs savings
- **Cache local identifier compilation** — map alloc (+5 allocs) and lookup overhead dominate
- **Skip coerce in emitAssign** — extra check adds overhead without enough savings
- **Preallocate file Builder capacity** — over-allocation dwarfs reallocation savings
- **Strip parens from if conditions** — string checks add overhead
- **Conditional argTypes creation** — extra branch check adds overhead
- **Preallocate imports map** — map preallocation overhead exceeds savings
- **Inline MangledName in emitFunc sym lookup** — branch overhead exceeds savings
- **Fast-path common array/optional types in goType** — branch overhead exceeds allocation savings
- **Fast-path common return types in emitFunc** — branch overhead exceeds function call savings
- **Use WriteString instead of WriteByte for tabs** — WriteString has more overhead for short strings
- **Combine 0-param signature into single WriteString** — branch misprediction dominates
- **Stack buffer for 1-param function signature** — emitFunc not called enough
- **Stack buffer for emitFor range header** — range loop not a hotspot
- **Stack buffer for emitAssign target** — emitAssign not a major hotspot
- **Stack buffer for goType array/optional** — goType not a hotspot
- **Stack buffer for compileUnary** — unary expressions rare in benchmark
- **Stack buffer for goLocalName** — function call overhead dominates
- **Write emitFor/emitExprStmt directly to g.out** — code bloat hurts inlining of hotter functions
- **Reuse g.out buffer instead of file Builder** — g.out.Reset() adds overhead
- **Use writeIndent+WriteString for closing braces** — savings marginal
- **Inline endsWithReturn in emitFunc** — inline adds code bloat without benefit
- **Add empty string fast path to writeLine** — extra branch check adds overhead

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

## Nativegen-Specific Ideas

- **Avoid unnecessary parentheses in compileBinary**: Many binary expressions don't need parens when used in contexts with lower/equal precedence (e.g., `n - 1` as a call arg). Removing them reduces output size and string concat work.
- **Batch emitVarDecl writes**: Combine `x := rhs` and `_ = x` into a single writeLine to halve the call overhead per variable declaration.
- **Preallocate strings.Builder in compileArrayLit/compileRecordLit**: Estimate capacity from element count to avoid reallocations for larger literals.
- **Fast-path 1-arg calls with smarter concat**: The 1-arg compileCall fast-path saved allocations but regressed time; try pre-building the prefix/suffix to avoid intermediate string copies.
- **Inline writeLine for emitFunc body**: The body loop calls writeLine for every statement; inlining the indent+write+newline sequence could reduce function call overhead.
- **Cache goType for non-primitive types**: A pointer-keyed map for arrays/optionals/funcs could help programs with repeated type references (previous map attempt failed for small scripts but may help larger ones).
- **Optimize emitFor range string building**: The range loop header builds a long string via concatenation; a preallocated Builder or slice-append-then-join may be faster.
- **Remove `_ = target` when variable is definitely used**: Tartalo's checker doesn't prove liveness, but a simple scan of the remaining function body could suppress many `_ =` lines.

## Low-Impact (easy wins)

- **More fmt.Sprintf -> string concat**: Replace remaining hot-path fmt.Sprintf calls with direct concatenation.
- **Pre-allocate strings.Builder capacity**: Estimate output size and pre-allocate to reduce reallocs during emission.
- **Optimize writeLines empty check**: Early return when slice is empty to avoid loop overhead.
