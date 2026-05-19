package diag

import (
	"encoding/json"
	"io"

	"github.com/enekos/tartalo/internal/token"
)

// JSONSchemaVersion is the schema version stamped onto `tartalo check --json`
// output. Increment when fields are removed or repurposed; additive changes do
// not require a version bump.
const JSONSchemaVersion = 1

// Packet is the envelope written by `tartalo check --json`. Consumers should
// inspect `Ok` rather than treat the absence of `Diagnostics` as success.
type Packet struct {
	SchemaVersion int      `json:"schemaVersion"`
	Ok            bool     `json:"ok"`
	Diagnostics   []Record `json:"diagnostics"`
}

// Record is the JSON shape of a single diagnostic.
type Record struct {
	Code     string       `json:"code"`
	Severity string       `json:"severity"`
	Message  string       `json:"message"`
	Path     string       `json:"path,omitempty"`
	Line     int          `json:"line,omitempty"`
	Column   int          `json:"column,omitempty"`
	End      *EndPos      `json:"end,omitempty"`
	Hint     string       `json:"hint,omitempty"`
	Suggest  string       `json:"suggest,omitempty"`
	Notes    []NoteRecord `json:"notes,omitempty"`
	// Explain is the literal command an agent can run to load the long-form
	// explanation. Always shaped as `tartalo explain <code>`.
	Explain string `json:"explain,omitempty"`
}

// EndPos is the optional end-of-range cursor when the diagnostic spans more
// than one column / line.
type EndPos struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// NoteRecord is the JSON shape for a related-location note.
type NoteRecord struct {
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

// ToRecord projects a *Diag into the JSON shape. If the diag has no Code we
// run the classifier so consumers always see a code.
func (d *Diag) ToRecord() Record {
	code := d.Code
	if code == "" {
		code = InferCode(d.Msg)
	}
	r := Record{
		Code:     code,
		Severity: d.Severity.String(),
		Message:  d.Msg,
		Path:     d.Pos.File,
		Line:     d.Pos.Line,
		Column:   d.Pos.Col,
		Hint:     d.Hint,
		Suggest:  d.Suggest,
	}
	if code != "" {
		r.Explain = "tartalo explain " + code
	}
	if d.End.Line > 0 {
		r.End = &EndPos{Line: d.End.Line, Column: d.End.Col}
	}
	for _, n := range d.Notes {
		nr := NoteRecord{Message: n.Msg}
		if n.Pos.Line > 0 {
			nr.Path = n.Pos.File
			nr.Line = n.Pos.Line
			nr.Column = n.Pos.Col
		}
		r.Notes = append(r.Notes, nr)
	}
	return r
}

// EncodePacket renders diags to JSON and writes the packet to w. The packet
// is `ok: true` only when diags is empty.
func EncodePacket(w io.Writer, diags []*Diag) error {
	records := make([]Record, 0, len(diags))
	for _, d := range diags {
		if d == nil {
			continue
		}
		records = append(records, d.ToRecord())
	}
	pkt := Packet{
		SchemaVersion: JSONSchemaVersion,
		Ok:            len(records) == 0,
		Diagnostics:   records,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(pkt)
}

// PosFor is a tiny helper for producers that want to construct a Note from a
// (file, line, col) triple without importing token directly. Unused today;
// reserved.
func PosFor(file string, line, col int) token.Pos {
	return token.Pos{File: file, Line: line, Col: col}
}
