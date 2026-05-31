package paths

import (
	"path/filepath"
	"testing"
)

// Accessors are pure path-joining rooted at repoRoot. The table pins
// the exact layout so a rename can't silently move a file out from
// under the orchestrator or a read command.
func TestPathsLayout(t *testing.T) {
	const repo = "/repo"
	p := New(repo)

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"RepoRoot", p.RepoRoot(), repo},
		{"Root", p.Root(), filepath.Join(repo, ".ralph")},
		{"Config", p.Config(), filepath.Join(repo, ".ralph", "config.toml")},
		{"PromptsDir", p.PromptsDir(), filepath.Join(repo, ".ralph", "prompts")},
		{"HooksDir", p.HooksDir(), filepath.Join(repo, ".ralph", "hooks")},
		{"StateDir", p.StateDir(), filepath.Join(repo, ".ralph", "state")},
		{"FSM", p.FSM(), filepath.Join(repo, ".ralph", "state", "fsm.json")},
		{"PidLock", p.PidLock(), filepath.Join(repo, ".ralph", "state", "pid.lock")},
		{"LogsDir", p.LogsDir(), filepath.Join(repo, ".ralph", "state", "logs")},
		{"Summary", p.Summary(), filepath.Join(repo, ".ralph", "state", "logs", "summary.jsonl")},
		{"OrchestratorLog", p.OrchestratorLog(), filepath.Join(repo, ".ralph", "state", "logs", "orchestrator.log")},
		{"RunsDir", p.RunsDir(), filepath.Join(repo, ".ralph", "state", "runs")},
		{"IncidentsDir", p.IncidentsDir(), filepath.Join(repo, ".ralph", "state", "incidents")},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s() = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// Paths performs no I/O: a non-existent root yields joined strings, not
// an error, so --help/--version pay no filesystem cost and tests stay
// hermetic.
func TestPathsNoIO(t *testing.T) {
	p := New("/does/not/exist")
	if got := p.Summary(); got != filepath.Join("/does/not/exist", ".ralph", "state", "logs", "summary.jsonl") {
		t.Errorf("Summary() = %q", got)
	}
}
