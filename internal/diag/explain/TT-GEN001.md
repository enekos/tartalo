# TT-GEN001: Generic function error

A generic call or declaration violates Tartalo's narrow generics rules.

## Constraints

- Type arguments are *inferred* — Tartalo has no `f<int>(x)` syntax.
- Every type parameter must appear in at least one parameter type so it can be inferred from the supplied arguments.
- Inside a generic body, a value of type `T` may only be passed through, returned, stored in arrays, indexed, or used with optional operators (`??`, `!`, `== null`). Arithmetic, ordering, field access, and calls on `T` are rejected.
- No generic record or sum types in v0.
- Spawned function targets cannot be generic.

## Repair

```tartalo
func id<T>(x: T): T { return x }
func first<T>(xs: T[]): T { return xs[0] }
```

If you need ordering or arithmetic, write a non-generic helper for each concrete type, or pass the operation in as a function value.
