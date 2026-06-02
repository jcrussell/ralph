// Package bd is a thin typed wrapper over the bd (beads) CLI used by
// the orchestrator. It exists for orchestrator convenience only —
// there is no ralph subcommand that wraps a bd surface. Users invoke
// bd directly.
//
// Every method shells out to the bd binary; we do not reimplement bd
// semantics. If bd is missing or returns garbage, the orchestrator
// surfaces the underlying error.
package bd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Client invokes the bd CLI. The zero value uses "bd" on PATH; use
// New to override the binary or working directory.
type Client struct {
	binary       string // empty -> "bd"
	dir          string // working directory; cwd of caller if empty
	excludeTypes []string
}

// Option configures a Client at construction time. Composes via the
// variadic tail of New, so callers can opt in to filters without
// disturbing the two-positional-arg shape that older call sites use.
type Option func(*Client)

// WithExcludeTypes sets a list of bd issue types to drop from every
// Ready/List/Snapshot query the orchestrator issues. The slice is
// copied; later mutation of the caller's slice is not observed.
func WithExcludeTypes(types ...string) Option {
	cp := append([]string(nil), types...)
	return func(c *Client) { c.excludeTypes = cp }
}

// New returns a Client. binary is looked up on PATH if non-absolute;
// pass an empty string for the default "bd". dir sets the cwd
// (matters because bd resolves .beads/ relative to cwd).
func New(binary, dir string, opts ...Option) *Client {
	c := &Client{binary: binary, dir: dir}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) bin() string {
	if c.binary == "" {
		return "bd"
	}
	return c.binary
}

// Issue is the slice of fields we read from `bd list/ready --json`.
type Issue struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Priority int      `json:"priority"`
	Type     string   `json:"type,omitempty"`
	Labels   []string `json:"labels,omitempty"`
}

// Ready returns issues with no blockers, optionally filtered to a
// single label (empty label = unscoped). Types passed via
// WithExcludeTypes are dropped server-side.
func (c *Client) Ready(ctx context.Context, label string) ([]Issue, error) {
	args := []string{"ready", "--json", "-n", "0"}
	if label != "" {
		args = append(args, "-l", label)
	}
	args = append(args, c.excludeFlags()...)
	return c.runList(ctx, args)
}

// List returns issues filtered by status and optionally label.
// status="" passes --all so closed/deferred/etc. are included; use a
// specific status ("open", "in_progress", "closed", …) to filter.
// Types passed via WithExcludeTypes are dropped server-side.
func (c *Client) List(ctx context.Context, status, label string) ([]Issue, error) {
	args := []string{"list", "--json", "-n", "0"}
	if status == "" {
		args = append(args, "--all")
	} else {
		args = append(args, "--status="+status)
	}
	if label != "" {
		args = append(args, "-l", label)
	}
	args = append(args, c.excludeFlags()...)
	return c.runList(ctx, args)
}

// ExcludeTypes returns the configured exclude-type filter (a copy
// so callers can't mutate the client's slice). Read-only accessor
// for diagnostics and tests; the filter is applied automatically.
func (c *Client) ExcludeTypes() []string {
	if len(c.excludeTypes) == 0 {
		return nil
	}
	out := make([]string, len(c.excludeTypes))
	copy(out, c.excludeTypes)
	return out
}

// excludeFlags renders the configured excludeTypes as repeated
// `--exclude-type <t>` argv pairs. Returns nil when empty so the
// surrounding append is a no-op.
func (c *Client) excludeFlags() []string {
	if len(c.excludeTypes) == 0 {
		return nil
	}
	out := make([]string, 0, 2*len(c.excludeTypes))
	for _, t := range c.excludeTypes {
		out = append(out, "--exclude-type", t)
	}
	return out
}

// CreateOpts is the input to Create. Fields with zero values fall back
// to bd's defaults (e.g. Type="" → "task").
type CreateOpts struct {
	Title       string
	Description string
	Type        string // task, bug, feature, decision; empty -> task
	Priority    int    // 0-4; 0 = highest
	Labels      []string
}

// Create files a new issue via `bd create --json` and returns the new
// issue ID parsed from stdout.
func (c *Client) Create(ctx context.Context, opts CreateOpts) (string, error) {
	if opts.Title == "" {
		return "", errors.New("bd: Create requires Title")
	}
	args := []string{"create", "--title", opts.Title}
	if opts.Description != "" {
		args = append(args, "--description", opts.Description)
	}
	if opts.Type != "" {
		args = append(args, "--type", opts.Type)
	}
	args = append(args, "--priority", fmt.Sprintf("%d", opts.Priority))
	for _, l := range opts.Labels {
		args = append(args, "--label", l)
	}
	args = append(args, "--json")
	out, err := c.run(ctx, args...)
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		return "", fmt.Errorf("bd: parse create output: %w", err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("bd: create returned empty id (output: %s)", strings.TrimSpace(string(out)))
	}
	return created.ID, nil
}

// Close marks the given IDs closed. Multiple IDs in one call match
// bd's batch idiom.
func (c *Client) Close(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	args := append([]string{"close"}, ids...)
	_, err := c.run(ctx, args...)
	return err
}

// Defer marks id deferred until the given when string (bd accepts
// "+6h", "tomorrow", "2026-06-01", etc.). reason is optional.
func (c *Client) Defer(ctx context.Context, id, when, reason string) error {
	args := []string{"defer", id, "--until", when}
	if reason != "" {
		args = append(args, "--reason", reason)
	}
	_, err := c.run(ctx, args...)
	return err
}

// Remember stores a persistent note under the given key (or
// auto-keyed if key=="").
func (c *Client) Remember(ctx context.Context, key, body string) error {
	args := []string{"remember"}
	if key != "" {
		args = append(args, "--key", key)
	}
	args = append(args, body)
	_, err := c.run(ctx, args...)
	return err
}

// Snapshot is a labelled list of open issues at a point in time.
// Diff produces opened/closed/created/deferred sets between two
// snapshots taken before and after an iteration.
type Snapshot struct {
	Status map[string]string
}

// Snapshot returns the current set of issues (any status) for use as
// the "before" or "after" input to Diff.
func (c *Client) Snapshot(ctx context.Context) (*Snapshot, error) {
	issues, err := c.List(ctx, "", "") // all statuses
	if err != nil {
		return nil, err
	}
	s := &Snapshot{
		Status: make(map[string]string, len(issues)),
	}
	for _, i := range issues {
		s.Status[i.ID] = i.Status
	}
	return s, nil
}

// Diff captures the bead-level deltas between two snapshots. Used by
// internal/narrative to compose the per-iteration narrative line. Each
// arm records "transitioned to X" — an issue that moved between two
// non-identity states lands in exactly one bucket, keyed by the target
// state. The closed/resolved bucket guards against intra-category
// resolved↔closed shuffles so a still-closed issue is not re-reported.
type Diff struct {
	Created    []string // present in after, absent in before
	Closed     []string // transitioned to closed/resolved from a non-closed state
	Opened     []string // transitioned to open from any other state
	Deferred   []string // transitioned to deferred
	InProgress []string // transitioned to in_progress
	Blocked    []string // transitioned to blocked
}

// IsEmpty reports whether the diff carries no transitions at all — no
// beads created, closed, opened, deferred, moved to in_progress, or
// blocked. The loop uses it to recognize a no-op iteration (combined
// with zero commits and an unchanged state).
func (d Diff) IsEmpty() bool {
	return len(d.Created) == 0 && len(d.Closed) == 0 && len(d.Opened) == 0 &&
		len(d.Deferred) == 0 && len(d.InProgress) == 0 && len(d.Blocked) == 0
}

// DiffSnapshots computes the deltas going from before to after.
func DiffSnapshots(before, after *Snapshot) Diff {
	d := Diff{}
	if before == nil {
		before = &Snapshot{Status: map[string]string{}}
	}
	if after == nil {
		after = &Snapshot{Status: map[string]string{}}
	}
	for id, st := range after.Status {
		old, existed := before.Status[id]
		if !existed {
			d.Created = append(d.Created, id)
			continue
		}
		if st == old {
			continue
		}
		switch st {
		case "open":
			d.Opened = append(d.Opened, id)
		case "in_progress":
			d.InProgress = append(d.InProgress, id)
		case "blocked":
			d.Blocked = append(d.Blocked, id)
		case "deferred":
			d.Deferred = append(d.Deferred, id)
		case "closed", "resolved":
			if !isClosed(old) {
				d.Closed = append(d.Closed, id)
			}
		}
	}
	return d
}

func isClosed(status string) bool {
	return status == "closed" || status == "resolved"
}

// run executes bd with args and returns stdout. stderr is appended
// to the error context on non-zero exit.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin(), args...) //nolint:gosec // args are package-internal literals, byob-input-validation.3
	if c.dir != "" {
		cmd.Dir = c.dir
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("bd %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes(), nil
}

// runList runs args and unmarshals stdout as a JSON array of issues.
func (c *Client) runList(ctx context.Context, args []string) ([]Issue, error) {
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 || bytes.Equal(out, []byte("null")) {
		return nil, nil
	}
	var typed []Issue
	if err := json.Unmarshal(out, &typed); err != nil {
		return nil, fmt.Errorf("bd: parse %s: %w", strings.Join(args, " "), err)
	}
	return typed, nil
}
