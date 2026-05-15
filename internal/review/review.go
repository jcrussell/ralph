// Package review is the small helper surface that ralph review and
// the review loop body lean on. Two responsibilities:
//
//  1. Resolve which branch/base pair the loop should run against, with
//     guard rails against reviewing the base branch itself.
//  2. Sugar over `gh pr checkout N` so `ralph review --pr N` is one
//     subprocess instead of a documentation note.
//
// The merge-bead-on-happy-path is intentionally absent here — per the
// master plan, the agent files merge:<branch> via the review prompt.
package review

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jcrussell/ralph/internal/git"
)

// Resolution captures the branch and base for one review run.
type Resolution struct {
	Branch string // the feature branch under review
	Base   string // the branch we're diffing against (origin/HEAD analog)
}

// Resolve returns the (branch, base) pair for a review run.
//
//   - branch defaults to the current checkout when empty.
//   - base defaults to configured (cfg.Review.BaseBranch) when empty,
//     else "main" — same fallback as git.ResolveBase.
//   - reviewing the base against itself is a foot-gun (the diff is
//     empty, the prompt has nothing to ingest); return an error.
//   - a detached HEAD with no explicit branch is also rejected.
func Resolve(ctx context.Context, repo, branch, baseConfig string) (Resolution, error) {
	if repo == "" {
		return Resolution{}, errors.New("review: empty repo path")
	}
	if branch == "" {
		cur, err := git.CurrentBranch(ctx, repo)
		if err != nil {
			return Resolution{}, fmt.Errorf("review: resolve branch: %w", err)
		}
		branch = strings.TrimSpace(cur)
	}
	if branch == "" || branch == "HEAD" {
		return Resolution{}, errors.New("review: detached HEAD — pass --branch")
	}

	base, err := git.ResolveBase(ctx, repo, baseConfig)
	if err != nil {
		return Resolution{}, fmt.Errorf("review: resolve base: %w", err)
	}
	if branch == base {
		return Resolution{}, fmt.Errorf("review: refusing to review %s against itself", branch)
	}
	return Resolution{Branch: branch, Base: base}, nil
}

// CheckoutPR runs `gh pr checkout N` in repo. Used by
// `ralph review --pr N` so the operator can run review against a PR
// without dropping back to the shell. Errors from gh are surfaced
// verbatim — auth failures, missing PR numbers, etc. are gh's
// territory, not ours.
func CheckoutPR(ctx context.Context, repo string, pr int) error {
	if repo == "" {
		return errors.New("review: empty repo path")
	}
	if pr <= 0 {
		return fmt.Errorf("review: invalid PR number %d", pr)
	}
	cmd := exec.CommandContext(ctx, "gh", "pr", "checkout", fmt.Sprintf("%d", pr)) //nolint:gosec // pr validated as positive int above, byob-input-validation.3
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr checkout %d in %s: %w: %s", pr, repo, err, strings.TrimSpace(string(out)))
	}
	return nil
}
