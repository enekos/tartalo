# TT-TYP001: Type mismatch

A value of type X was used where type Y was expected. The diagnostic reports both types in "expected X, got Y" form.

## Common shapes

- Assigning a `string` to a `number` variable, or vice versa.
- Calling a function with arguments whose types differ from its parameters.
- Returning a value whose type differs from the function's declared return type.
- Using a `string` where a `bool` is required (in `if`, `while`, `&&`, `||`).

## Repair

- Convert explicitly: `str(n)` to turn a number into a string, `num(s)` to turn a string into a number, `asInt`/`asFloat`/`asBool` at trust boundaries.
- Restructure the call so the argument and parameter types match.

```tartalo
let count: number = 3
echo("count=" + str(count))   // explicit conversion
```

Tartalo never coerces implicitly between primitive types. `"foo" + 1` is a type error by design.
