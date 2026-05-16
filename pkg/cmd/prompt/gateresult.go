package prompt

import "fmt"

// GateResultFlag is a pflag.Value implementation for --gate-result
// (byob-command-shape.7). The flag is constrained at parse time to the
// three user-facing outcomes the prompt template recognizes; cobra
// rejects any other value with a *cmdutil.FlagError -> exit 2 before
// RunE fires. The runner's internal "skipped" / "" sentinels are not
// surfaced here because no user invokes `prompt show` to preview them.
type GateResultFlag string

// The three user-facing outcomes accepted at the --gate-result surface.
const (
	GateResultPassed GateResultFlag = "passed"
	GateResultFailed GateResultFlag = "failed"
	GateResultNotRun GateResultFlag = "not-run"
)

func (g *GateResultFlag) String() string { return string(*g) }

// Type is the pflag.Value type name shown in --help.
func (*GateResultFlag) Type() string { return "gate-result" }

// Set implements pflag.Value, rejecting any value outside the
// recognized enum.
func (g *GateResultFlag) Set(s string) error {
	switch GateResultFlag(s) {
	case GateResultPassed, GateResultFailed, GateResultNotRun:
		*g = GateResultFlag(s)
		return nil
	}
	return fmt.Errorf("must be one of passed|failed|not-run")
}
