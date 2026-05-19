# TT-OPT001: Optional / null error

The expression uses `null` or an optional value in a position that requires a non-optional value, or in a way that conflicts with Tartalo's optional rules.

## Allowed operations on `T?`

- `expr ?? default` — unwrap with a fallback. Result is `T`.
- `expr!` — forced unwrap; aborts at runtime if null.
- `expr == null` / `expr != null` — null check.

Arithmetic, ordering, indexing, and field access on `T?` are rejected. There is no flow-narrowing in v0; even inside `if x != null { ... }` `x` is still `T?`.

## Repair

```tartalo
let raw: string? = env("HOME")
let home: string = raw ?? "/tmp"   // unwrap with default
let must: string = raw!            // assert non-null (aborts on null)
```

`null` cannot stand alone; it always needs a `T?` context, either via annotation, parameter type, or the surrounding expression.
