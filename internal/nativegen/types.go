package nativegen

import (
	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/types"
)

// goType returns the Go type expression for a Tartalo type. The receiver is a
// no-op for primitives and arrays; records resolve to their generated
// `Tt_<name>` struct; optionals to `*<elem>`.
func (g *Generator) goType(t types.Type) string {
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
		return "[]" + g.goType(t.Elem)
	case *types.Optional:
		return "*" + g.goType(t.Elem)
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
	}
	return nil
}

// emitPredeclaredTypes writes the four record types every Tartalo program
// can refer to via builtins (Response/Process/FileInfo/PathParts). Their
// field shapes are defined in the checker's builtinTypes() and must stay
// in lockstep.
func (g *Generator) emitPredeclaredTypes() {
	g.writeLine("type Tt_Response struct {")
	g.indent++
	g.writeLine("F_status int64")
	g.writeLine("F_ok bool")
	g.writeLine("F_body string")
	g.writeLine("F_headers string")
	g.indent--
	g.writeLine("}")
	g.writeLine("")
	g.writeLine("type Tt_Process struct {")
	g.indent++
	g.writeLine("F_code int64")
	g.writeLine("F_ok bool")
	g.writeLine("F_stdout string")
	g.writeLine("F_stderr string")
	g.indent--
	g.writeLine("}")
	g.writeLine("")
	g.writeLine("type Tt_FileInfo struct {")
	g.indent++
	g.writeLine("F_exists bool")
	g.writeLine("F_isFile bool")
	g.writeLine("F_isDir bool")
	g.writeLine("F_size int64")
	g.writeLine("F_mtime int64")
	g.writeLine("F_mode string")
	g.indent--
	g.writeLine("}")
	g.writeLine("")
	g.writeLine("type Tt_PathParts struct {")
	g.indent++
	g.writeLine("F_dir string")
	g.writeLine("F_base string")
	g.writeLine("F_name string")
	g.writeLine("F_ext string")
	g.indent--
	g.writeLine("}")
	g.writeLine("")
}

// emitTypeDecl writes the Go declaration for a Tartalo type. Records become
// plain structs; sums become a struct with a tag field plus per-variant
// payload slots, mirroring the sh backend's encoding.
func (g *Generator) emitTypeDecl(td *ast.TypeDecl) {
	switch spec := td.Spec.(type) {
	case *ast.RecordType:
		g.writeLine("type " + goTypeName(td.Name) + " struct {")
		g.indent++
		for _, f := range spec.Fields {
			g.writeLine(goFieldName(f.Name) + " " + g.goType(g.typeFromAnn(f.TypeAnn)))
		}
		g.indent--
		g.writeLine("}")
		g.writeLine("")
	case *ast.SumType:
		g.emitSumTypeDecl(td.Name, spec)
	}
}

// emitSumTypeDecl renders a sum type as a Go struct with a Tag string and a
// fan of fields per variant. Field names are prefixed with the variant name
// to avoid cross-variant collisions; only the tag's matching slots carry
// meaningful values at runtime.
func (g *Generator) emitSumTypeDecl(name string, spec *ast.SumType) {
	g.writeLine("type " + goTypeName(name) + " struct {")
	g.indent++
	g.writeLine("Tag string")
	for _, v := range spec.Variants {
		for _, f := range v.Fields {
			g.writeLine("F_" + v.Name + "_" + f.Name + " " + g.goType(g.typeFromAnn(f.TypeAnn)))
		}
	}
	g.indent--
	g.writeLine("}")
	g.writeLine("")
}
