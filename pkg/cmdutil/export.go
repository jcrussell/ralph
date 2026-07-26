package cmdutil

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"text/template"

	"github.com/itchyny/gojq"
	"github.com/spf13/cobra"

	"github.com/jcrussell/ralph/pkg/iostreams"
)

// JSONFieldNames returns the top-level JSON field names of struct v (or the
// struct v points to), in declaration order, honoring json tags: renamed fields
// use the tag name and json:"-" fields are skipped. Views pass this to
// AddJSONFlags as the valid-field allowlist so the allowlist can never drift
// from the struct's json tags.
func JSONFieldNames(v any) []string {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Name
		if tag := f.Tag.Get("json"); tag != "" {
			base, _, _ := strings.Cut(tag, ",")
			if base == "-" {
				continue
			}
			if base != "" {
				name = base
			}
		}
		out = append(out, name)
	}
	return out
}

// StructFields marshals v to a JSON object and returns only the requested
// top-level keys, so a view type can satisfy RowExporter with a one-line
// ExportData instead of a hand-written per-field switch. An empty fields slice
// returns every key. Requested keys absent from the marshaled object (e.g.
// omitempty zero values) are skipped, matching JSON semantics.
func StructFields(v any, fields []string) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var full map[string]any
	if err := json.Unmarshal(b, &full); err != nil {
		return map[string]any{}
	}
	if len(fields) == 0 {
		return full
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if val, ok := full[f]; ok {
			out[f] = val
		}
	}
	return out
}

// RowExporter emits a filtered map[string]any for the requested JSON fields.
// Each resource/view type implements this to define its JSON contract
// explicitly, independent of the TTY rendering. An empty fields slice means
// "all fields".
type RowExporter interface {
	ExportData(fields []string) map[string]any
}

// Exporter renders shaped rows — already filtered to the requested fields — to
// an IOStreams. It writes a single resource as a JSON object (writeRow) or a
// list as a JSON array (writeRows); keeping the two typed (rather than one
// Write(any)) makes the []map[string]any contract compile-time-visible instead
// of a runtime type check inside each implementation. The methods are
// unexported, so Exporter is sealed to this package: construct one via
// AddJSONFlags or NewExporter. A nil Exporter means the command was not asked
// for JSON output and should fall through to its human renderer.
type Exporter interface {
	Fields() []string
	writeRow(ios *iostreams.IOStreams, row map[string]any) error
	writeRows(ios *iostreams.IOStreams, rows []map[string]any) error
}

// WriteRow shapes a single resource to the requested fields and writes it as a
// top-level JSON object (not a one-element array), so scripts read `.state`
// rather than `.[0].state`.
func WriteRow(ios *iostreams.IOStreams, e Exporter, row RowExporter) error {
	return e.writeRow(ios, row.ExportData(e.Fields()))
}

// WriteRows shapes rows to the requested fields and writes them as a JSON
// array. Factored out so the list commands don't each repeat the loop.
func WriteRows[T RowExporter](ios *iostreams.IOStreams, e Exporter, rows []T) error {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		out[i] = r.ExportData(e.Fields())
	}
	return e.writeRows(ios, out)
}

// AddJSONFlags adds --json, --jq, and --template to cmd. When --json is set,
// PreRunE builds an exporter and stores it in *exporter (left nil otherwise).
// --jq and --template post-process the JSON output and require --json; they are
// mutually exclusive. The jq expression and template are compiled in PreRunE so
// syntax errors surface as FlagErrors (exit 2) before the command body runs.
//
// It snapshots and chains any PreRunE already set on cmd, so set your command's
// own PreRunE (if any) BEFORE calling AddJSONFlags — assigning cmd.PreRunE
// afterwards would drop the exporter wiring.
func AddJSONFlags(cmd *cobra.Command, exporter *Exporter, validFields []string) {
	var jsonFields string
	var jqExpr string
	var tmplStr string

	f := cmd.Flags()
	f.StringVar(&jsonFields, "json", "", fmt.Sprintf("output JSON with the given comma-separated fields (available: %s)", strings.Join(validFields, ",")))
	f.StringVar(&jqExpr, "jq", "", "filter --json output with a jq expression")
	f.StringVar(&tmplStr, "template", "", "format --json output with a Go template")
	cmd.MarkFlagsMutuallyExclusive("jq", "template")

	oldPreRun := cmd.PreRunE
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if oldPreRun != nil {
			if err := oldPreRun(cmd, args); err != nil {
				return err
			}
		}
		return buildExporter(jsonFields, jqExpr, tmplStr, validFields, exporter)
	}
}

// NewExporter builds the same Exporter AddJSONFlags produces, for callers (and
// tests) that construct one directly rather than through cobra flag parsing.
// jsonFields uses the same comma-separated rules; an empty jsonFields returns a
// nil Exporter (no JSON mode). jqExpr/tmplStr require a non-empty jsonFields and
// are mutually exclusive.
func NewExporter(jsonFields, jqExpr, tmplStr string, validFields []string) (Exporter, error) {
	var e Exporter
	if err := buildExporter(jsonFields, jqExpr, tmplStr, validFields, &e); err != nil {
		return nil, err
	}
	return e, nil
}

func buildExporter(jsonFields, jqExpr, tmplStr string, validFields []string, out *Exporter) error {
	if jqExpr != "" && jsonFields == "" {
		return FlagErrorf("--jq requires --json")
	}
	if tmplStr != "" && jsonFields == "" {
		return FlagErrorf("--template requires --json")
	}
	if jsonFields == "" {
		return nil
	}

	fields, err := resolveFields(jsonFields, validFields)
	if err != nil {
		return err
	}
	base := baseExporter{fields: fields}
	switch {
	case jqExpr != "":
		query, err := gojq.Parse(jqExpr)
		if err != nil {
			return FlagErrorf("invalid jq expression: %w", err)
		}
		*out = &jqFilterExporter{baseExporter: base, query: query}
	case tmplStr != "":
		tmpl, err := template.New("").Parse(tmplStr)
		if err != nil {
			return FlagErrorf("invalid template: %w", err)
		}
		*out = &templateExporter{baseExporter: base, tmpl: tmpl}
	default:
		*out = &jsonExporter{baseExporter: base}
	}
	return nil
}

// resolveFields splits the comma-separated --json value, trimming each name and
// validating it against the allowlist. Following byob-output.2 (and
// solvent-streets), --json always requires an explicit field list — there is no
// "all" shorthand; the client picks exactly what it wants.
func resolveFields(jsonFields string, validFields []string) ([]string, error) {
	fields := strings.Split(jsonFields, ",")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
		if !slices.Contains(validFields, fields[i]) {
			return nil, FlagErrorf("unknown JSON field %q; available: %s", fields[i], strings.Join(validFields, ", "))
		}
	}
	return fields, nil
}

// baseExporter holds the shared fields slice for all exporter types.
type baseExporter struct {
	fields []string
}

func (e *baseExporter) Fields() []string { return e.fields }

var (
	_ Exporter = (*jsonExporter)(nil)
	_ Exporter = (*jqFilterExporter)(nil)
	_ Exporter = (*templateExporter)(nil)
)

type jsonExporter struct{ baseExporter }

func (*jsonExporter) writeRow(ios *iostreams.IOStreams, row map[string]any) error {
	return marshalIndent(ios, row)
}

func (*jsonExporter) writeRows(ios *iostreams.IOStreams, rows []map[string]any) error {
	return marshalIndent(ios, rows)
}

func marshalIndent(ios *iostreams.IOStreams, value any) error {
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	_, err = fmt.Fprintln(ios.Out, string(out))
	return err
}

// jqFilterExporter applies a jq expression to the JSON output. The query is
// precompiled in buildExporter so parse errors surface early as FlagErrors.
type jqFilterExporter struct {
	baseExporter
	query *gojq.Query
}

func (e *jqFilterExporter) writeRow(ios *iostreams.IOStreams, row map[string]any) error {
	// A single object is one input, so `.state` works directly (no `.[0]`).
	return e.run(ios, row)
}

func (e *jqFilterExporter) writeRows(ios *iostreams.IOStreams, rows []map[string]any) error {
	// gojq evaluates .[] on []any, not []map[string]any — widen the element type
	// so `.[] | select(...)` / `map(...)` work as users expect on list output.
	generic := make([]any, len(rows))
	for i, r := range rows {
		generic[i] = r
	}
	return e.run(ios, generic)
}

// run feeds the whole input to the query as one value and streams every result
// on its own line. gojq returns a generator; runtime errors come back as values
// that type-assert to error.
func (e *jqFilterExporter) run(ios *iostreams.IOStreams, input any) error {
	iter := e.query.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			return nil
		}
		if err, isErr := v.(error); isErr {
			return fmt.Errorf("jq error: %w", err)
		}
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(ios.Out, string(out)); err != nil {
			return err
		}
	}
}

// templateExporter applies a Go template to the JSON output. The template is
// precompiled in buildExporter. A list executes the template once per element;
// a single object executes it once.
type templateExporter struct {
	baseExporter
	tmpl *template.Template
}

func (e *templateExporter) writeRow(ios *iostreams.IOStreams, row map[string]any) error {
	return e.execOne(ios, row)
}

func (e *templateExporter) writeRows(ios *iostreams.IOStreams, rows []map[string]any) error {
	for _, item := range rows {
		if err := e.execOne(ios, item); err != nil {
			return err
		}
	}
	return nil
}

func (e *templateExporter) execOne(ios *iostreams.IOStreams, item map[string]any) error {
	if err := e.tmpl.Execute(ios.Out, item); err != nil {
		return fmt.Errorf("template error: %w", err)
	}
	_, err := fmt.Fprintln(ios.Out)
	return err
}
