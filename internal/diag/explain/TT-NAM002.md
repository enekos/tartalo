# TT-NAM002: Duplicate name

Two declarations in the same scope share a name. The diagnostic span points at the duplicate; the original is usually mentioned in a related note.

## Common shapes

- Two `let`/`const` declarations with the same name in one function.
- Two function parameters with the same name.
- Two record fields with the same name in one type.
- Two variants with the same name in one sum type.

## Repair

- Rename one of the conflicting declarations.
- If you intended to update a value, drop the `let` keyword and assign directly: `x = ...` rather than `let x = ...`.

```tartalo
let count: number = 1
count = count + 1            // update, no new declaration
```
