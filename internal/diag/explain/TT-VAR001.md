# TT-VAR001: Sum / variant error

The construction or destructuring of a tagged-union value disagrees with the declared sum type.

## Common shapes

- An unknown variant in a match arm.
- A duplicate variant name in one sum type.
- A variant payload field that isn't declared.
- A unit variant used with payload syntax (`Empty{}`), or a data variant used as bare (`Circle`).

## Repair

```tartalo
type Shape =
    Circle{r: number}
  | Rectangle{w: number, h: number}
  | Empty

let s: Shape = Circle{r: 4}
let e: Shape = Empty

match s {
  Circle{r}        => echo("c " + str(r))
  Rectangle{w, h}  => echo("r " + str(w * h))
  Empty            => echo("e")
}
```

`match` on a sum must be exhaustive or include a `_` wildcard arm.
