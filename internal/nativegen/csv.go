package nativegen

import (
	"sort"
	"strconv"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/types"
)

// CSV codegen registers per-record-type reader/writer helpers. Each unique
// record type the program calls readCsv / writeCsv for gets one helper
// function emitted at the bottom of the program. The runtime helpers use
// encoding/csv, so quoted fields and commas-in-strings are handled correctly.

// compileReadCsvNative produces the call expression for one readCsv invocation.
// The result row-record type comes from g.info.Types[e]. We register the
// record so emitCsvHelpers will lay down the corresponding _tt_readCsv_<Rec>
// function in the runtime tail.
func (g *Generator) compileReadCsvNative(e *ast.CallExpr) string {
	want := g.info.Types[e]
	arr, _ := want.(*types.Array)
	if arr == nil {
		return `nil /* readCsv: missing result type */`
	}
	rec, _ := arr.Elem.(*types.Record)
	if rec == nil {
		return `nil /* readCsv: row type is not a record */`
	}
	if g.csvReaders == nil {
		g.csvReaders = map[string]*types.Record{}
	}
	g.csvReaders[rec.Name] = rec
	g.addImport("encoding/csv")
	g.addImport("fmt")
	g.addImport("os")
	if needsParseInt(rec) {
		g.addImport("strconv")
		g.addImport("strings")
	}
	if needsParseFloat(rec) {
		g.addImport("strconv")
		g.addImport("strings")
	}
	pathExpr := g.compileExpr(e.Args[0])
	return "_tt_readCsv_" + rec.Name + "(" + pathExpr + ")"
}

// compileWriteCsvNative is the writeCsv counterpart. Symmetric in structure
// to compileReadCsvNative.
func (g *Generator) compileWriteCsvNative(e *ast.CallExpr, args []string, argTypes []types.Type) string {
	arr, _ := argTypes[0].(*types.Array)
	if arr == nil {
		return `func() {}() /* writeCsv: rows arg not an array */`
	}
	rec, _ := arr.Elem.(*types.Record)
	if rec == nil {
		return `func() {}() /* writeCsv: row type is not a record */`
	}
	if g.csvWriters == nil {
		g.csvWriters = map[string]*types.Record{}
	}
	g.csvWriters[rec.Name] = rec
	g.addImport("encoding/csv")
	g.addImport("fmt")
	g.addImport("os")
	if needsFormatInt(rec) || needsFormatFloat(rec) {
		g.addImport("strconv")
	}
	return "func() { _tt_writeCsv_" + rec.Name + "(" + args[0] + ", " + args[1] + ") }()"
}

// emitCsvHelpers writes one reader and/or writer per registered record type.
// Called from emitProgram after pass 2 so user types are already declared.
func (g *Generator) emitCsvHelpers(out *strings.Builder) {
	if len(g.csvReaders) == 0 && len(g.csvWriters) == 0 {
		return
	}
	names := make([]string, 0, len(g.csvReaders)+len(g.csvWriters))
	seen := map[string]bool{}
	for n := range g.csvReaders {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for n := range g.csvWriters {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if rec, ok := g.csvReaders[name]; ok {
			out.WriteString(g.csvReaderFor(rec))
		}
		if rec, ok := g.csvWriters[name]; ok {
			out.WriteString(g.csvWriterFor(rec))
		}
	}
}

func (g *Generator) csvReaderFor(rec *types.Record) string {
	var b strings.Builder
	tn := goTypeName(rec.Name)
	b.WriteString("\nfunc _tt_readCsv_" + rec.Name + "(path string) []" + tn + " {\n")
	b.WriteString("\tf, err := os.Open(path)\n")
	b.WriteString("\tif err != nil { fmt.Fprintf(os.Stderr, \"tartalo: readCsv: %s\\n\", err); os.Exit(1) }\n")
	b.WriteString("\tdefer f.Close()\n")
	b.WriteString("\tr := csv.NewReader(f)\n")
	b.WriteString("\tr.FieldsPerRecord = -1\n")
	b.WriteString("\trows, err := r.ReadAll()\n")
	b.WriteString("\tif err != nil { fmt.Fprintf(os.Stderr, \"tartalo: readCsv: %s\\n\", err); os.Exit(1) }\n")
	b.WriteString("\tif len(rows) == 0 { return nil }\n")
	b.WriteString("\tidx := map[string]int{}\n")
	b.WriteString("\tfor i, h := range rows[0] { idx[h] = i }\n")
	b.WriteString("\tout := make([]" + tn + ", 0, len(rows)-1)\n")
	b.WriteString("\tfor _, row := range rows[1:] {\n")
	b.WriteString("\t\tvar v " + tn + "\n")
	for _, f := range rec.Fields {
		b.WriteString("\t\tif _i, _ok := idx[" + strconv.Quote(f.Name) + "]; _ok && _i < len(row) {\n")
		b.WriteString("\t\t\t" + csvParseExpr("row[_i]", "v."+goFieldName(f.Name), f.Type))
		b.WriteString("\t\t}\n")
	}
	b.WriteString("\t\tout = append(out, v)\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn out\n}\n")
	return b.String()
}

func (g *Generator) csvWriterFor(rec *types.Record) string {
	var b strings.Builder
	tn := goTypeName(rec.Name)
	b.WriteString("\nfunc _tt_writeCsv_" + rec.Name + "(rows []" + tn + ", path string) {\n")
	b.WriteString("\tf, err := os.Create(path)\n")
	b.WriteString("\tif err != nil { fmt.Fprintf(os.Stderr, \"tartalo: writeCsv: %s\\n\", err); os.Exit(1) }\n")
	b.WriteString("\tdefer f.Close()\n")
	b.WriteString("\tw := csv.NewWriter(f)\n")
	b.WriteString("\tdefer w.Flush()\n")
	b.WriteString("\tif err := w.Write([]string{")
	for i, f := range rec.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Quote(f.Name))
	}
	b.WriteString("}); err != nil { fmt.Fprintf(os.Stderr, \"tartalo: writeCsv: %s\\n\", err); os.Exit(1) }\n")
	b.WriteString("\tfor _, r := range rows {\n")
	b.WriteString("\t\tif err := w.Write([]string{\n")
	for _, f := range rec.Fields {
		b.WriteString("\t\t\t" + csvFormatExpr("r."+goFieldName(f.Name), f.Type) + ",\n")
	}
	b.WriteString("\t\t}); err != nil { fmt.Fprintf(os.Stderr, \"tartalo: writeCsv: %s\\n\", err); os.Exit(1) }\n")
	b.WriteString("\t}\n}\n")
	return b.String()
}

// csvParseExpr returns a Go statement that assigns the parsed value of
// `src` (a string expression) into `dst`, given the target Tartalo type.
// Optional types use the empty string as null.
func csvParseExpr(src, dst string, t types.Type) string {
	if opt, ok := t.(*types.Optional); ok {
		switch opt.Elem {
		case types.String:
			return "if _s := " + src + "; _s != \"\" { " + dst + " = &_s }\n"
		case types.Number:
			return "if _s := " + src + "; _s != \"\" { _v, _err := strconv.ParseInt(strings.TrimSpace(_s), 10, 64); if _err == nil { " + dst + " = &_v } }\n"
		case types.Float:
			return "if _s := " + src + "; _s != \"\" { _v, _err := strconv.ParseFloat(strings.TrimSpace(_s), 64); if _err == nil { " + dst + " = &_v } }\n"
		case types.Bool:
			return "if _s := " + src + "; _s != \"\" { _v := _s == \"true\" || _s == \"1\"; " + dst + " = &_v }\n"
		}
	}
	switch t {
	case types.String:
		return dst + " = " + src + "\n"
	case types.Number:
		return "_v, _ := strconv.ParseInt(strings.TrimSpace(" + src + "), 10, 64); " + dst + " = _v\n"
	case types.Float:
		return "_v, _ := strconv.ParseFloat(strings.TrimSpace(" + src + "), 64); " + dst + " = _v\n"
	case types.Bool:
		return dst + " = " + src + " == \"true\" || " + src + " == \"1\"\n"
	}
	return "// unsupported csv field type: " + types.Format(t) + "\n"
}

// csvFormatExpr returns a Go expression of type `string` that renders the
// given record field for CSV output. Optional values become "" when nil.
func csvFormatExpr(src string, t types.Type) string {
	if opt, ok := t.(*types.Optional); ok {
		switch opt.Elem {
		case types.String:
			return "func() string { if " + src + " == nil { return \"\" }; return *" + src + " }()"
		case types.Number:
			return "func() string { if " + src + " == nil { return \"\" }; return strconv.FormatInt(*" + src + ", 10) }()"
		case types.Float:
			return "func() string { if " + src + " == nil { return \"\" }; return strconv.FormatFloat(*" + src + ", 'f', -1, 64) }()"
		case types.Bool:
			return "func() string { if " + src + " == nil { return \"\" }; if *" + src + " { return \"true\" }; return \"false\" }()"
		}
	}
	switch t {
	case types.String:
		return src
	case types.Number:
		return "strconv.FormatInt(" + src + ", 10)"
	case types.Float:
		return "strconv.FormatFloat(" + src + ", 'f', -1, 64)"
	case types.Bool:
		return "func() string { if " + src + " { return \"true\" }; return \"false\" }()"
	}
	return `""`
}

// needsParseInt reports whether any field needs strconv.ParseInt — drives
// the import gate. ParseFloat is similar.
func needsParseInt(rec *types.Record) bool {
	for _, f := range rec.Fields {
		if f.Type == types.Number {
			return true
		}
		if opt, ok := f.Type.(*types.Optional); ok && opt.Elem == types.Number {
			return true
		}
	}
	return false
}

func needsParseFloat(rec *types.Record) bool {
	for _, f := range rec.Fields {
		if f.Type == types.Float {
			return true
		}
		if opt, ok := f.Type.(*types.Optional); ok && opt.Elem == types.Float {
			return true
		}
	}
	return false
}

func needsFormatInt(rec *types.Record) bool {
	return needsParseInt(rec)
}

func needsFormatFloat(rec *types.Record) bool {
	return needsParseFloat(rec)
}
