package cmdutil

import (
	"errors"
	"strings"
	"testing"
	"text/template"

	"github.com/itchyny/gojq"

	"github.com/jcrussell/ralph/pkg/iostreams"
)

// fakeRow is a minimal RowExporter used by the exporter tests.
type fakeRow struct {
	Name  string
	Count int
}

func (r fakeRow) ExportData(fields []string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		switch f {
		case "name":
			out[f] = r.Name
		case "count":
			out[f] = r.Count
		}
	}
	return out
}

func TestJSONExporter_Write(t *testing.T) {
	t.Run("list stays an array", func(t *testing.T) {
		ios, bufs := iostreams.Test()
		e := &jsonExporter{baseExporter{fields: []string{"name", "count"}}}
		rows := []map[string]any{{"name": "a", "count": 1}, {"name": "b", "count": 2}}
		if err := e.writeRows(ios, rows); err != nil {
			t.Fatal(err)
		}
		got := strings.TrimSpace(bufs.Out.String())
		if !strings.HasPrefix(got, "[") {
			t.Errorf("list output should be an array, got %q", got)
		}
		if !strings.Contains(got, `"name": "a"`) || !strings.Contains(got, `"count": 2`) {
			t.Errorf("missing expected fields: %q", got)
		}
	})
	t.Run("single object stays an object", func(t *testing.T) {
		ios, bufs := iostreams.Test()
		e := &jsonExporter{baseExporter{fields: []string{"name"}}}
		if err := e.writeRow(ios, map[string]any{"name": "solo"}); err != nil {
			t.Fatal(err)
		}
		got := strings.TrimSpace(bufs.Out.String())
		if !strings.HasPrefix(got, "{") {
			t.Errorf("single-object output should be an object, got %q", got)
		}
	})
	t.Run("empty rows marshal to [] not null", func(t *testing.T) {
		ios, bufs := iostreams.Test()
		e := &jsonExporter{baseExporter{fields: []string{"name"}}}
		if err := e.writeRows(ios, []map[string]any{}); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(bufs.Out.String()); got != "[]" {
			t.Errorf("empty rows = %q, want %q", got, "[]")
		}
	})
}

// TestResolveFields covers whitespace trimming and rejection of unknown fields.
func TestResolveFields(t *testing.T) {
	valid := []string{"name", "count"}
	t.Run("trims spaced list", func(t *testing.T) {
		got, err := resolveFields(" name , count ", valid)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0] != "name" || got[1] != "count" {
			t.Errorf("resolveFields = %v, want [name count]", got)
		}
	})
	t.Run("unknown field is a FlagError", func(t *testing.T) {
		_, err := resolveFields("name,bogus", valid)
		assertFlagError(t, err)
	})
}

func TestJQFilterExporter_Write(t *testing.T) {
	t.Run("selector over a list", func(t *testing.T) {
		ios, bufs := iostreams.Test()
		query, err := gojq.Parse(".[].name")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		e := &jqFilterExporter{baseExporter{[]string{"name"}}, query}
		rows := []map[string]any{{"name": "first"}, {"name": "second"}}
		if err := e.writeRows(ios, rows); err != nil {
			t.Fatal(err)
		}
		got := bufs.Out.String()
		if !strings.Contains(got, `"first"`) || !strings.Contains(got, `"second"`) {
			t.Errorf("jq output missing names: %q", got)
		}
	})
	t.Run("selector over a single object", func(t *testing.T) {
		ios, bufs := iostreams.Test()
		query, err := gojq.Parse(".name")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		e := &jqFilterExporter{baseExporter{[]string{"name"}}, query}
		if err := e.writeRow(ios, map[string]any{"name": "solo"}); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(bufs.Out.String()); got != `"solo"` {
			t.Errorf("jq on object = %q, want %q", got, `"solo"`)
		}
	})
}

func TestTemplateExporter_Write(t *testing.T) {
	t.Run("per-element over a list", func(t *testing.T) {
		ios, bufs := iostreams.Test()
		tmpl, err := template.New("").Parse(`{{.name}}={{.count}}`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		e := &templateExporter{baseExporter{[]string{"name", "count"}}, tmpl}
		rows := []map[string]any{{"name": "a", "count": 1}, {"name": "b", "count": 2}}
		if err := e.writeRows(ios, rows); err != nil {
			t.Fatal(err)
		}
		got := bufs.Out.String()
		if !strings.Contains(got, "a=1") || !strings.Contains(got, "b=2") {
			t.Errorf("unexpected template output: %q", got)
		}
	})
	t.Run("once over a single object", func(t *testing.T) {
		ios, bufs := iostreams.Test()
		tmpl, err := template.New("").Parse(`{{.name}}`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		e := &templateExporter{baseExporter{[]string{"name"}}, tmpl}
		if err := e.writeRow(ios, map[string]any{"name": "solo"}); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(bufs.Out.String()); got != "solo" {
			t.Errorf("template on object = %q, want %q", got, "solo")
		}
	})
}

// TestBuildExporter_Guards verifies --jq/--template require --json and that
// malformed expressions are rejected early as FlagErrors before any exporter
// is stored.
func TestBuildExporter_Guards(t *testing.T) {
	valid := []string{"name", "count"}

	t.Run("--jq requires --json", func(t *testing.T) {
		var e Exporter
		assertFlagError(t, buildExporter("", ".name", "", valid, &e))
		if e != nil {
			t.Errorf("exporter set without --json: %T", e)
		}
	})
	t.Run("--template requires --json", func(t *testing.T) {
		var e Exporter
		assertFlagError(t, buildExporter("", "", "{{.name}}", valid, &e))
	})
	t.Run("no --json leaves exporter nil", func(t *testing.T) {
		var e Exporter
		if err := buildExporter("", "", "", valid, &e); err != nil {
			t.Fatal(err)
		}
		if e != nil {
			t.Errorf("exporter should be nil, got %T", e)
		}
	})
	t.Run("bad jq is a FlagError", func(t *testing.T) {
		var e Exporter
		assertFlagError(t, buildExporter("name", ".foo |", "", valid, &e))
		if e != nil {
			t.Errorf("exporter set on parse failure: %T", e)
		}
	})
	t.Run("bad template is a FlagError", func(t *testing.T) {
		var e Exporter
		assertFlagError(t, buildExporter("name", "", "{{.name", valid, &e))
	})
	t.Run("valid jq builds a working exporter", func(t *testing.T) {
		var e Exporter
		if err := buildExporter("name", ".name", "", valid, &e); err != nil {
			t.Fatalf("buildExporter: %v", err)
		}
		ios, bufs := iostreams.Test()
		if err := e.writeRow(ios, map[string]any{"name": "x"}); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if !strings.Contains(bufs.Out.String(), `"x"`) {
			t.Errorf("jq output missing value: %q", bufs.Out.String())
		}
	})
}

func TestWriteRow(t *testing.T) {
	ios, bufs := iostreams.Test()
	e := &jsonExporter{baseExporter{fields: []string{"name"}}}
	if err := WriteRow(ios, e, fakeRow{Name: "x", Count: 99}); err != nil {
		t.Fatal(err)
	}
	got := bufs.Out.String()
	if !strings.Contains(got, `"name": "x"`) {
		t.Errorf("missing name: %q", got)
	}
	if strings.Contains(got, "count") {
		t.Errorf("count not requested but present: %q", got)
	}
}

func TestWriteRows(t *testing.T) {
	ios, bufs := iostreams.Test()
	e := &jsonExporter{baseExporter{fields: []string{"name"}}}
	rows := []fakeRow{{Name: "x", Count: 99}, {Name: "y", Count: 100}}
	if err := WriteRows(ios, e, rows); err != nil {
		t.Fatal(err)
	}
	got := bufs.Out.String()
	if !strings.Contains(got, `"name": "x"`) || !strings.Contains(got, `"name": "y"`) {
		t.Errorf("expected both names: %q", got)
	}
	if strings.Contains(got, "count") {
		t.Errorf("count not requested but present: %q", got)
	}
}

func assertFlagError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("got nil error, want FlagError")
	}
	var fe *FlagError
	if !errors.As(err, &fe) {
		t.Fatalf("got %T (%v), want *FlagError", err, err)
	}
}
