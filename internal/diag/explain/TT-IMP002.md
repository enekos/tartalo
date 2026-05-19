# TT-IMP002: Import cycle

Two or more modules import each other (directly or transitively). Tartalo bundles every reachable file into a single `.sh` artifact and uses topological order to emit them, so cycles are not allowed.

## Repair

- Extract the shared declarations into a third module that both can import.
- Replace one of the cross-imports with a function parameter (pass the value in instead of importing the type).

```tartalo
// before: a.tt → b.tt → a.tt
// after:  a.tt → shared.tt ← b.tt
```
