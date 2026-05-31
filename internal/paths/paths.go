// Package paths centralizes ralph's on-disk layout. Every accessor is
// pure path-joining — no filesystem access — so Paths composes with
// fs.FS seams (byob-interfaces.3) and tests can point it at a
// t.TempDir() without touching disk.
//
// Ralph deliberately keeps all runtime state in-repo under
// <repo>/.ralph/state rather than per-OS XDG directories (see the
// runtime-directories-deviation memory). Paths adopts the
// Factory-lazy PATTERN from byob-runtime-directories.2, not its
// per-OS resolution: it is rooted at the repo, not at os.UserConfigDir.
//
// Layout:
//
//	<repo>/.ralph/                  Root      — customization tree
//	<repo>/.ralph/config.toml       Config
//	<repo>/.ralph/prompts/          PromptsDir
//	<repo>/.ralph/hooks/            HooksDir
//	<repo>/.ralph/state/            StateDir  — orchestrator runtime state
//	<repo>/.ralph/state/fsm.json    FSM
//	<repo>/.ralph/state/pid.lock    PidLock
//	<repo>/.ralph/state/logs/       LogsDir
//	<repo>/.ralph/state/logs/summary.jsonl       Summary
//	<repo>/.ralph/state/logs/orchestrator.log    OrchestratorLog
//	<repo>/.ralph/state/runs/       RunsDir
//	<repo>/.ralph/state/incidents/  IncidentsDir
package paths

import "path/filepath"

// Paths resolves ralph's directory layout under a single repository
// root. Construct one with New; on the Factory it is exposed as a lazy
// closure (cmdutil.LazyPaths) built on RepoRoot.
type Paths struct {
	repoRoot string
}

// New returns a Paths rooted at repoRoot — the directory containing the
// .ralph tree, i.e. the value of cmdutil.Factory.RepoRoot().
func New(repoRoot string) *Paths {
	return &Paths{repoRoot: repoRoot}
}

// RepoRoot returns the repository root Paths was built from.
func (p *Paths) RepoRoot() string { return p.repoRoot }

// Root is <repo>/.ralph — the customization tree (config, prompts, hooks).
func (p *Paths) Root() string { return filepath.Join(p.repoRoot, ".ralph") }

// Config is <repo>/.ralph/config.toml.
func (p *Paths) Config() string { return filepath.Join(p.Root(), "config.toml") }

// PromptsDir is <repo>/.ralph/prompts.
func (p *Paths) PromptsDir() string { return filepath.Join(p.Root(), "prompts") }

// HooksDir is <repo>/.ralph/hooks.
func (p *Paths) HooksDir() string { return filepath.Join(p.Root(), "hooks") }

// StateDir is <repo>/.ralph/state — the orchestrator's runtime state.
func (p *Paths) StateDir() string { return filepath.Join(p.Root(), "state") }

// FSM is <repo>/.ralph/state/fsm.json.
func (p *Paths) FSM() string { return filepath.Join(p.StateDir(), "fsm.json") }

// PidLock is <repo>/.ralph/state/pid.lock.
func (p *Paths) PidLock() string { return filepath.Join(p.StateDir(), "pid.lock") }

// LogsDir is <repo>/.ralph/state/logs.
func (p *Paths) LogsDir() string { return filepath.Join(p.StateDir(), "logs") }

// Summary is <repo>/.ralph/state/logs/summary.jsonl — the iteration stream.
func (p *Paths) Summary() string { return filepath.Join(p.LogsDir(), "summary.jsonl") }

// OrchestratorLog is <repo>/.ralph/state/logs/orchestrator.log.
func (p *Paths) OrchestratorLog() string { return filepath.Join(p.LogsDir(), "orchestrator.log") }

// RunsDir is <repo>/.ralph/state/runs.
func (p *Paths) RunsDir() string { return filepath.Join(p.StateDir(), "runs") }

// IncidentsDir is <repo>/.ralph/state/incidents.
func (p *Paths) IncidentsDir() string { return filepath.Join(p.StateDir(), "incidents") }
