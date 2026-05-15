package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

func TestParseSinceDuration(t *testing.T) {
	got, err := parseSince("1h")
	if err != nil {
		t.Fatalf("parseSince: %v", err)
	}
	if delta := time.Since(got); delta < 50*time.Minute || delta > 70*time.Minute {
		t.Errorf("1h yielded since=%v (delta=%v)", got, delta)
	}
}

func TestParseSinceRFC3339(t *testing.T) {
	got, err := parseSince("2026-01-02T03:04:05Z")
	if err != nil {
		t.Fatalf("parseSince: %v", err)
	}
	want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseSinceInvalid(t *testing.T) {
	if _, err := parseSince("eleven days ago"); err == nil {
		t.Errorf("expected error for invalid spec")
	}
}

func TestRenderHappyPath(t *testing.T) {
	repo := t.TempDir()

	// summary.jsonl with two records: one in window, one before.
	logs := filepath.Join(repo, ".ralph/state/logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	rec1, _ := json.Marshal(map[string]any{
		"iter":      1,
		"state":     "clean",
		"timestamp": time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339),
		"bd_diff":   map[string][]string{"closed": {"a-1", "a-2"}, "created": {"a-3"}},
	})
	rec2, _ := json.Marshal(map[string]any{
		"iter":      2,
		"state":     "dirty",
		"timestamp": time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
		"bd_diff":   map[string][]string{"closed": {"old-id"}},
	})
	body := append(rec1, '\n')
	body = append(body, rec2...)
	body = append(body, '\n')
	if err := os.WriteFile(filepath.Join(logs, "summary.jsonl"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	// One run manifest.
	runDir := filepath.Join(repo, ".ralph/state/runs/r1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{
		"start":              time.Now().Add(-60 * time.Minute).UTC().Format(time.RFC3339),
		"end":                time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339),
		"iters":              5,
		"state_distribution": map[string]int{"clean": 3, "dirty": 2},
		"cost_usd":           0.42,
		"wallclock_secs":     3000,
	})
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	// One incident.
	incDir := filepath.Join(repo, ".ralph/state/incidents")
	if err := os.MkdirAll(incDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incDir, "100-revert.md"), []byte("# revert: dirty streak 3\n\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	since := time.Now().Add(-24 * time.Hour)
	if err := Render(context.Background(), repo, since, &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"# ralph report (since ",
		"## Work done",
		"closed: a-1, a-2",
		"created: a-3",
		"## Commits",
		"## Incidents",
		"100-revert.md — revert: dirty streak 3",
		"## State distribution",
		"- clean: 3",
		"- dirty: 2",
		"## Cost",
		"- iters: 5",
		"- cost: $0.4200",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing in output: %q\n---\n%s", want, out)
		}
	}
	// Out-of-window record must not appear.
	if strings.Contains(out, "old-id") {
		t.Errorf("included out-of-window bd_diff:\n%s", out)
	}
}

func TestRenderEmpty(t *testing.T) {
	repo := t.TempDir()
	var buf bytes.Buffer
	since := time.Now().Add(-1 * time.Hour)
	if err := Render(context.Background(), repo, since, &buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	// Every section header is still present (no-data placeholders).
	for _, h := range []string{"## Work done", "## Commits", "## Incidents", "## State distribution", "## Cost"} {
		if !strings.Contains(out, h) {
			t.Errorf("section %q missing in empty report", h)
		}
	}
	if !strings.Contains(out, "no bead activity") {
		t.Errorf("expected 'no bead activity' placeholder")
	}
}

func TestNewCmdReportInvalidSince(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdReport(f, func(context.Context, *Options) error { return nil })
	cmd.SetArgs([]string{"--since", "bogus"})
	err := cmd.Execute()
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v, want FlagError", err)
	}
}
