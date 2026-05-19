# TT-IMP001: Import error

Tartalo could not resolve the import. The most common cases:

- the path does not exist on disk
- the path is missing a `.tt` suffix
- the path is not a relative path (`./` or `../`) and Tartalo has no module registry
- the import statement appears below another declaration (imports must precede all other top-level declarations)

## Repair

```tartalo
// at the top of the file, before any func/type/let
import { Pair, sumPair } from "./lib/math.tt"
```

Module paths are interpreted relative to the importing file's directory. Tartalo has no package manager and no remote module resolution — every dependency is a local `.tt` file.
