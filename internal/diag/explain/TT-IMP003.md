# TT-IMP003: No exported name

The imported module exists, but the requested name is not visible from outside it. Only declarations prefixed with `export` are exported.

## Repair

- Add `export` in front of the declaration in the source module.

```tartalo
// lib/math.tt
export type Pair = { a: number, b: number }
export func sumPair(p: Pair): number { return p.a + p.b }

// helper not exported — private to this file
func helper(): string { return "shh" }
```

- Or use the unexported name only inside the declaring module.
