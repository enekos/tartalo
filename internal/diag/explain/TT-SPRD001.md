# TT-SPRD001: Record spread error

The `...source` spread in a record literal must:

- Appear as the *first* entry in the literal.
- Refer to a value of exactly the same record type as the literal.

Cross-type spreads (copying fields from a different record type that happens to share field names) are rejected. Use a record-to-record `as` cast instead.

## Repair

```tartalo
type Person = { name: string, age: number }

let alice: Person = Person{ name: "Alice", age: 30 }
let older: Person = Person{ ...alice, age: 31 }
```

The spread copies every field from `source`; any named field that follows overrides the corresponding spread value.
