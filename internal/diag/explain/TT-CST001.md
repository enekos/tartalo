# TT-CST001: Invalid `as` cast

`expr as Type` is restricted in v0:

- Allowed: record-to-record cast where the target's field set is a subset of the source's, with each shared field's type assignable from source to target.
- Not allowed: primitive-to-primitive, record-to-primitive, sum-related, or array-related casts.

For primitive conversions, use the existing builtins:

- `str(n)` — scalar → string
- `num(s)` — string → number
- `floatOf(n)` / `intOf(f)` — number ↔ float
- `asInt(s)` / `asFloat(s)` / `asBool(s)` — typed assertions at trust boundaries

## Repair

```tartalo
type RawUser   = { name: string, age: number, email: string }
type ShortUser = { name: string, age: number }

let raw:   RawUser   = RawUser{ name: "Alice", age: 30, email: "a@x" }
let short: ShortUser = raw as ShortUser   // field set is a subset
```
