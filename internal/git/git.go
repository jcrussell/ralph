// Package git wraps the small set of git commands ralph needs at
// runtime: working-tree detection, hard reset on auto-revert, head-sha
// lookup, short-stat diffs for narrative composition, and branch/base
// resolution for review mode.
//
// Every operation invokes git through exec.CommandContext with a fixed
// argv (per byob-input-validation.3) — no shell, no string templating.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Clean reports whether the working tree at repo has no modified or
// staged changes. Untracked files are ignored (--untracked-files=no),
// matching the Python detect_git_state reference.
func Clean(ctx context.Context, repo string) (bool, error) {
	out, err := run(ctx, repo, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace(out)) == 0, nil
}

// Revert discards all uncommitted changes: `git checkout -- .` resets
// tracked files, then `git clean -fd` removes any untracked files and
// directories. Used by the FSM revert state.
func Revert(ctx context.Context, repo string) error {
	if _, err := run(ctx, repo, "checkout", "--", "."); err != nil {
		return err
	}
	if _, err := run(ctx, repo, "clean", "-fd"); err != nil {
		return err
	}
	return nil
}

// HeadSHA returns the full 40-char object name of HEAD.
func HeadSHA(ctx context.Context, repo string) (string, error) {
	out, err := run(ctx, repo, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CurrentBranch returns the short branch name HEAD points at. Returns
// "HEAD" when the working tree is in detached state — callers that
// need to detect detachment should compare against "HEAD".
func CurrentBranch(ctx context.Context, repo string) (string, error) {
	out, err := run(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveBase returns configured when non-empty, else "main". KISS:
// callers pass in [review] base_branch from config; we don't probe the
// remote to discover origin/HEAD because review mode is local.
func ResolveBase(_ context.Context, _ string, configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return configured, nil
	}
	return "main", nil
}

// Stat is the parsed output of `git diff --shortstat`. A zero Stat
// means no changes (git emits empty output in that case).
type Stat struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

// shortstatRE matches lines like " 2 files changed, 5 insertions(+), 3 deletions(-)".
// Insertion and deletion clauses are independent — either can be absent.
var shortstatRE = regexp.MustCompile(
	`(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?`,
)

// CountCommits returns the number of commits in the range from..to.
// Empty from is rejected; empty to defaults to HEAD. Used by the loop
// to count agent-made commits per iteration.
func CountCommits(ctx context.Context, repo, from, to string) (int, error) {
	if strings.TrimSpace(from) == "" {
		return 0, fmt.Errorf("git: CountCommits requires from")
	}
	if to == "" {
		to = "HEAD"
	}
	out, err := run(ctx, repo, "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("git: rev-list --count: parse %q: %w", strings.TrimSpace(string(out)), err)
	}
	return n, nil
}

// DiffStat returns the short-stat between two revisions. Empty from/to
// pass through to git, which then defaults to HEAD vs. the index.
func DiffStat(ctx context.Context, repo, from, to string) (Stat, error) {
	args := []string{"diff", "--shortstat"}
	if from != "" {
		args = append(args, from)
	}
	if to != "" {
		args = append(args, to)
	}
	out, err := run(ctx, repo, args...)
	if err != nil {
		return Stat{}, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return Stat{}, nil
	}
	m := shortstatRE.FindStringSubmatch(line)
	if m == nil {
		return Stat{}, fmt.Errorf("git: unrecognized shortstat line: %q", line)
	}
	return Stat{
		FilesChanged: atoiOr0(m[1]),
		Insertions:   atoiOr0(m[2]),
		Deletions:    atoiOr0(m[3]),
	}, nil
}

func atoiOr0(s string) int {
	if s == "" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

// run executes `git <args...>` in repo and returns stdout. Stderr is
// folded into the error message on failure so callers see what git
// said. Empty repo is rejected — git would otherwise inherit cwd.
func run(ctx context.Context, repo string, args ...string) ([]byte, error) {
	if repo == "" {
		return nil, fmt.Errorf("git: empty repo path")
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), repo, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
