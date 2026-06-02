package trace

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRenderHappyPath(t *testing.T) {
	fs := fstest.MapFS{
		"iter-0001-1700000000.json":            {Data: []byte(`{"iter":1,"state":"clean","narrative":"first"}`)},
		"iter-0001-1700000000-prompt.txt":      {Data: []byte("rendered prompt body\n")},
		"iter-0001-1700000000-stdout.txt":      {Data: []byte("line1\nline2\nline3\n")},
		"iter-0001-1700000000-stderr.txt":      {Data: []byte("err line\n")},
		"iter-0001-1700000000-gate-stdout.txt": {Data: []byte("gate out line\n")},
		"iter-0001-1700000000-gate-stderr.txt": {Data: []byte("gate err line\n")},
		"iter-0002-1700000010.json":            {Data: []byte(`{"iter":2}`)},
	}
	var buf bytes.Buffer
	if err := Render(fs, 1, 0, &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"=== ITERATION RECORD (iter-0001-1700000000.json) ===",
		`"narrative": "first"`,
		"=== RENDERED PROMPT (iter-0001-1700000000-prompt.txt) ===",
		"rendered prompt body",
		"=== RUNNER STDOUT (iter-0001-1700000000-stdout.txt) ===",
		"line1\nline2\nline3",
		"=== RUNNER STDERR (iter-0001-1700000000-stderr.txt) ===",
		"err line",
		"=== GATE STDOUT (iter-0001-1700000000-gate-stdout.txt) ===",
		"gate out line",
		"=== GATE STDERR (iter-0001-1700000000-gate-stderr.txt) ===",
		"gate err line",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing in output: %q\n---\n%s", want, out)
		}
	}
	// Wrong iter shouldn't appear.
	if strings.Contains(out, "iter-0002") {
		t.Errorf("output included unrelated iter:\n%s", out)
	}
}

func TestRenderMissingArtifacts(t *testing.T) {
	// Only the record JSON is present; prompt/stdout/stderr missing.
	fs := fstest.MapFS{
		"iter-0007-x.json": {Data: []byte(`{"iter":7}`)},
	}
	var buf bytes.Buffer
	if err := Render(fs, 7, 0, &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"=== ITERATION RECORD (iter-0007-x.json) ===",
		"=== RENDERED PROMPT (iter-0007-x-prompt.txt) ===\n(missing)",
		"=== RUNNER STDOUT (iter-0007-x-stdout.txt) ===\n(missing)",
		"=== RUNNER STDERR (iter-0007-x-stderr.txt) ===\n(missing)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing in output: %q\n---\n%s", want, out)
		}
	}
}

func TestRenderUnknownIter(t *testing.T) {
	fs := fstest.MapFS{
		"iter-0001-x.json": {Data: []byte(`{}`)},
	}
	var buf bytes.Buffer
	err := Render(fs, 99, 0, &buf)
	if err == nil {
		t.Fatalf("Render(unknown iter) = nil, want error")
	}
	if !strings.Contains(err.Error(), "no iter-0099") {
		t.Errorf("err = %v, want 'no iter-0099' substring", err)
	}
}

func TestTailTruncates(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"a\nb\nc\nd\ne\n", 2, "d\ne\n"},
		{"a\nb\nc\n", 5, "a\nb\nc\n"}, // n > lines: unchanged
		{"a\nb\nc\n", 0, "a\nb\nc\n"}, // n=0: unchanged
		{"a\nb\nc", 2, "b\nc"},        // no trailing newline preserved
	}
	for _, c := range cases {
		if got := tail(c.in, c.n); got != c.want {
			t.Errorf("tail(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestRenderTailLines(t *testing.T) {
	stdout := "L1\nL2\nL3\nL4\nL5\nL6\n"
	fs := fstest.MapFS{
		"iter-0001-x.json":       {Data: []byte(`{"iter":1}`)},
		"iter-0001-x-stdout.txt": {Data: []byte(stdout)},
		"iter-0001-x-stderr.txt": {Data: []byte("ok\n")},
		"iter-0001-x-prompt.txt": {Data: []byte("p")},
	}
	var buf bytes.Buffer
	if err := Render(fs, 1, 3, &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "L4\nL5\nL6") {
		t.Errorf("--tail-lines=3 should include L4-L6:\n%s", out)
	}
	if strings.Contains(out, "L1") || strings.Contains(out, "L2") || strings.Contains(out, "L3") {
		t.Errorf("--tail-lines=3 should not include L1-L3:\n%s", out)
	}
}

func TestRenderStemExactDisambiguation(t *testing.T) {
	// Two iterations share number 0001 (an fsm reset replays it). Render
	// by number picks the lexicographically first; RenderStem must let a
	// caller select the exact one.
	fs := fstest.MapFS{
		"iter-0001-20260101T000000Z.json":       {Data: []byte(`{"iter":1,"narrative":"older run"}`)},
		"iter-0001-20260101T000000Z-prompt.txt": {Data: []byte("older prompt\n")},
		"iter-0001-20260601T000000Z.json":       {Data: []byte(`{"iter":1,"narrative":"newer run"}`)},
		"iter-0001-20260601T000000Z-prompt.txt": {Data: []byte("newer prompt\n")},
	}
	var buf bytes.Buffer
	if err := RenderStem(fs, "iter-0001-20260601T000000Z", 0, &buf); err != nil {
		t.Fatalf("RenderStem: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "newer run") || !strings.Contains(out, "newer prompt") {
		t.Errorf("RenderStem did not render the requested stem:\n%s", out)
	}
	if strings.Contains(out, "older run") || strings.Contains(out, "older prompt") {
		t.Errorf("RenderStem leaked the other stem:\n%s", out)
	}
}
