// Package narrative composes the one-line per-iteration story that
// ralph timeline and ralph logs surface by default.
//
// Compose is pure: same inputs always produce the same string. The
// caller (the loop) assembles Input from the FSM transition, the bd
// snapshot diff, the runner result, and the gate hook output.
//
// Example output:
//
//	clean → clean: claimed angr-z8xa, 2 commits, closed angr-z8xa, gate green
package narrative

import (
	"fmt"
	"strings"

	"github.com/jcrussell/ralph/internal/bd"
	"github.com/jcrussell/ralph/internal/fsm"
)

// Gate labels surface as "gate <label>". Empty means "no gate ran this
// iteration"; we omit the segment.
const (
	GatePassed  = "passed"
	GateFailed  = "failed"
	GateSkipped = "skipped"
	GateNotRun  = "not-run"
)

// Input bundles everything Compose needs to render one line.
type Input struct {
	Prev    fsm.Outcome // previous routed outcome; the from-state
	Next    fsm.Outcome // newly routed outcome; the to-state
	Diff    bd.Diff     // bead-level deltas observed this iteration
	Commits int         // git commits made this iteration
	Gate    string      // one of the Gate* constants; "" omits
	// FailureMode, when set on a terminal transition into failed{},
	// adds a trailing "failure: <mode>" segment. Ignored otherwise.
	FailureMode string
}

// Compose renders in as a single-line narrative. The prefix
// "prev → next" is always present; further segments append only when
// they carry information.
func Compose(in Input) string {
	prefix := fmt.Sprintf("%s → %s", in.Prev, in.Next)

	var segs []string
	if s := joinIDs("claimed", in.Diff.InProgress); s != "" {
		segs = append(segs, s)
	}
	if in.Commits > 0 {
		segs = append(segs, plural(in.Commits, "commit"))
	}
	if s := joinIDs("closed", in.Diff.Closed); s != "" {
		segs = append(segs, s)
	}
	if s := joinIDs("opened", in.Diff.Opened); s != "" {
		segs = append(segs, s)
	}
	if s := joinIDs("created", in.Diff.Created); s != "" {
		segs = append(segs, s)
	}
	if s := joinIDs("deferred", in.Diff.Deferred); s != "" {
		segs = append(segs, s)
	}
	if g := gateLabel(in.Gate); g != "" {
		segs = append(segs, g)
	}
	if in.Next.State == fsm.StateFailed && in.FailureMode != "" {
		segs = append(segs, "failure: "+in.FailureMode)
	}

	if len(segs) == 0 {
		return prefix
	}
	return prefix + ": " + strings.Join(segs, ", ")
}

// joinIDs renders "verb <id>" for one bead, "verb <id1>, <id2>" for
// two, and "verb <id1> +N more" once the list grows past two. Empty
// list returns "".
func joinIDs(verb string, ids []string) string {
	switch len(ids) {
	case 0:
		return ""
	case 1:
		return verb + " " + ids[0]
	case 2:
		return verb + " " + ids[0] + ", " + ids[1]
	default:
		return fmt.Sprintf("%s %s +%d more", verb, ids[0], len(ids)-1)
	}
}

// plural renders "1 commit" / "2 commits".
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// gateLabel maps internal gate strings to the human form. "" returns
// "" so the segment is omitted.
func gateLabel(g string) string {
	switch g {
	case "":
		return ""
	case GatePassed:
		return "gate green"
	case GateFailed:
		return "gate red"
	case GateSkipped:
		return "gate skipped"
	case GateNotRun:
		return "gate not run"
	default:
		return "gate " + g
	}
}
