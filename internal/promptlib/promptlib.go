// Package promptlib renders ralph's per-state prompts.
//
// Layout under .ralph/prompts/:
//
//	_header.md     optional, prepended to every render
//	_footer.md     optional, appended to every render
//	<state>.md     the per-state template
//
// Templates use Go text/template with the variable set documented in
// the master plan, plus an {{include "relpath"}} helper that pastes
// another file from the same prompts/ directory. Includes cannot
// escape that directory (no "../" climbs, no symlink-out) — safety
// against typos pointing at arbitrary files.
//
// Render and RenderString take an fs.FS rooted at the prompts
// directory. Production callers use Open(repoRoot) to get an *os.Root
// whose FS() blocks symlink escapes (chroot-style). Tests can pass
// fstest.MapFS{} for hermetic fixtures.
package promptlib

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"text/template"

	"github.com/jcrussell/ralph/internal/paths"
)

// Vars are the documented template fields. Add fields here as
// downstream subsystems (loop, narrative, review) need them; never
// silently rename one.
type Vars struct {
	Iter       int
	State      string
	PrevState  string
	GitDirty   bool
	GitHead    string
	RepoRoot   string
	LastIter   any // opaque iteration record; templates access by field
	GateResult string
	// GateFailStreak is the count of consecutive gate failures
	// (FSM.ConsecutiveGateFail), reset to 0 on the first gate pass. Lets a
	// prompt back off or change tack after repeated red gates.
	GateFailStreak int
	// GateOutput is the tail of the previous iteration's gate-hook
	// stdout, populated by the loop when a gate ran. Bounded to a
	// few KB so a multi-MB perf table doesn't blow the context. Empty
	// string when no gate output is available.
	GateOutput string
	Review     ReviewVars
	Beads      BeadsVars
}

// ReviewVars are the review-state fields. OpenFindings is the count
// of open bd findings labeled review:<branch>.
type ReviewVars struct {
	Branch       string
	Base         string
	OpenFindings int
}

// BeadsVars exposes the bd query filters the orchestrator applies, so
// prompt templates can render the same flags the FSM uses internally
// (avoids the prompt showing a `bd ready` example that returns
// library beads the FSM is silently filtering out).
type BeadsVars struct {
	// ExcludeFlags is the rendered " --exclude-type=byob
	// --exclude-type=…" suffix safe to concatenate into a
	// `bd ready` / `bd list` command in a prompt. Empty string when
	// no filter is configured.
	ExcludeFlags string
}

// BeadsVarsFromExcludeTypes builds the BeadsVars for the given
// configured excludeTypes. Centralized here so the loop and the
// `ralph prompt show` command render identical flag suffixes.
func BeadsVarsFromExcludeTypes(excludeTypes []string) BeadsVars {
	if len(excludeTypes) == 0 {
		return BeadsVars{}
	}
	var b strings.Builder
	for _, t := range excludeTypes {
		b.WriteString(" --exclude-type=")
		b.WriteString(t)
	}
	return BeadsVars{ExcludeFlags: b.String()}
}

// PromptsDir returns the .ralph/prompts directory under repoRoot.
func PromptsDir(repoRoot string) string {
	return paths.New(repoRoot).PromptsDir()
}

// Open returns an *os.Root rooted at PromptsDir(repoRoot). Its FS()
// is the production fs.FS for Render — symlinks inside the directory
// cannot escape it (chroot-style containment via openat). This is
// the trust boundary for {{include}}; see path-trust-boundaries.
func Open(repoRoot string) (*os.Root, error) {
	return os.OpenRoot(PromptsDir(repoRoot))
}

// Render reads <state>.md from fsys, wraps with optional _header.md
// and _footer.md, executes it with vars, and returns the final
// string. Missing state file is a clear error; missing header/footer
// is fine.
func Render(fsys fs.FS, state string, vars Vars) (string, error) {
	body, err := readRequired(fsys, state+".md")
	if err != nil {
		return "", err
	}
	return renderComposed(fsys, state+".md", body, vars)
}

// RenderString is like Render but reads the body from the given
// string instead of from fsys. Header/footer still come from fsys
// when present. Useful for `ralph prompt show` and for tests.
func RenderString(fsys fs.FS, body string, vars Vars) (string, error) {
	return renderComposed(fsys, "inline", body, vars)
}

func renderComposed(fsys fs.FS, name, body string, vars Vars) (string, error) {
	header, err := readOptional(fsys, "_header.md")
	if err != nil {
		return "", err
	}
	footer, err := readOptional(fsys, "_footer.md")
	if err != nil {
		return "", err
	}
	composed := joinNonEmpty(header, body, footer)
	t := template.New(name)
	t.Option("missingkey=error")
	t.Funcs(funcs(fsys))
	if _, err := t.Parse(composed); err != nil {
		return "", fmt.Errorf("promptlib: parse %s: %w", name, err)
	}
	var out bytes.Buffer
	if err := t.Execute(&out, vars); err != nil {
		return "", fmt.Errorf("promptlib: execute %s: %w", name, err)
	}
	return out.String(), nil
}

func funcs(fsys fs.FS) template.FuncMap {
	return template.FuncMap{
		"include": func(rel string) (string, error) {
			return includeFile(fsys, rel)
		},
	}
}

// includeFile reads rel from fsys. fs.ValidPath rejects empty paths,
// "../" climbs, absolute paths, and "." segments — covering the
// pre-resolve containment check. Symlink-escape protection is the
// FS implementation's job: production wires Open(repoRoot).FS() so
// openat blocks escapes; tests with fstest.MapFS have no symlinks.
func includeFile(fsys fs.FS, rel string) (string, error) {
	if !fs.ValidPath(rel) {
		return "", fmt.Errorf("promptlib: include %q: escapes prompts directory", rel)
	}
	b, err := fs.ReadFile(fsys, rel)
	if err != nil {
		return "", fmt.Errorf("promptlib: include %s: %w", rel, err)
	}
	return string(b), nil
}

func readRequired(fsys fs.FS, name string) (string, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", fmt.Errorf("promptlib: read %s: %w", name, err)
	}
	return string(b), nil
}

func readOptional(fsys fs.FS, name string) (string, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("promptlib: read %s: %w", name, err)
	}
	return string(b), nil
}

func joinNonEmpty(parts ...string) string {
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, "\n")
}
