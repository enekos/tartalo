# TT-CON001: Concurrency error

`parallel { ... }`, `task { ... }`, `spawn`, and `chan[T]` together form Tartalo's concurrency model. The diagnostic flags a use that cannot be lowered to *both* backends safely.

## Common shapes

- `task { ... }` outside a surrounding `parallel { ... }`.
- A task body that assigns to a variable declared outside the task (sh subshells cannot propagate writes; goroutines would race).
- `return`, `defer`, or a nested `parallel` block inside a task body.
- A `chan[T]` element type that is not a scalar primitive (`string`, `number`, `float`, `bool`).
- `spawn` targeting a generic function, a builtin, or a function that returns non-`void`.
- `send` on a closed channel (runtime), or `closeChan` followed by another `send`.

## Repair

- Use a `chan[T]` to ship task results back: `send(ch, value)` inside the task, `recv(ch)` outside.
- For "fire and forget" workers, use `spawn` + `waitAll()` at program exit.
- Replace generic spawn targets with concrete wrappers per type.

```tartalo
let ch: chan[string] = chan()
parallel {
  task { send(ch, "a") }
  task { send(ch, "b") }
}
closeChan(ch)
```
