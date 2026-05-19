# TT-RNG001: Range / iter error

The iterable in a `for x in iter { ... }` loop is not a supported form.

## Legal iterables in v0

- Numeric range `start..end` (half-open; `start` and `end` must be `number`).
- Array `T[]`.
- `string` (iterates line-by-line, split on `\n`).
- Command literal `` `cmd` `` (iterates the command's stdout lines).

Steps other than 1 are not supported; build them by hand with `while`.

## Repair

```tartalo
for i in 0..10 { echo(str(i)) }
for s in ["a", "b"] { echo(s) }
for line in `ls -1` { echo(line) }
```

If you have a mixed-kind iterable, lift one form out into a temporary of the right type.
