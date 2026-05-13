package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cleanRepo seeds a throwaway git repo with one commit on main so
// branch resolution returns "main". Skips when git is absent (mirrors
// the helper in internal/fsm/select_test.go but kept local to avoid
// cross-package test dependencies).
func cleanRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestResolveDefaults(t *testing.T) {
	repo := cleanRepo(t)
	runGit(t, repo, "checkout", "-q", "-b", "feat/x")

	// Branch empty + base empty -> current branch / "main".
	r, err := Resolve(context.Background(), repo, "", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Branch != "feat/x" || r.Base != "main" {
		t.Errorf("got %+v, want feat/x → main", r)
	}
}

func TestResolveExplicit(t *testing.T) {
	repo := cleanRepo(t)
	r, err := Resolve(context.Background(), repo, "feat/y", "develop")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Branch != "feat/y" || r.Base != "develop" {
		t.Errorf("got %+v, want feat/y → develop", r)
	}
}

func TestResolveRefusesBaseAgainstItself(t *testing.T) {
	repo := cleanRepo(t)
	_, err := Resolve(context.Background(), repo, "main", "main")
	if err == nil || !strings.Contains(err.Error(), "against itself") {
		t.Errorf("err = %v, want 'against itself'", err)
	}
}

func TestResolveRefusesEmptyRepo(t *testing.T) {
	if _, err := Resolve(context.Background(), "", "feat/x", "main"); err == nil {
		t.Error("nil err for empty repo, want failure")
	}
}

func TestResolveDetachedHeadIsRejected(t *testing.T) {
	repo := cleanRepo(t)
	// Detach HEAD by checking out the commit SHA directly.
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	sha := strings.TrimSpace(string(out))
	runGit(t, repo, "checkout", "-q", "--detach", sha)

	_, err = Resolve(context.Background(), repo, "", "")
	if err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Errorf("err = %v, want detached HEAD failure", err)
	}
}

// makeFakeGH writes a shell script to dir that records its argv to
// <dir>/argv and exits 0 (or non-zero if `exitCode` is non-zero,
// writing `body` to stdout). Returns the dir, suitable for prefixing
// PATH.
func makeFakeGH(t *testing.T, exitCode int, body string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		`printf '%s\n' "$@" > "` + dir + `/argv"` + "\n"
	if body != "" {
		script += "printf '%s' '" + body + "'\n"
	}
	if exitCode != 0 {
		script += "exit " + itoa(exitCode) + "\n"
	}
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	return dir
}

func itoa(n int) string {
	// stdlib's strconv is fine here, but keep the test deps minimal.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func TestCheckoutPRArgvShape(t *testing.T) {
	repo := cleanRepo(t)
	ghDir := makeFakeGH(t, 0, "")

	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := CheckoutPR(context.Background(), repo, 42); err != nil {
		t.Fatalf("CheckoutPR: %v", err)
	}
	argv, err := os.ReadFile(filepath.Join(ghDir, "argv"))
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	got := strings.TrimSpace(string(argv))
	want := "pr\ncheckout\n42"
	if got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestCheckoutPRPropagatesGHFailure(t *testing.T) {
	repo := cleanRepo(t)
	ghDir := makeFakeGH(t, 1, "no such PR")
	t.Setenv("PATH", ghDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := CheckoutPR(context.Background(), repo, 99)
	if err == nil {
		t.Fatal("nil err on gh failure, want wrapped error")
	}
	if !strings.Contains(err.Error(), "no such PR") {
		t.Errorf("err = %v, want gh stderr in message", err)
	}
}

func TestCheckoutPRRejectsBadInputs(t *testing.T) {
	if err := CheckoutPR(context.Background(), "", 1); err == nil {
		t.Error("empty repo: nil err, want failure")
	}
	if err := CheckoutPR(context.Background(), t.TempDir(), 0); err == nil {
		t.Error("pr=0: nil err, want failure")
	}
	if err := CheckoutPR(context.Background(), t.TempDir(), -3); err == nil {
		t.Error("pr<0: nil err, want failure")
	}
}
