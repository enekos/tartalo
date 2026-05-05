package types

import "testing"

func TestLookupReturnsPrimitives(t *testing.T) {
	cases := []struct {
		name string
		want Type
	}{
		{"string", String},
		{"number", Number},
		{"float", Float},
		{"bool", Bool},
		{"void", Void},
	}
	for _, c := range cases {
		if got := Lookup(c.name); got != c.want {
			t.Errorf("Lookup(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLookupUnknownReturnsNil(t *testing.T) {
	if got := Lookup("widget"); got != nil {
		t.Errorf("Lookup unknown name returned %v, want nil", got)
	}
	if got := Lookup(""); got != nil {
		t.Errorf("Lookup empty name returned %v, want nil", got)
	}
	// "null" and "<invalid>" are real primitives but not user-spellable: Lookup
	// must NOT surface them.
	if got := Lookup("null"); got != nil {
		t.Errorf("Lookup(\"null\") leaked Null primitive: %v", got)
	}
}

func TestEqualPrimitivesByPointer(t *testing.T) {
	if !Equal(String, String) {
		t.Error("String not equal to itself")
	}
	if Equal(String, Number) {
		t.Error("String == Number")
	}
}

func TestEqualArrays(t *testing.T) {
	if !Equal(&Array{Elem: String}, &Array{Elem: String}) {
		t.Error("string[] != string[]")
	}
	if Equal(&Array{Elem: String}, &Array{Elem: Number}) {
		t.Error("string[] == number[]")
	}
	if Equal(&Array{Elem: String}, String) {
		t.Error("string[] == string")
	}
}

func TestEqualOptionals(t *testing.T) {
	if !Equal(&Optional{Elem: String}, &Optional{Elem: String}) {
		t.Error("string? != string?")
	}
	if Equal(&Optional{Elem: String}, &Optional{Elem: Number}) {
		t.Error("string? == number?")
	}
	if Equal(&Optional{Elem: String}, String) {
		t.Error("string? == string")
	}
}

func TestEqualRecordsByName(t *testing.T) {
	a := &Record{Name: "Point", Fields: []Field{{Name: "x", Type: Number}}}
	b := &Record{Name: "Point", Fields: []Field{{Name: "y", Type: String}}} // intentionally diff fields
	c := &Record{Name: "Other", Fields: []Field{{Name: "x", Type: Number}}}
	if !Equal(a, b) {
		t.Error("records with same name should be Equal regardless of field shape (nominal)")
	}
	if Equal(a, c) {
		t.Error("records with different names compared equal")
	}
	if Equal(a, String) {
		t.Error("record == primitive")
	}
}

func TestEqualFuncsStructural(t *testing.T) {
	f1 := &Func{Params: []Type{String, Number}, Result: Bool}
	f2 := &Func{Params: []Type{String, Number}, Result: Bool}
	f3 := &Func{Params: []Type{String}, Result: Bool}
	f4 := &Func{Params: []Type{String, Number}, Result: String}
	if !Equal(f1, f2) {
		t.Error("structurally identical func types should be Equal")
	}
	if Equal(f1, f3) {
		t.Error("funcs with different arity compared equal")
	}
	if Equal(f1, f4) {
		t.Error("funcs with different result type compared equal")
	}
	if Equal(f1, String) {
		t.Error("func == primitive")
	}
}

func TestEqualMixedKindsReturnsFalse(t *testing.T) {
	cases := []struct {
		a, b Type
	}{
		{&Array{Elem: String}, &Optional{Elem: String}},
		{&Optional{Elem: String}, &Array{Elem: String}},
		{&Record{Name: "R"}, &Func{Result: Void}},
	}
	for _, c := range cases {
		if Equal(c.a, c.b) {
			t.Errorf("%v should not equal %v", c.a, c.b)
		}
	}
}

func TestIsAssignableSameType(t *testing.T) {
	if !IsAssignable(String, String) {
		t.Error("string not assignable to string")
	}
	if !IsAssignable(&Array{Elem: Number}, &Array{Elem: Number}) {
		t.Error("number[] not assignable to itself")
	}
}

func TestIsAssignableNullToOptional(t *testing.T) {
	if !IsAssignable(Null, &Optional{Elem: String}) {
		t.Error("null not assignable to string?")
	}
	if IsAssignable(Null, String) {
		t.Error("null assignable to non-optional string")
	}
	if IsAssignable(Null, Number) {
		t.Error("null assignable to non-optional number")
	}
}

func TestIsAssignableAutoWrap(t *testing.T) {
	if !IsAssignable(String, &Optional{Elem: String}) {
		t.Error("string should auto-wrap into string?")
	}
	if !IsAssignable(Number, &Optional{Elem: Number}) {
		t.Error("number should auto-wrap into number?")
	}
	if IsAssignable(String, &Optional{Elem: Number}) {
		t.Error("string should NOT auto-wrap into number?")
	}
}

func TestIsAssignableNumericWidening(t *testing.T) {
	if !IsAssignable(Number, Float) {
		t.Error("number should widen to float")
	}
	if IsAssignable(Float, Number) {
		t.Error("float must NOT narrow to number implicitly")
	}
	// Widening through optional: number → float?
	if !IsAssignable(Number, &Optional{Elem: Float}) {
		t.Error("number should widen-and-wrap into float?")
	}
}

func TestIsAssignableMismatch(t *testing.T) {
	if IsAssignable(String, Number) {
		t.Error("string assignable to number")
	}
	if IsAssignable(Bool, &Optional{Elem: String}) {
		t.Error("bool assignable to string?")
	}
}

func TestUnwrap(t *testing.T) {
	if got := Unwrap(&Optional{Elem: String}); got != String {
		t.Errorf("Unwrap(string?) = %v, want string", got)
	}
	if got := Unwrap(String); got != String {
		t.Errorf("Unwrap(string) = %v, want string (no-op for non-optional)", got)
	}
	arr := &Array{Elem: Number}
	if got := Unwrap(arr); got != arr {
		t.Errorf("Unwrap(number[]) returned different pointer; should be no-op")
	}
}

func TestIsOptional(t *testing.T) {
	if !IsOptional(&Optional{Elem: String}) {
		t.Error("string? not detected as optional")
	}
	if IsOptional(String) {
		t.Error("string detected as optional")
	}
	if IsOptional(&Array{Elem: String}) {
		t.Error("string[] detected as optional")
	}
}

func TestRecordLookup(t *testing.T) {
	r := &Record{
		Name: "Point",
		Fields: []Field{
			{Name: "x", Type: Number},
			{Name: "y", Type: Number},
			{Name: "label", Type: String},
		},
	}
	if f := r.Lookup("x"); f == nil || f.Type != Number {
		t.Errorf("Lookup(x) = %v, want field of type number", f)
	}
	if f := r.Lookup("label"); f == nil || f.Type != String {
		t.Errorf("Lookup(label) = %v, want field of type string", f)
	}
	if f := r.Lookup("z"); f != nil {
		t.Errorf("Lookup(z) = %v, want nil", f)
	}
}

func TestStringFormatting(t *testing.T) {
	cases := []struct {
		t    Type
		want string
	}{
		{String, "string"},
		{Number, "number"},
		{Bool, "bool"},
		{&Array{Elem: String}, "string[]"},
		{&Optional{Elem: Number}, "number?"},
		{&Array{Elem: &Optional{Elem: String}}, "string?[]"},
		{&Record{Name: "Point"}, "Point"},
		{&Func{Params: []Type{String, Number}, Result: Bool}, "(string, number): bool"},
		{&Func{Params: nil, Result: Void}, "(): void"},
	}
	for _, c := range cases {
		if got := c.t.String(); got != c.want {
			t.Errorf("%T.String() = %q, want %q", c.t, got, c.want)
		}
	}
}

func TestFormatHandlesNil(t *testing.T) {
	if got := Format(nil); got != "<unknown>" {
		t.Errorf("Format(nil) = %q, want <unknown>", got)
	}
	if got := Format(String); got != "string" {
		t.Errorf("Format(string) = %q", got)
	}
}

func TestNullAndInvalidAreDistinct(t *testing.T) {
	// Defensive: these are both *Primitive but should never compare equal.
	if Equal(Null, Invalid) {
		t.Error("Null compared equal to Invalid")
	}
	if Equal(Null, String) {
		t.Error("Null compared equal to String")
	}
}

func TestEqualNilsAndZeroValues(t *testing.T) {
	// Equal(nil, nil) goes through `a == b` (both nil interfaces) and returns
	// true. This is a reasonable contract: nil is its own equivalence class
	// and downstream code already short-circuits on Invalid.
	if !Equal(nil, nil) {
		t.Error("Equal(nil, nil) should be true")
	}
	if Equal(nil, String) {
		t.Error("Equal(nil, string) should be false")
	}
}
