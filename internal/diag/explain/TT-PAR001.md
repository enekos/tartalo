# TT-PAR001: Syntax error

The parser hit a token it did not expect. The diagnostic span points at the offending token.

## Common shapes

- Missing `}` to close a function or block.
- Missing `,` between record fields, function parameters, or array elements.
- A keyword used as an identifier (`let`, `func`, `match`, `task`, …).
- A statement-only construct (`task { ... }`, `parallel { ... }`) used in expression position.

## Repair

- Re-read the line carefully; the parser reports the *first* token it could not place, which is usually one token past the actual omission.
- For nested braces, use your editor's bracket-matching to find the unbalanced pair.
- For keyword collisions, rename the identifier.

```tartalo
func greet(name: string): string {
  return "Hello, " + name
}
```
