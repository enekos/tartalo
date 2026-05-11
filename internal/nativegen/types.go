package nativegen

import (
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/goprint"
	"github.com/enekos/tartalo/internal/types"
)

// Pre-allocated strings for common array/optional types to avoid allocation
// from string concatenation in goType.
var (
	_goTypeInt64Arr  = "[]int64"
	_goTypeStringArr = "[]string"
	_goTypeFloatArr  = "[]float64"
	_goTypeBoolArr   = "[]bool"
	_goTypeInt64Opt  = "*int64"
	_goTypeStringOpt = "*string"
	_goTypeFloatOpt  = "*float64"
	_goTypeBoolOpt   = "*bool"
)

// goType returns the Go type expression for a Tartalo type. The receiver is a
// no-op for primitives and arrays; records resolve to their generated
// `Tt_<name>` struct; optionals to `*<elem>`.
func (g *Generator) goType(t types.Type) string {
	// Apply the active monomorphisation substitution before pattern-matching
	// on shape, so a body expression typed `T` resolves to its concrete type
	// inside the current generic instantiation.
	if g.currentSubst != nil {
		t = types.Substitute(t, g.currentSubst)
	}
	switch t := t.(type) {
	case *types.Primitive:
		switch t {
		case types.String:
			return "string"
		case types.Number:
			return "int64"
		case types.Float:
			return "float64"
		case types.Bool:
			return "bool"
		case types.Void:
			return ""
		case types.Null:
			// `null` literal type — never the declared type of a binding.
			// Falling through to interface{} keeps the emitter conservative;
			// in practice every consumer special-cases NullLit before this.
			return "interface{}"
		}
	case *types.Array:
		switch t.Elem {
		case types.Number:
			return _goTypeInt64Arr
		case types.String:
			return _goTypeStringArr
		case types.Float:
			return _goTypeFloatArr
		case types.Bool:
			return _goTypeBoolArr
		}
		return "[]" + g.goType(t.Elem)
	case *types.Optional:
		switch t.Elem {
		case types.Number:
			return _goTypeInt64Opt
		case types.String:
			return _goTypeStringOpt
		case types.Float:
			return _goTypeFloatOpt
		case types.Bool:
			return _goTypeBoolOpt
		}
		return "*" + g.goType(t.Elem)
	case *types.Map:
		return "map[" + g.goType(t.Key) + "]" + g.goType(t.Value)
	case *types.Chan:
		return "chan " + g.goType(t.Elem)
	case *types.Record:
		return goTypeName(t.Name)
	case *types.Sum:
		return goTypeName(t.Name)
	case *types.Func:
		out := "func("
		for i, p := range t.Params {
			if i > 0 {
				out += ", "
			}
			out += g.goType(p)
		}
		out += ")"
		if t.Result != types.Void {
			out += " " + g.goType(t.Result)
		}
		return out
	case *types.TypeVar:
		// Reached only if a generic body expression's type leaked outside its
		// monomorphisation context. Returning interface{} keeps the file
		// well-formed even if the call site was missed; a nil currentSubst
		// usually indicates a bug elsewhere in the emitter.
		return "interface{}"
	}
	return "interface{}"
}

// typeFromAnn resolves a (parser-produced) AST type annotation into a
// types.Type. Used in spots where the declared shape (e.g. `let x: T?`) wins
// over the inferred RHS shape. Returns nil if the annotation references an
// unknown name — the checker would already have flagged that case.
func (g *Generator) typeFromAnn(ann ast.TypeExpr) types.Type {
	switch ann := ann.(type) {
	case *ast.TypeName:
		// Generic-scope override: if we're emitting a monomorphisation, a
		// type-parameter name resolves to its concrete substitution.
		if g.currentTypeNameSubst != nil {
			if t, ok := g.currentTypeNameSubst[ann.Name]; ok {
				return t
			}
		}
		if t := types.Lookup(ann.Name); t != nil {
			return t
		}
		// Record reference: look it up in the type info via any decl that
		// produced this name. Fall back to a placeholder Record which will
		// have its name correctly mangled on emission.
		for _, sym := range g.info.Decls {
			if sym.Name == ann.Name {
				if rec, ok := sym.Type.(*types.Record); ok {
					return rec
				}
			}
		}
		return &types.Record{Name: ann.Name}
	case *ast.ArrayType:
		return &types.Array{Elem: g.typeFromAnn(ann.Elem)}
	case *ast.OptionalType:
		return &types.Optional{Elem: g.typeFromAnn(ann.Elem)}
	case *ast.MapType:
		return &types.Map{Key: g.typeFromAnn(ann.Key), Value: g.typeFromAnn(ann.Value)}
	case *ast.ChanType:
		return &types.Chan{Elem: g.typeFromAnn(ann.Elem)}
	}
	return nil
}

// emitPredeclaredTypes writes the record types every Tartalo program can
// refer to via builtins (Response/Request/Process/FileInfo/PathParts).
// Their field shapes are defined in the checker's builtinTypes() and must
// stay in lockstep. Output is column-aligned via goprint.Struct so it
// reads cleanly when the user inspects the generated source.
func (g *Generator) emitPredeclaredTypes() {
	buf := goprint.NewBuf(1024)
	goprint.Struct(buf, "Tt_Response", []goprint.StructField{
		{Name: "F_status", Type: "int64"},
		{Name: "F_ok", Type: "bool"},
		{Name: "F_body", Type: "string"},
		{Name: "F_headers", Type: "string"},
	})
	goprint.Struct(buf, "Tt_Request", []goprint.StructField{
		{Name: "F_url", Type: "string"},
		{Name: "F_method", Type: "string"},
		{Name: "F_headers", Type: "[]string"},
		{Name: "F_body", Type: "string"},
		{Name: "F_timeout", Type: "int64"},
		{Name: "F_followRedirects", Type: "bool"},
		{Name: "F_insecure", Type: "bool"},
		{Name: "F_user", Type: "string"},
		{Name: "F_password", Type: "string"},
	})
	goprint.Struct(buf, "Tt_Process", []goprint.StructField{
		{Name: "F_code", Type: "int64"},
		{Name: "F_ok", Type: "bool"},
		{Name: "F_stdout", Type: "string"},
		{Name: "F_stderr", Type: "string"},
	})
	goprint.Struct(buf, "Tt_FileInfo", []goprint.StructField{
		{Name: "F_exists", Type: "bool"},
		{Name: "F_isFile", Type: "bool"},
		{Name: "F_isDir", Type: "bool"},
		{Name: "F_size", Type: "int64"},
		{Name: "F_mtime", Type: "int64"},
		{Name: "F_mode", Type: "string"},
	})
	goprint.Struct(buf, "Tt_PathParts", []goprint.StructField{
		{Name: "F_dir", Type: "string"},
		{Name: "F_base", Type: "string"},
		{Name: "F_name", Type: "string"},
		{Name: "F_ext", Type: "string"},
	})
	g.out.WriteString(buf.String())
}

// emitTypeDecl writes the Go declaration for a Tartalo type. Records become
// plain structs; sums become a struct with a tag field plus per-variant
// payload slots, mirroring the sh backend's encoding. Both forms route
// through goprint.Struct so field names and types line up in columns.
func (g *Generator) emitTypeDecl(td *ast.TypeDecl) {
	switch spec := td.Spec.(type) {
	case *ast.RecordType:
		fields := make([]goprint.StructField, 0, len(spec.Fields))
		for _, f := range spec.Fields {
			fields = append(fields, goprint.StructField{
				Name: "F_" + f.Name,
				Type: g.goType(g.typeFromAnn(f.TypeAnn)),
			})
		}
		g.writeStructDecl("Tt_"+td.Name, fields)
	case *ast.SumType:
		g.emitSumTypeDecl(td.Name, spec)
	}
}

// emitSumTypeDecl renders a sum type as a Go struct with a Tag string and a
// fan of fields per variant. Field names are prefixed with the variant name
// to avoid cross-variant collisions; only the tag's matching slots carry
// meaningful values at runtime.
func (g *Generator) emitSumTypeDecl(name string, spec *ast.SumType) {
	fields := []goprint.StructField{{Name: "Tag", Type: "string"}}
	for _, v := range spec.Variants {
		for _, f := range v.Fields {
			fields = append(fields, goprint.StructField{
				Name: "F_" + v.Name + "_" + f.Name,
				Type: g.goType(g.typeFromAnn(f.TypeAnn)),
			})
		}
	}
	g.writeStructDecl("Tt_"+name, fields)
}

// writeStructDecl pipes the struct text built by goprint.Struct into g.out,
// re-indenting each emitted line at the generator's current depth so the
// declaration lands inside any surrounding block context (currently always
// top-level, but preserved for forward-compat with nested decls).
func (g *Generator) writeStructDecl(name string, fields []goprint.StructField) {
	buf := goprint.NewBuf(64 + 32*len(fields))
	goprint.Struct(buf, name, fields)
	if g.indent == 0 {
		g.out.WriteString(buf.String())
		return
	}
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		g.writeLine(line)
	}
	g.out.WriteByte('\n')
}
