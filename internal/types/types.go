// Package types defines the type system for tartalo.
package types

import "fmt"

// Type is the interface satisfied by every type in the system.
type Type interface {
	String() string
	typeNode()
}

// Primitive types are singletons.
type Primitive struct{ Name string }

func (p *Primitive) String() string { return p.Name }
func (p *Primitive) typeNode()      {}

var (
	String  = &Primitive{Name: "string"}
	Number  = &Primitive{Name: "number"}
	Float   = &Primitive{Name: "float"}
	Bool    = &Primitive{Name: "bool"}
	Void    = &Primitive{Name: "void"}
	Null    = &Primitive{Name: "null"} // type of the null literal; assignable to any Optional
	Invalid = &Primitive{Name: "<invalid>"}
)

// Array is a homogeneous list type.
type Array struct {
	Elem Type
}

func (a *Array) String() string { return a.Elem.String() + "[]" }
func (a *Array) typeNode()      {}

// Optional wraps a type T to represent values that may be `null`. v0 disallows
// nesting (no `T??`); the checker enforces that.
type Optional struct {
	Elem Type
}

func (o *Optional) String() string { return o.Elem.String() + "?" }
func (o *Optional) typeNode()      {}

// Unwrap returns t's element type if t is Optional, otherwise t itself.
func Unwrap(t Type) Type {
	if o, ok := t.(*Optional); ok {
		return o.Elem
	}
	return t
}

// IsOptional reports whether t is an Optional type.
func IsOptional(t Type) bool {
	_, ok := t.(*Optional)
	return ok
}

// IsAssignable reports whether a value of type `value` can be used where a
// value of type `target` is expected. It is more permissive than Equal:
//   - null is assignable to any Optional.
//   - T is assignable to T? (auto-wrap on assignment).
//   - number is assignable to float (numeric widening).
func IsAssignable(value, target Type) bool {
	if Equal(value, target) {
		return true
	}
	if value == Null {
		_, ok := target.(*Optional)
		return ok
	}
	if value == Number && target == Float {
		return true
	}
	if to, ok := target.(*Optional); ok {
		if Equal(value, to.Elem) {
			return true
		}
		// number → float? widening through an optional is also fine.
		if value == Number && to.Elem == Float {
			return true
		}
	}
	return false
}

// Field is one (name, type) pair in a record. We keep an ordered slice instead
// of a map so codegen can emit positional shell arguments in declared order.
type Field struct {
	Name string
	Type Type
}

// Record is a named struct-like type. The Name is what the user wrote in
// `type Name = { ... }`; equality is by name (nominal typing).
type Record struct {
	Name   string
	Fields []Field
}

func (r *Record) String() string { return r.Name }
func (r *Record) typeNode()      {}

// Lookup finds a field by name in the record. Returns nil if absent.
func (r *Record) Lookup(name string) *Field {
	for i := range r.Fields {
		if r.Fields[i].Name == name {
			return &r.Fields[i]
		}
	}
	return nil
}

// Func describes a function signature.
type Func struct {
	Params []Type
	Result Type
}

func (f *Func) String() string {
	out := "("
	for i, p := range f.Params {
		if i > 0 {
			out += ", "
		}
		out += p.String()
	}
	out += "): " + f.Result.String()
	return out
}
func (f *Func) typeNode() {}

// Equal reports whether two types are the same. We use pointer equality for
// primitives (they're singletons), nominal equality for records (a record
// type is identified by its name), and structural equality for function and
// array types.
func Equal(a, b Type) bool {
	if a == b {
		return true
	}
	if ra, ok := a.(*Record); ok {
		if rb, ok := b.(*Record); ok {
			return ra.Name == rb.Name
		}
		return false
	}
	if aa, ok := a.(*Array); ok {
		if bb, ok := b.(*Array); ok {
			return Equal(aa.Elem, bb.Elem)
		}
		return false
	}
	if oa, ok := a.(*Optional); ok {
		if ob, ok := b.(*Optional); ok {
			return Equal(oa.Elem, ob.Elem)
		}
		return false
	}
	fa, oka := a.(*Func)
	fb, okb := b.(*Func)
	if oka && okb {
		if !Equal(fa.Result, fb.Result) {
			return false
		}
		if len(fa.Params) != len(fb.Params) {
			return false
		}
		for i := range fa.Params {
			if !Equal(fa.Params[i], fb.Params[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// Lookup returns the primitive type for a type name. Returns nil if unknown.
func Lookup(name string) Type {
	switch name {
	case "string":
		return String
	case "number":
		return Number
	case "float":
		return Float
	case "bool":
		return Bool
	case "void":
		return Void
	}
	return nil
}

// Format produces a human-readable form of a type, with a sensible fallback
// so printing a nil type doesn't panic during error reporting.
func Format(t Type) string {
	if t == nil {
		return "<unknown>"
	}
	return fmt.Sprint(t)
}
