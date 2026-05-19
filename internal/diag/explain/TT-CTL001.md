# TT-CTL001: Control-flow error

A control-flow statement appears where it cannot be lowered safely.

## Rules

- `break` and `continue` are only valid inside `for` or `while`. They cannot cross a `task { ... }` boundary, a function boundary, or appear at file scope.
- `return` inside `task { ... }` is rejected — each task runs as its own subshell/goroutine, so a "return from the enclosing function" cannot be lowered.
- A void function must not `return <value>`; a non-void function must `return` a value of the declared return type.

## Repair

- Hoist the control-flow out of the task: send a sentinel on a channel, then have the main flow break/return.
- Match the function's declared return type.

```tartalo
func count(): number {
  let n: number = 0
  for i in 0..10 {
    if i == 5 { break }
    n = n + 1
  }
  return n            // always returns number
}
```
