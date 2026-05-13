package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// newRepo initializes a throwaway git repo under t.TempDir() with one
// empty commit on main. Returns the repo path. Skips when git is not
// on PATH so contributors without git can still run the rest.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "test")
	runGit(t, repo, "commit", "--allow-empty", "-q", "-m", "init")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestCleanIgnoresUntracked(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	clean, err := Clean(ctx, repo)
	if err != nil || !clean {
		t.Fatalf("fresh: clean=%v err=%v", clean, err)
	}

	// Untracked file should NOT make the tree dirty per --untracked-files=no.
	write(t, filepath.Join(repo, "untracked.txt"), "x")
	clean, err = Clean(ctx, repo)
	if err != nil || !clean {
		t.Errorf("untracked-only: clean=%v err=%v (want true)", clean, err)
	}

	// Modifying a tracked file flips it dirty.
	tracked := filepath.Join(repo, "tracked.txt")
	write(t, tracked, "v1")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-q", "-m", "track")
	write(t, tracked, "v2")
	clean, err = Clean(ctx, repo)
	if err != nil || clean {
		t.Errorf("modified tracked: clean=%v err=%v (want false)", clean, err)
	}
}

func TestRevertResetsTrackedAndRemovesUntracked(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	// Seed: commit a tracked file.
	tracked := filepath.Join(repo, "tracked.txt")
	write(t, tracked, "v1")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, "commit", "-q", "-m", "track")

	// Modify tracked + drop an untracked file.
	write(t, tracked, "v2-DIRTY")
	write(t, filepath.Join(repo, "untracked.txt"), "garbage")

	if err := Revert(ctx, repo); err != nil {
		t.Fatalf("Revert: %v", err)
	}

	b, err := os.ReadFile(tracked)
	if err != nil {
		t.Fatalf("read tracked: %v", err)
	}
	if string(b) != "v1" {
		t.Errorf("tracked content = %q, want %q", b, "v1")
	}
	if _, err := os.Stat(filepath.Join(repo, "untracked.txt")); !os.IsNotExist(err) {
		t.Errorf("untracked.txt still present after Revert: err=%v", err)
	}

	clean, _ := Clean(ctx, repo)
	if !clean {
		t.Errorf("after Revert: clean=false, want true")
	}
}

func TestHeadSHA(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	sha, err := HeadSHA(ctx, repo)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("HeadSHA length = %d, want 40 (got %q)", len(sha), sha)
	}
}

func TestCurrentBranch(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	br, err := CurrentBranch(ctx, repo)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if br != "main" {
		t.Errorf("CurrentBranch = %q, want %q", br, "main")
	}

	// Detached head reports "HEAD".
	sha, _ := HeadSHA(ctx, repo)
	runGit(t, repo, "checkout", "-q", "--detach", sha)
	br, _ = CurrentBranch(ctx, repo)
	if br != "HEAD" {
		t.Errorf("detached CurrentBranch = %q, want %q", br, "HEAD")
	}
}

func TestResolveBase(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		configured string
		want       string
	}{
		{"", "main"},
		{"  ", "main"},
		{"trunk", "trunk"},
		{"feat/x", "feat/x"},
	}
	for _, c := range cases {
		got, err := ResolveBase(ctx, "", c.configured)
		if err != nil {
			t.Errorf("ResolveBase(%q): %v", c.configured, err)
		}
		if got != c.want {
			t.Errorf("ResolveBase(%q) = %q, want %q", c.configured, got, c.want)
		}
	}
}

func TestDiffStatParses(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	// First commit with content.
	a := filepath.Join(repo, "a.txt")
	write(t, a, "one\ntwo\nthree\n")
	runGit(t, repo, "add", "a.txt")
	runGit(t, repo, "commit", "-q", "-m", "a")
	from, _ := HeadSHA(ctx, repo)

	// Modify: 2 insertions, 1 deletion across one file.
	write(t, a, "one\nTWO-EDITED\nthree\nfour\n")
	runGit(t, repo, "add", "a.txt")
	runGit(t, repo, "commit", "-q", "-m", "edit")
	to, _ := HeadSHA(ctx, repo)

	got, err := DiffStat(ctx, repo, from, to)
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}
	want := Stat{FilesChanged: 1, Insertions: 2, Deletions: 1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("DiffStat mismatch (-want +got):\n%s", diff)
	}
}

func TestDiffStatEmptyWhenNoChanges(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	head, _ := HeadSHA(ctx, repo)
	got, err := DiffStat(ctx, repo, head, head)
	if err != nil {
		t.Fatalf("DiffStat: %v", err)
	}
	if (got != Stat{}) {
		t.Errorf("empty diff: got = %+v, want zero Stat", got)
	}
}

func TestRunErrorIncludesStderr(t *testing.T) {
	// Run against a non-git directory.
	tmp := t.TempDir()
	_, err := HeadSHA(context.Background(), tmp)
	if err == nil {
		t.Fatal("HeadSHA on non-repo: nil err, want failure")
	}
	if !strings.Contains(err.Error(), "git") {
		t.Errorf("error does not mention git: %v", err)
	}
}

func TestRunRejectsEmptyRepo(t *testing.T) {
	_, err := HeadSHA(context.Background(), "")
	if err == nil {
		t.Fatal("HeadSHA(\"\"): nil err, want failure")
	}
}
