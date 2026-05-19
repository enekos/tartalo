# TT-CALL001: Call-site error

The call doesn't match the callee's signature.

## Common shapes

- Wrong number of arguments (too many or too few).
- An argument's type doesn't match the parameter's declared type.
- Calling a non-function value: `let x = 5; x()`.

## Repair

- Read the callee's signature (hover in your editor or jump to definition).
- Convert arguments to the expected type explicitly with `str`, `num`, `floatOf`, etc.
- Pass `null` only when the parameter type is `T?`.

```tartalo
func greet(name: string, times: number): void { ... }
greet("alice", 3)   // ok
```
