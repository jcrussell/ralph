// Package git pins the small set of git operations ralph needs at
// runtime to a single in-process surface: working-tree detection,
// hard reset on auto-revert, head-sha lookup, branch resolution,
// commit counting, short-stat diffs, and commit log windows.
//
// All operations go through go-git. The package exists so go-git's
// API does not leak into every caller — the FSM, loop, review, and
// report packages depend only on these typed Go signatures.
package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// Clean reports whether the working tree at repo has no modified or
// staged changes. Untracked files are ignored, matching `git status
// --porcelain --untracked-files=no`.
func Clean(ctx context.Context, repo string) (bool, error) {
	r, err := open(repo)
	if err != nil {
		return false, err
	}
	wt, err := r.Worktree()
	if err != nil {
		return false, fmt.Errorf("git: worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return false, fmt.Errorf("git: status: %w", err)
	}
	for _, fs := range status {
		if fs.Staging == ggit.Untracked && fs.Worktree == ggit.Untracked {
			continue
		}
		return false, nil
	}
	return true, nil
}

// Revert discards all uncommitted changes: a hard reset to HEAD
// drops tracked changes (both staged and unstaged), then a clean
// pass removes any untracked files and directories. Used by the FSM
// revert state.
func Revert(ctx context.Context, repo string) error {
	r, err := open(repo)
	if err != nil {
		return err
	}
	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("git: worktree: %w", err)
	}
	head, err := r.Head()
	if err != nil {
		return fmt.Errorf("git: head: %w", err)
	}
	if err := wt.Reset(&ggit.ResetOptions{Mode: ggit.HardReset, Commit: head.Hash()}); err != nil {
		return fmt.Errorf("git: reset --hard: %w", err)
	}
	if err := wt.Clean(&ggit.CleanOptions{Dir: true}); err != nil {
		return fmt.Errorf("git: clean -fd: %w", err)
	}
	return nil
}

// HeadSHA returns the full 40-char object name of HEAD.
func HeadSHA(ctx context.Context, repo string) (string, error) {
	r, err := open(repo)
	if err != nil {
		return "", err
	}
	head, err := r.Head()
	if err != nil {
		return "", fmt.Errorf("git: head: %w", err)
	}
	return head.Hash().String(), nil
}

// CurrentBranch returns the short branch name HEAD points at, or
// "HEAD" when the working tree is in detached state.
func CurrentBranch(ctx context.Context, repo string) (string, error) {
	r, err := open(repo)
	if err != nil {
		return "", err
	}
	head, err := r.Head()
	if err != nil {
		return "", fmt.Errorf("git: head: %w", err)
	}
	if head.Name() == plumbing.HEAD {
		return "HEAD", nil
	}
	return head.Name().Short(), nil
}

// ResolveBase returns configured when non-empty, else "main". KISS:
// callers pass in [review] base_branch from config; we don't probe
// the remote to discover origin/HEAD because review mode is local.
func ResolveBase(_ context.Context, _ string, configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return configured, nil
	}
	return "main", nil
}

// Stat is the parsed equivalent of `git diff --shortstat`. A zero
// Stat means no changes.
type Stat struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

// CountCommits returns the number of commits reachable from `to`
// but not from `from`. Empty from is rejected; empty to defaults
// to HEAD. The implementation walks first-parent history from `to`
// until it reaches `from`, which is correct for the linear-history
// case the loop uses to count agent commits per iteration.
func CountCommits(ctx context.Context, repo, from, to string) (int, error) {
	if strings.TrimSpace(from) == "" {
		return 0, errors.New("git: CountCommits requires from")
	}
	if to == "" {
		to = "HEAD"
	}
	r, err := open(repo)
	if err != nil {
		return 0, err
	}
	fromHash, err := r.ResolveRevision(plumbing.Revision(from))
	if err != nil {
		return 0, fmt.Errorf("git: resolve %q: %w", from, err)
	}
	toHash, err := r.ResolveRevision(plumbing.Revision(to))
	if err != nil {
		return 0, fmt.Errorf("git: resolve %q: %w", to, err)
	}
	iter, err := r.Log(&ggit.LogOptions{From: *toHash})
	if err != nil {
		return 0, fmt.Errorf("git: log: %w", err)
	}
	defer iter.Close()
	count := 0
	stopErr := errors.New("stop")
	err = iter.ForEach(func(c *object.Commit) error {
		if c.Hash == *fromHash {
			return stopErr
		}
		count++
		return nil
	})
	if err != nil && !errors.Is(err, stopErr) {
		return 0, fmt.Errorf("git: walk: %w", err)
	}
	return count, nil
}

// DiffStat returns the short-stat between two revisions. Both from
// and to must be non-empty revision strings (SHAs, refs, etc).
func DiffStat(ctx context.Context, repo, from, to string) (Stat, error) {
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return Stat{}, errors.New("git: DiffStat requires from and to")
	}
	r, err := open(repo)
	if err != nil {
		return Stat{}, err
	}
	fromHash, err := r.ResolveRevision(plumbing.Revision(from))
	if err != nil {
		return Stat{}, fmt.Errorf("git: resolve %q: %w", from, err)
	}
	toHash, err := r.ResolveRevision(plumbing.Revision(to))
	if err != nil {
		return Stat{}, fmt.Errorf("git: resolve %q: %w", to, err)
	}
	fromCommit, err := r.CommitObject(*fromHash)
	if err != nil {
		return Stat{}, fmt.Errorf("git: load commit %s: %w", fromHash, err)
	}
	toCommit, err := r.CommitObject(*toHash)
	if err != nil {
		return Stat{}, fmt.Errorf("git: load commit %s: %w", toHash, err)
	}
	if fromCommit.Hash == toCommit.Hash {
		return Stat{}, nil
	}
	patch, err := fromCommit.Patch(toCommit)
	if err != nil {
		return Stat{}, fmt.Errorf("git: patch: %w", err)
	}
	out := Stat{}
	for _, s := range patch.Stats() {
		out.FilesChanged++
		out.Insertions += s.Addition
		out.Deletions += s.Deletion
	}
	return out, nil
}

// LogEntry is one non-merge commit returned by Log.
type LogEntry struct {
	ShortHash string // 7-char abbreviated hash
	Subject   string // first line of the commit message
}

// Log returns non-merge commits reachable from HEAD whose commit
// timestamp is at or after `since`, in reverse chronological order.
// Mirrors `git log --since=<since> --no-merges` for ralph report.
func Log(ctx context.Context, repo string, since time.Time) ([]LogEntry, error) {
	r, err := open(repo)
	if err != nil {
		return nil, err
	}
	iter, err := r.Log(&ggit.LogOptions{Since: &since})
	if err != nil {
		return nil, fmt.Errorf("git: log: %w", err)
	}
	defer iter.Close()
	var out []LogEntry
	err = iter.ForEach(func(c *object.Commit) error {
		if c.NumParents() > 1 {
			return nil
		}
		subject := strings.TrimSpace(c.Message)
		if i := strings.IndexByte(subject, '\n'); i >= 0 {
			subject = subject[:i]
		}
		h := c.Hash.String()
		if len(h) > 7 {
			h = h[:7]
		}
		out = append(out, LogEntry{ShortHash: h, Subject: subject})
		return nil
	})
	if err != nil && !errors.Is(err, storer.ErrStop) {
		return nil, fmt.Errorf("git: walk: %w", err)
	}
	return out, nil
}

// open loads the repository at repo. Empty path is rejected — go-git
// would otherwise inherit cwd.
func open(repo string) (*ggit.Repository, error) {
	if repo == "" {
		return nil, errors.New("git: empty repo path")
	}
	r, err := ggit.PlainOpen(repo)
	if err != nil {
		return nil, fmt.Errorf("git: open %s: %w", repo, err)
	}
	return r, nil
}
