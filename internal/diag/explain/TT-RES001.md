# TT-RES001: Result `?` operator error

The `?` postfix operator is restricted to Result-shaped sums and to functions whose return type is also Result-shaped with the same `Err` payload.

## Rules

- The operand of `?` must have a sum type with exactly two variants named `Ok` (with a single field `value`) and `Err` (with a single field `error`).
- The enclosing function's return type must be the same Result-shaped sum (or share the same `Err` payload type).
- Defer blocks registered before `?` still fire on the early-return path.

## Repair

Use `?` only inside a function that itself returns a Result:

```tartalo
import { Result, ok, err } from "tartalo:result/result"

func parse(s: string): Result {
  if s == "" { return err("empty") }
  return ok(s)
}

func wrap(s: string): Result {
  let v: string = parse(s)?     // on Err, wrap returns the same Err
  return ok("[" + v + "]")
}
```

If the caller can't return a Result, replace `?` with an explicit `match`.
