# TT-MAP001: Map error

A `map<K, V>` operation violates Tartalo's map rules.

## Constraints

- Keys must be `string`, `number`, or `bool`.
- Values must be a primitive (`string`, `number`, `float`, `bool`) on both backends; the native backend additionally allows record values.
- Maps cannot be record fields, array elements, or other map values.
- `mapNew()` requires a typed context (`let m: map<K, V> = mapNew()`).

## Repair

Build with `mapNew` + `mapSet` and reassign back into the same variable:

```tartalo
let m0: map<string, number> = mapNew()
let m1: map<string, number> = mapSet(m0, "alice", 30)
echo(str(mapGet(m1, "alice") ?? -1))
```

`mapSet`/`mapDelete` return a *new* map; they do not mutate in place.
