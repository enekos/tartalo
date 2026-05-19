# TT-FLD001: Record field error

A record literal, access, or update references a field that does not match the record's declared shape.

## Common shapes

- Unknown field name in a literal: `Person{name: "x", emial: "y"}`.
- Missing required field in a literal.
- Duplicate field in a single literal or `type` declaration.

## Repair

```tartalo
type Person = { name: string, email: string }

let p: Person = Person{ name: "x", email: "x@example.com" }
```

Field names are case-sensitive. The literal must list every field that has no default and must not list any field the record doesn't declare.
