package bd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// requireBD finds bd on PATH and skips the test if it's not present.
func requireBD(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("bd")
	if err != nil {
		t.Skipf("bd not on PATH: %v", err)
	}
	return p
}

// newBDRepo seeds an isolated .beads/ in a temp dir so tests don't
// touch the host project's bd state.
func newBDRepo(t *testing.T) string {
	t.Helper()
	bin := requireBD(t)
	dir := t.TempDir()
	cmd := exec.Command(bin, "init", "--prefix", "test")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("bd init failed (skipping integration tests): %v\n%s", err, out)
	}
	return dir
}

func TestClientListEmpty(t *testing.T) {
	dir := newBDRepo(t)
	c := New("", dir)
	issues, err := c.List(context.Background(), "open", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("List() = %d issues, want 0 (fresh repo)", len(issues))
	}
}

func TestClientCreateAndCloseRoundTrip(t *testing.T) {
	dir := newBDRepo(t)
	c := New("", dir)
	ctx := context.Background()

	id, err := c.Create(ctx, CreateOpts{
		Title:       "test-issue-from-go",
		Description: "Created by internal/bd test",
		Type:        "task",
		Priority:    2,
		Labels:      []string{"test", "go-wrapper"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("Create returned empty ID")
	}

	issues, err := c.List(ctx, "open", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(issues) != 1 || issues[0].ID != id {
		t.Errorf("List() = %v, want one issue %s", issues, id)
	}
	if issues[0].Title != "test-issue-from-go" {
		t.Errorf("Title = %q", issues[0].Title)
	}
	if !contains(issues[0].Labels, "test") {
		t.Errorf("labels = %v, want to contain 'test'", issues[0].Labels)
	}

	if cerr := c.Close(ctx, id); cerr != nil {
		t.Fatalf("Close: %v", cerr)
	}
	open, err := c.List(ctx, "open", "")
	if err != nil {
		t.Fatalf("List after close: %v", err)
	}
	if len(open) != 0 {
		t.Errorf("after close, open = %v, want empty", open)
	}
}

func TestClientReadyHonorsLabel(t *testing.T) {
	dir := newBDRepo(t)
	c := New("", dir)
	ctx := context.Background()
	if _, err := c.Create(ctx, CreateOpts{Title: "A", Type: "task", Priority: 2, Labels: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Create(ctx, CreateOpts{Title: "B", Type: "task", Priority: 2, Labels: []string{"beta"}}); err != nil {
		t.Fatal(err)
	}

	all, err := c.Ready(ctx, "")
	if err != nil {
		t.Fatalf("Ready unscoped: %v", err)
	}
	if len(all) < 2 {
		t.Errorf("Ready unscoped = %d, want at least 2", len(all))
	}

	alpha, err := c.Ready(ctx, "alpha")
	if err != nil {
		t.Fatalf("Ready alpha: %v", err)
	}
	if len(alpha) != 1 {
		t.Errorf("Ready(alpha) = %d, want 1", len(alpha))
	}
	if len(alpha) > 0 && !contains(alpha[0].Labels, "alpha") {
		t.Errorf("Ready(alpha) returned wrong issue: %v", alpha[0])
	}
}

func TestSnapshotAndDiff(t *testing.T) {
	dir := newBDRepo(t)
	c := New("", dir)
	ctx := context.Background()

	idA, err := c.Create(ctx, CreateOpts{Title: "A", Type: "task", Priority: 2})
	if err != nil {
		t.Fatal(err)
	}
	idB, err := c.Create(ctx, CreateOpts{Title: "B", Type: "task", Priority: 2})
	if err != nil {
		t.Fatal(err)
	}

	before, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot before: %v", err)
	}
	if _, ok := before.Status[idA]; !ok {
		t.Errorf("before missing %s", idA)
	}

	// Mutate: close A, create C.
	if cerr := c.Close(ctx, idA); cerr != nil {
		t.Fatal(cerr)
	}
	idC, err := c.Create(ctx, CreateOpts{Title: "C", Type: "task", Priority: 2})
	if err != nil {
		t.Fatal(err)
	}

	after, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot after: %v", err)
	}

	d := DiffSnapshots(before, after)
	sort.Strings(d.Created)
	sort.Strings(d.Closed)

	if diff := cmp.Diff([]string{idC}, d.Created); diff != "" {
		t.Errorf("Created mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{idA}, d.Closed); diff != "" {
		t.Errorf("Closed mismatch (-want +got):\n%s", diff)
	}
	if len(d.Opened) != 0 {
		t.Errorf("Opened = %v, want empty", d.Opened)
	}
	if _, ok := after.Status[idB]; !ok {
		t.Errorf("after missing %s", idB)
	}
}

func TestDiffNilSnapshots(t *testing.T) {
	d := DiffSnapshots(nil, nil)
	if len(d.Created)+len(d.Closed)+len(d.Opened) != 0 {
		t.Errorf("Diff(nil, nil) = %+v, want empty", d)
	}
}

// TestDiffSnapshotsTransitions exercises the switch directly against
// every status pair we care about, including the cases that previously
// fell through (in_progress→blocked, deferred/blocked→open, etc.).
func TestDiffSnapshotsTransitions(t *testing.T) {
	cases := []struct {
		name   string
		before map[string]string
		after  map[string]string
		want   Diff
	}{
		{
			name:   "in_progress to blocked",
			before: map[string]string{"x": "in_progress"},
			after:  map[string]string{"x": "blocked"},
			want:   Diff{Blocked: []string{"x"}},
		},
		{
			name:   "deferred to open",
			before: map[string]string{"x": "deferred"},
			after:  map[string]string{"x": "open"},
			want:   Diff{Opened: []string{"x"}},
		},
		{
			name:   "blocked to open",
			before: map[string]string{"x": "blocked"},
			after:  map[string]string{"x": "open"},
			want:   Diff{Opened: []string{"x"}},
		},
		{
			name:   "open to closed",
			before: map[string]string{"x": "open"},
			after:  map[string]string{"x": "closed"},
			want:   Diff{Closed: []string{"x"}},
		},
		{
			name:   "closed to open",
			before: map[string]string{"x": "closed"},
			after:  map[string]string{"x": "open"},
			want:   Diff{Opened: []string{"x"}},
		},
		{
			name:   "resolved to closed is intra-category and silent",
			before: map[string]string{"x": "resolved"},
			after:  map[string]string{"x": "closed"},
			want:   Diff{},
		},
		{
			name:   "open to in_progress",
			before: map[string]string{"x": "open"},
			after:  map[string]string{"x": "in_progress"},
			want:   Diff{InProgress: []string{"x"}},
		},
		{
			name:   "open to deferred",
			before: map[string]string{"x": "open"},
			after:  map[string]string{"x": "deferred"},
			want:   Diff{Deferred: []string{"x"}},
		},
		{
			name:   "absent before is Created",
			before: map[string]string{},
			after:  map[string]string{"x": "open"},
			want:   Diff{Created: []string{"x"}},
		},
		{
			name:   "no change is silent",
			before: map[string]string{"x": "open"},
			after:  map[string]string{"x": "open"},
			want:   Diff{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiffSnapshots(&Snapshot{Status: c.before}, &Snapshot{Status: c.after})
			if diff := cmp.Diff(c.want, got); diff != "" {
				t.Errorf("DiffSnapshots mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunReturnsErrorWithStderr(t *testing.T) {
	dir := t.TempDir() // no .beads here
	c := New("", dir)
	_, err := c.List(context.Background(), "open", "")
	if err == nil {
		t.Fatal("expected error in dir with no .beads/")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "bd ") {
		t.Errorf("err missing bd context: %v", err)
	}
}

// Sanity check that the local working directory (where this test
// process was started) is irrelevant.
func TestClientUsesDirNotCWD(t *testing.T) {
	dir := newBDRepo(t)
	// Move our process to an unrelated dir to prove c.dir wins.
	wd, _ := os.Getwd()
	tmpHome := t.TempDir()
	if err := os.Chdir(tmpHome); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	c := New("", dir)
	id, err := c.Create(context.Background(), CreateOpts{Title: "cwd-test", Type: "task", Priority: 2})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("empty id")
	}
	// Confirm by listing through the same client.
	issues, _ := c.List(context.Background(), "open", "")
	if len(issues) != 1 {
		t.Errorf("List = %d, want 1", len(issues))
	}
	_ = filepath.Join(tmpHome, "ignored")
}

// TestCreateRejectsEmptyID points a Client at a fake "bd" binary that
// returns valid JSON with an empty id field. The Create call must
// surface an error instead of returning ("", nil).
func TestCreateRejectsEmptyID(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "bd")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"id\": \"\", \"title\": \"x\"}'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := New(fake, dir)
	id, err := c.Create(context.Background(), CreateOpts{Title: "x", Priority: 3})
	if err == nil {
		t.Fatalf("Create with empty-id stdout: got id=%q err=nil, want error", id)
	}
	if id != "" {
		t.Errorf("id on error = %q, want empty", id)
	}
	if !strings.Contains(err.Error(), "empty id") {
		t.Errorf("err = %v, want to mention empty id", err)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
