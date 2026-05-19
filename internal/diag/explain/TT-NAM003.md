# TT-NAM003: Redeclaration

A name is being redeclared in a scope that already has the same name. This is a stricter variant of TT-NAM002 for cases like redeclaring a predeclared type (`number`, `string`, …) or shadowing a function across module scope.

## Repair

- Rename the new declaration.
- If you intended to override behavior for a different scope, move the declaration into that scope so the outer name remains untouched.

```tartalo
// rejected — `number` is a predeclared type
type number = { value: string }

// repair: rename
type IntStr = { value: string }
```
