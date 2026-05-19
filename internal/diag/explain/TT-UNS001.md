# TT-UNS001: Intentional v0 limitation

The construct is syntactically meaningful but Tartalo's v0 surface deliberately excludes it. Common cases:

- arrays of optionals (`(T?)[]`)
- arrays of maps (`map<K,V>[]`)
- optional arrays (`T[]?`)
- optional maps (`map<K,V>?`)
- nested records inside variant payloads
- equality between records (compare individual fields)

These are not bugs; they're scope decisions tied to the sh-target's flat encoding. The lifting from v0 to v1 is tracked in `SPEC.md` and `agents.md`.

## Repair

- Lift one level of nesting out by hand: store the optional value as a sentinel inside an array, or restructure the data into a sum type.
- For record equality, write a small comparison function that compares each field individually.

If a particular limitation blocks your program, prefer `--target=native` (which has Go-level types and lifts most of these) over working around them in the sh target.
