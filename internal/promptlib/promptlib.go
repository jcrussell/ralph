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
// escape that directory (no "../" climbs) — safety against typos
// pointing at arbitrary files.
package promptlib

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
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
	Review     ReviewVars
}

// ReviewVars are the review-state fields. OpenFindings is the count
// of open bd findings labeled review:<branch>.
type ReviewVars struct {
	Branch       string
	Base         string
	OpenFindings int
}

// PromptsDir returns the .ralph/prompts directory under repoRoot.
func PromptsDir(repoRoot string) string {
	return filepath.Join(repoRoot, ".ralph", "prompts")
}

// Render reads prompts/<state>.md from repoRoot, wraps it with
// optional _header.md and _footer.md, executes it with vars, and
// returns the final string. Missing state file is a clear error;
// missing header/footer is fine.
func Render(repoRoot, state string, vars Vars) (string, error) {
	dir := PromptsDir(repoRoot)
	body, err := readRequired(filepath.Join(dir, state+".md"))
	if err != nil {
		return "", err
	}
	header, err := readOptional(filepath.Join(dir, "_header.md"))
	if err != nil {
		return "", err
	}
	footer, err := readOptional(filepath.Join(dir, "_footer.md"))
	if err != nil {
		return "", err
	}

	composed := joinNonEmpty(header, body, footer)
	t := template.New(state + ".md")
	t.Option("missingkey=error")
	t.Funcs(funcs(dir))
	if _, err := t.Parse(composed); err != nil {
		return "", fmt.Errorf("promptlib: parse %s: %w", state, err)
	}
	var out bytes.Buffer
	if err := t.Execute(&out, vars); err != nil {
		return "", fmt.Errorf("promptlib: execute %s: %w", state, err)
	}
	return out.String(), nil
}

// RenderString is like Render but reads body from the given string
// instead of from disk. Header/footer still come from repoRoot's
// prompts/ when present. Useful for `ralph prompt show` and for
// tests.
func RenderString(repoRoot, body string, vars Vars) (string, error) {
	dir := PromptsDir(repoRoot)
	header, err := readOptional(filepath.Join(dir, "_header.md"))
	if err != nil {
		return "", err
	}
	footer, err := readOptional(filepath.Join(dir, "_footer.md"))
	if err != nil {
		return "", err
	}
	composed := joinNonEmpty(header, body, footer)
	t := template.New("inline")
	t.Option("missingkey=error")
	t.Funcs(funcs(dir))
	if _, err := t.Parse(composed); err != nil {
		return "", fmt.Errorf("promptlib: parse: %w", err)
	}
	var out bytes.Buffer
	if err := t.Execute(&out, vars); err != nil {
		return "", fmt.Errorf("promptlib: execute: %w", err)
	}
	return out.String(), nil
}

func funcs(dir string) template.FuncMap {
	return template.FuncMap{
		"include": func(rel string) (string, error) {
			return includeFile(dir, rel)
		},
	}
}

// includeFile reads dir/rel and returns its content. It rejects rel
// values that resolve outside dir, including via symlinks
// (byob-input-validation.1).
func includeFile(dir, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("promptlib: include: empty path")
	}
	cleaned := filepath.Clean(rel)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("promptlib: include %q: absolute paths are not allowed", rel)
	}
	// Pre-check: reject literal `../` climbs before touching the
	// filesystem so missing targets still produce a clear escape error.
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("promptlib: include %s: resolve base: %w", rel, err)
	}
	full := filepath.Join(resolvedDir, cleaned)
	if !contained(resolvedDir, full) {
		return "", fmt.Errorf("promptlib: include %q: escapes prompts directory", rel)
	}
	// Post-check: resolve symlinks and recheck so a symlink inside dir
	// cannot escape it.
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", fmt.Errorf("promptlib: include %s: %w", rel, err)
	}
	if !contained(resolvedDir, resolved) {
		return "", fmt.Errorf("promptlib: include %q: escapes prompts directory", rel)
	}
	b, err := os.ReadFile(resolved) //nolint:gosec // resolved is contained under resolvedDir, verified above
	if err != nil {
		return "", fmt.Errorf("promptlib: include %s: %w", rel, err)
	}
	return string(b), nil
}

// contained reports whether path lies inside base. Both arguments
// must already be cleaned/absolute for the result to be meaningful.
func contained(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func readRequired(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path joined from PromptsDir(repoRoot) + state literal
	if err != nil {
		return "", fmt.Errorf("promptlib: read %s: %w", path, err)
	}
	return string(b), nil
}

func readOptional(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path joined from PromptsDir(repoRoot) + header/footer literal
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("promptlib: read %s: %w", path, err)
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
