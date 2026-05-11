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

// Map is an associative type from a primitive key K to a value V. Keys are
// restricted to string/number/bool so the runtime encodings (sh's flat string
// pairs, Go's map[K]V) stay simple and lookup-cheap. The checker enforces the
// key-type restriction at the syntactic point where a map type is introduced.
type Map struct {
	Key   Type
	Value Type
}

func (m *Map) String() string { return "map<" + m.Key.String() + ", " + m.Value.String() + ">" }
func (m *Map) typeNode()      {}

// Chan is the type of a typed message channel `chan[T]`. The element type
// T is restricted to scalar primitives (string, number, float, bool) in
// v1 because the sh backend serialises every message as a single text
// line; the checker enforces this where a channel type is introduced.
type Chan struct {
	Elem Type
}

func (c *Chan) String() string { return "chan[" + c.Elem.String() + "]" }
func (c *Chan) typeNode()      {}

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

// Variant is one alternative inside a sum type. An empty Fields list means
// the variant carries no payload (a bare tag like `Empty`).
type Variant struct {
	Name   string
	Fields []Field
}

// Sum is a tagged union type. Equality is nominal — two sum types with the
// same Name from different declarations are still distinct.
type Sum struct {
	Name     string
	Variants []Variant
}

func (s *Sum) String() string { return s.Name }
func (s *Sum) typeNode()      {}

// LookupVariant returns the variant with the given name, or nil.
func (s *Sum) LookupVariant(name string) *Variant {
	for i := range s.Variants {
		if s.Variants[i].Name == name {
			return &s.Variants[i]
		}
	}
	return nil
}

// TypeVar is a placeholder for a type parameter inside a generic function's
// signature or body. Each TypeVar instance is unique by pointer identity (the
// checker allocates a fresh set per generic function declaration), so two
// different functions both declaring `<T>` produce distinct TypeVars even
// though their printable name matches.
type TypeVar struct {
	Name string
}

func (v *TypeVar) String() string { return v.Name }
func (v *TypeVar) typeNode()      {}

// ContainsTypeVar reports whether t mentions any *TypeVar anywhere in its
// structure. Used by the checker to detect signatures that still need
// monomorphization at call sites.
func ContainsTypeVar(t Type) bool {
	switch tt := t.(type) {
	case *TypeVar:
		return true
	case *Array:
		return ContainsTypeVar(tt.Elem)
	case *Optional:
		return ContainsTypeVar(tt.Elem)
	case *Map:
		return ContainsTypeVar(tt.Key) || ContainsTypeVar(tt.Value)
	case *Chan:
		return ContainsTypeVar(tt.Elem)
	case *Func:
		for _, p := range tt.Params {
			if ContainsTypeVar(p) {
				return true
			}
		}
		return ContainsTypeVar(tt.Result)
	}
	return false
}

// Substitute returns a copy of t with every *TypeVar replaced by its mapping
// in subst. Types that contain no TypeVars are returned unchanged.
func Substitute(t Type, subst map[*TypeVar]Type) Type {
	if !ContainsTypeVar(t) {
		return t
	}
	switch tt := t.(type) {
	case *TypeVar:
		if r, ok := subst[tt]; ok {
			return r
		}
		return tt
	case *Array:
		return &Array{Elem: Substitute(tt.Elem, subst)}
	case *Optional:
		return &Optional{Elem: Substitute(tt.Elem, subst)}
	case *Map:
		return &Map{Key: Substitute(tt.Key, subst), Value: Substitute(tt.Value, subst)}
	case *Chan:
		return &Chan{Elem: Substitute(tt.Elem, subst)}
	case *Func:
		ps := make([]Type, len(tt.Params))
		for i, p := range tt.Params {
			ps[i] = Substitute(p, subst)
		}
		return &Func{Params: ps, Result: Substitute(tt.Result, subst)}
	}
	return t
}

// Func describes a function signature.
type Func struct {
	Params []Type
	Result Type
	// TypeParams lists the *TypeVar pointers introduced by a generic function
	// declaration. Empty for monomorphic signatures. The slice is stored in
	// declaration order so the checker can match call-site type arguments
	// positionally.
	TypeParams []*TypeVar
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
// array types. TypeVars compare by pointer identity — each generic function's
// `<T>` allocates a fresh *TypeVar, so distinct functions both declaring `T`
// stay distinct even though their printed name matches.
func Equal(a, b Type) bool {
	if a == b {
		return true
	}
	if _, ok := a.(*TypeVar); ok {
		// pointer equality already failed above, so two TypeVars from different
		// declarations are not equal even if their Name matches.
		return false
	}
	if _, ok := b.(*TypeVar); ok {
		return false
	}
	if ra, ok := a.(*Record); ok {
		if rb, ok := b.(*Record); ok {
			return ra.Name == rb.Name
		}
		return false
	}
	if sa, ok := a.(*Sum); ok {
		if sb, ok := b.(*Sum); ok {
			return sa.Name == sb.Name
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
	if ma, ok := a.(*Map); ok {
		if mb, ok := b.(*Map); ok {
			return Equal(ma.Key, mb.Key) && Equal(ma.Value, mb.Value)
		}
		return false
	}
	if ca, ok := a.(*Chan); ok {
		if cb, ok := b.(*Chan); ok {
			return Equal(ca.Elem, cb.Elem)
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
