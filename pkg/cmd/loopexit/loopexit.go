// Package loopexit is the shared tail of `ralph run` and `ralph review`:
// it reports how the loop ended in one closing line on stderr and maps
// the outcome to the command's exit code (docs/reference/exit-codes.md).
package loopexit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/jcrussell/ralph/internal/fsm"
	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/internal/runs"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// ExitRefusedTerminal is the exit code when the loop refuses to resume
// from a failed{*} state without --fresh (loop.ErrTerminalState).
const ExitRefusedTerminal = 3

// line is the closing outcome record's content.
type line struct {
	out      fsm.Outcome
	exitCode int
	iter     int
	costUSD  float64
	runID    string
}

// Finish writes the closing line to w when the loop produced an outcome
// and returns the command's error: nil for exit 0, an ExitCodeError for
// a non-zero outcome or the --fresh refusal, or err unchanged for an
// infrastructure failure (which prints its own "error:" line). The line
// is written directly rather than through the level-filtered logger so
// the default warn level never drops it. logFormat is the root
// --log-format value ("json" or text).
func Finish(ctx context.Context, w io.Writer, logFormat, repo string, out fsm.Outcome, err error) error {
	if err != nil {
		if errors.Is(err, loop.ErrTerminalState) {
			// The loop already printed why; this code says "pass --fresh".
			return &cmdutil.ExitCodeError{Code: ExitRefusedTerminal}
		}
		return err
	}
	code := out.ExitCode()
	writeLine(ctx, w, logFormat, newLine(repo, out, code))
	if code != 0 {
		return &cmdutil.ExitCodeError{Code: code}
	}
	return nil
}

func newLine(repo string, out fsm.Outcome, code int) line {
	l := line{out: out, exitCode: code}
	// Totals are best-effort decoration: the outcome and code stand
	// even if the state files can't be read back.
	if repo == "" {
		return l
	}
	if st, err := fsm.Load(repo); err == nil {
		l.iter = st.Iter
		l.costUSD = st.CumulativeCostUSD
	}
	if r, err := runs.Latest(repo); err == nil {
		l.runID = r.ID()
	}
	return l
}

// writeLine emits the record through slog's own JSON handler (at Info,
// on a handler of its own, so the root logger's warn level can't drop
// it), or as one human line.
func writeLine(ctx context.Context, w io.Writer, logFormat string, l line) {
	if logFormat == "json" {
		attrs := []slog.Attr{
			slog.String("state", string(l.out.State)),
			slog.String("reason", string(l.out.Reason)),
			slog.Int("exit_code", l.exitCode),
			slog.Int("iter", l.iter),
			slog.Float64("cost_usd", l.costUSD),
		}
		if l.runID != "" {
			attrs = append(attrs, slog.String("run_id", l.runID))
		}
		slog.New(slog.NewJSONHandler(w, nil)).LogAttrs(ctx, slog.LevelInfo, "run ended", attrs...)
		return
	}
	_, _ = fmt.Fprintf(w, "ralph: run ended: %s (exit %d)\n", l.out, l.exitCode)
}
