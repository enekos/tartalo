// Package stdlib serves embedded tartalo source files as importable modules.
//
// Stdlib modules are referenced from user code via the "tartalo:" import
// scheme, e.g. `import { repeat } from "tartalo:strings/extra"`. The loader
// recognizes the prefix and reads the source from this package's embed.FS
// instead of the on-disk module-resolver.
package stdlib

import (
	"embed"
	"io/fs"
	"strings"
)

// Prefix is the URI scheme that marks a stdlib import path.
const Prefix = "tartalo:"

//go:embed all:lib
var stdlibFS embed.FS

// Resolve looks up the stdlib source for an import path of the form
// "tartalo:strings/extra". The .tt suffix is appended automatically when
// missing. The returned canonical path is the input with the suffix
// normalized; ok is false when no such module is shipped.
func Resolve(importPath string) (canonical string, src []byte, ok bool) {
	if !strings.HasPrefix(importPath, Prefix) {
		return "", nil, false
	}
	rel := strings.TrimPrefix(importPath, Prefix)
	if rel == "" || strings.Contains(rel, "..") {
		return "", nil, false
	}
	candidates := []string{rel + ".tt"}
	if strings.HasSuffix(rel, ".tt") {
		candidates = []string{rel}
	}
	for _, c := range candidates {
		if data, err := fs.ReadFile(stdlibFS, "lib/"+c); err == nil {
			return Prefix + c, data, true
		}
	}
	return "", nil, false
}

// IsStdlibPath reports whether the given absolute/canonical loader path
// refers to a stdlib module rather than a real file on disk.
func IsStdlibPath(p string) bool {
	return strings.HasPrefix(p, Prefix)
}
