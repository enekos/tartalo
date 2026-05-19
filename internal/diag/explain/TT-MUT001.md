# TT-MUT001: Mutability error

The assignment target is not writable.

## Common shapes

- Assigning to a `const`.
- Assigning to a function name (functions are not reassignable values).
- Assigning to an outer-scope variable from inside a `task { ... }` block. Tasks may *read* outer values but not write them — the sh backend lowers tasks to subshells, which cannot propagate writes; the native backend lowers tasks to goroutines, which would race.

## Repair

- Use `let` rather than `const` if the value is intentionally rebindable.
- Communicate task results via a `chan[T]` instead of shared variables.

```tartalo
let counter: number = 0
counter = counter + 1   // ok: `let`-bound
```
