# TT-NAM001: Undeclared identifier

The name being referenced is not declared in any scope visible from this expression.

## Common causes

- Typo in the identifier (`useer` vs `user`).
- The declaration is in a different module and was not imported.
- The declaration uses `let`/`const` later in the same scope; Tartalo is single-pass within a function and requires names to be visible at use time.
- The name is private to another module (see TT-IMP003).

## Repair

- Declare it before use: `let user = ...`.
- Import it from the defining module: `import { user } from "./users.tt"`.
- Check capitalization — identifiers are case-sensitive.

```tartalo
let message: string = "hello"
echo(message)            // ok: declared above
```
