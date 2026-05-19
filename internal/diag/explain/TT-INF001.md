# TT-INF001: Type inference failed

The checker could not figure out the type of the expression and there is no annotation to fall back on.

## Common shapes

- An empty array literal with no annotation: `let xs = []`.
- A bare `null` with no surrounding type context: `let x = null`.
- `mapNew()` / `chan()` without an annotation on the binding.
- A generic call whose arguments don't pin down every type parameter.

## Repair

Add an annotation that pins the type:

```tartalo
let xs: string[] = []
let x:  string?  = null
let m:  map<string, number> = mapNew()
let ch: chan[string] = chan()
```

Type inference is local to the initializer; if the initializer doesn't carry enough type information on its own, the annotation does the work.
