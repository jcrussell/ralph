package prompt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

func TestOptionsValidate(t *testing.T) {
	cases := []struct {
		state string
		ok    bool
	}{
		{"clean", true},
		{"dirty", true},
		{"revert", true},
		{"review", true},
		{"start", false},
		{"done", false},
		{"failed", false},
		{"bogus", false},
	}
	for _, c := range cases {
		err := (&Options{State: c.state}).Validate()
		if c.ok && err != nil {
			t.Errorf("Validate(%q) = %v, want nil", c.state, err)
		}
		if !c.ok && err == nil {
			t.Errorf("Validate(%q) = nil, want error", c.state)
		}
		if !c.ok && err != nil {
			var fe *cmdutil.FlagError
			if !errors.As(err, &fe) {
				t.Errorf("Validate(%q) returned non-FlagError: %v", c.state, err)
			}
		}
	}
}

func TestShowRunRendersTemplate(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".ralph", "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "clean.md"), []byte("iter={{.Iter}} gate={{.GateResult}}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	io, bufs := iostreams.Test()
	repoRoot := func() (string, error) { return repo, nil }
	f := &cmdutil.Factory{
		IOStreams: io,
		RepoRoot:  repoRoot,
		Config:    cmdutil.LazyConfig(repoRoot),
	}
	opts := &Options{
		F:          f,
		State:      "clean",
		Iter:       42,
		GateResult: GateResultPassed,
	}
	if err := showRun(context.Background(), opts); err != nil {
		t.Fatalf("showRun: %v", err)
	}
	got := strings.TrimSpace(bufs.Out.String())
	if got != "iter=42 gate=passed" {
		t.Errorf("rendered = %q, want %q", got, "iter=42 gate=passed")
	}
}

func TestShowRunUsesHeaderFooter(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".ralph", "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	files := map[string]string{
		"_header.md": "HDR",
		"clean.md":   "BODY",
		"_footer.md": "FTR",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	io, bufs := iostreams.Test()
	repoRoot := func() (string, error) { return repo, nil }
	f := &cmdutil.Factory{
		IOStreams: io,
		RepoRoot:  repoRoot,
		Config:    cmdutil.LazyConfig(repoRoot),
	}
	opts := &Options{F: f, State: "clean"}
	if err := showRun(context.Background(), opts); err != nil {
		t.Fatalf("showRun: %v", err)
	}
	got := strings.TrimSpace(bufs.Out.String())
	// joinNonEmpty uses \n separators.
	if got != "HDR\nBODY\nFTR" {
		t.Errorf("rendered = %q, want %q", got, "HDR\nBODY\nFTR")
	}
}

func TestNewCmdPromptRunFInjection(t *testing.T) {
	called := false
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdPrompt(f, func(_ context.Context, opts *Options) error {
		called = true
		if opts.State != "clean" {
			t.Errorf("opts.State = %q, want clean", opts.State)
		}
		return nil
	})
	cmd.SetArgs([]string{"show", "clean", "--iter", "5"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !called {
		t.Errorf("runF not called")
	}
}

func TestNewCmdPromptRejectsBadState(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdPrompt(f, func(context.Context, *Options) error { return nil })
	cmd.SetArgs([]string{"show", "done"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute = nil, want FlagError")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %T %v, want *FlagError", err, err)
	}
}

func TestPromptShowCompletionRegistered(t *testing.T) {
	f := &cmdutil.Factory{IOStreams: iostreams.System()}
	cmd := NewCmdPrompt(f, nil)
	show, _, err := cmd.Find([]string{"show"})
	if err != nil {
		t.Fatalf("find show: %v", err)
	}
	wantStates := map[string]bool{"clean": true, "dirty": true, "revert": true, "review": true}
	gotStates := map[string]bool{}
	for _, a := range show.ValidArgs {
		gotStates[a] = true
	}
	for s := range wantStates {
		if !gotStates[s] {
			t.Errorf("ValidArgs missing %q (got %v)", s, show.ValidArgs)
		}
	}
	fn, ok := show.GetFlagCompletionFunc("gate-result")
	if !ok {
		t.Fatal("no completion func for --gate-result")
	}
	got, _ := fn(show, nil, "")
	want := []string{"passed", "failed", "not-run"}
	if len(got) != len(want) {
		t.Fatalf("--gate-result got %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("--gate-result [%d] = %q, want %q", i, got[i], v)
		}
	}
}
