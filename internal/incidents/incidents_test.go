package incidents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKindValid(t *testing.T) {
	known := []Kind{KindRevert, KindTerminalFailure, KindGateRegression, KindDeadStreak}
	for _, k := range known {
		if !k.Valid() {
			t.Errorf("%q.Valid() = false, want true", k)
		}
	}
	if Kind("bogus").Valid() {
		t.Errorf("Kind(bogus).Valid() = true, want false")
	}
}

func TestWriteRoundTrip(t *testing.T) {
	repo := t.TempDir()
	when := time.Date(2026, 5, 13, 15, 4, 5, 0, time.UTC)
	path, err := Write(repo, Incident{
		Kind:      KindRevert,
		Iter:      42,
		Summary:   "consecutive_dirty hit 3",
		Body:      "tail of bd_diff:\n- created xyz\n",
		IterIDs:   []string{"iter-0040", "iter-0041", "iter-0042"},
		Timestamp: when,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Filename layout.
	if !strings.HasSuffix(path, "-revert.md") {
		t.Errorf("filename does not end in -revert.md: %s", path)
	}
	if filepath.Dir(path) != Dir(repo) {
		t.Errorf("dir mismatch: got %s, want %s", filepath.Dir(path), Dir(repo))
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(b)
	want := "# revert: consecutive_dirty hit 3\n\n" +
		"- iter: 42\n" +
		"- timestamp: 2026-05-13T15:04:05Z\n" +
		"- iteration records: iter-0040, iter-0041, iter-0042\n" +
		"\ntail of bd_diff:\n- created xyz\n"
	if got != want {
		t.Errorf("markdown mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestWriteAllKinds(t *testing.T) {
	repo := t.TempDir()
	for _, k := range []Kind{KindRevert, KindTerminalFailure, KindGateRegression, KindDeadStreak} {
		_, err := Write(repo, Incident{Kind: k, Iter: 1, Summary: "x"})
		if err != nil {
			t.Errorf("Write(%s): %v", k, err)
		}
	}
	entries, err := os.ReadDir(Dir(repo))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 4 {
		t.Errorf("got %d files, want 4", len(entries))
	}
}

func TestWriteUnknownKind(t *testing.T) {
	repo := t.TempDir()
	_, err := Write(repo, Incident{Kind: "bogus"})
	if !errors.Is(err, ErrUnknownKind) {
		t.Errorf("err = %v, want errors.Is(_, ErrUnknownKind)", err)
	}
	// No file was created.
	if _, statErr := os.Stat(Dir(repo)); statErr == nil {
		// Dir may exist if a prior call created it; here it shouldn't.
		entries, _ := os.ReadDir(Dir(repo))
		if len(entries) != 0 {
			t.Errorf("incidents dir has %d entries after rejected write", len(entries))
		}
	}
}

func TestWriteEmptySummaryFallsBackToKind(t *testing.T) {
	repo := t.TempDir()
	path, err := Write(repo, Incident{Kind: KindDeadStreak, Iter: 9})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "# dead-streak: dead-streak\n") {
		t.Errorf("empty summary not fallback'd; got:\n%s", b)
	}
}

func TestWriteNoCollisionOnRapidCalls(t *testing.T) {
	// Nanosecond timestamps in the filename make collisions on the
	// same machine effectively impossible. Verify two back-to-back
	// writes don't clobber each other.
	repo := t.TempDir()
	p1, err := Write(repo, Incident{Kind: KindRevert, Summary: "a"})
	if err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	p2, err := Write(repo, Incident{Kind: KindRevert, Summary: "b"})
	if err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	if p1 == p2 {
		t.Errorf("both writes produced same path: %s", p1)
	}
	for _, p := range []string{p1, p2} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestWriteNoBodyOmitsBlankSection(t *testing.T) {
	repo := t.TempDir()
	path, err := Write(repo, Incident{Kind: KindRevert, Iter: 1, Summary: "x", Timestamp: time.Unix(0, 1).UTC()})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, _ := os.ReadFile(path)
	// Body absent → file ends at the last header line, not with a
	// trailing blank.
	if strings.HasSuffix(string(b), "\n\n") {
		t.Errorf("trailing blank line when body empty:\n%q", string(b))
	}
}
