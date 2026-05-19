# TT-TYP002: Unsupported type expression

The type position contains something Tartalo's v0 type system does not support.

## Common shapes

- An anonymous record type (`{ a: number }`) outside a `type Name = { ... }` declaration.
- A void-element array (`void[]`).
- A double-optional (`T??`).
- A type that is being referenced but never declared.

## Repair

- Declare the type first, then use its name:

```tartalo
type Point = { x: number, y: number }
func mid(a: Point, b: Point): Point { ... }
```

- For optionals, use `T?` exactly once. For "missing nested optional" cases, restructure to a sum type.
