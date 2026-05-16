package root

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	ralphlog "github.com/jcrussell/ralph/internal/log"
	"github.com/jcrussell/ralph/internal/ralphcmd/build"
	"github.com/jcrussell/ralph/pkg/cmdutil"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// Flag-parse errors must surface as *cmdutil.FlagError so the top-level
// runner maps them to exit 2 instead of the generic exit-1 path.
func TestRootWrapsFlagParseErrorAsFlagError(t *testing.T) {
	ios, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdRoot(f)
	cmd.SetArgs([]string{"--definitely-not-a-real-flag"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute(): want error for unknown flag, got nil")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v (%T); want errors.As(_, **FlagError)", err, err)
	}
}

// Cobra's auto-generated help text must flow through IOStreams.Out, not
// straight to os.Stdout (byob-iostreams.1).
func TestRootHelpWritesToIOStreamsOut(t *testing.T) {
	ios, bufs := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	cmd := NewCmdRoot(f)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute(--help): %v", err)
	}
	if got := bufs.Out.String(); !strings.Contains(got, "ralph") {
		t.Errorf("help output missing 'ralph' on IOStreams.Out; got %q", got)
	}
}

// PersistentPreRunE must attach a per-command slog.Logger to
// cmd.Context() (byob-logging.2). The leaf command's RunE pulls the
// logger off the context via log.From(ctx) and emits a record; the
// captured handler output should carry the "cmd" attribute decorated by
// the root.
func TestRootAttachesLoggerWithCmdAttr(t *testing.T) {
	ios, _ := iostreams.Test()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	f := &cmdutil.Factory{IOStreams: ios, Logger: logger}

	root := NewCmdRoot(f)
	probe := &cobra.Command{
		Use: "probe",
		RunE: func(c *cobra.Command, _ []string) error {
			ralphlog.From(c.Context()).InfoContext(c.Context(), "probed")
			return nil
		},
	}
	root.AddCommand(probe)
	root.SetArgs([]string{"probe"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "probed") {
		t.Errorf("log missing probe message; got %q", got)
	}
	if !strings.Contains(got, `cmd="ralph probe"`) {
		t.Errorf("log missing cmd=\"ralph probe\" attr; got %q", got)
	}
}

// The verbosity ladder (byob-logging.3) resolves the per-invocation
// level by flipping f.LogLevel; precedence is --log-level > -v/-vv >
// $RALPH_LOG > Warn (default). Each case constructs a fresh Factory so
// the LevelVar state is independent.
func TestRootVerbosityLadder(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  string
		want slog.Level
	}{
		{name: "default is warn", args: []string{"probe"}, want: slog.LevelWarn},
		{name: "-v promotes to info", args: []string{"-v", "probe"}, want: slog.LevelInfo},
		{name: "-vv promotes to debug", args: []string{"-vv", "probe"}, want: slog.LevelDebug},
		{name: "--log-level=debug", args: []string{"--log-level=debug", "probe"}, want: slog.LevelDebug},
		{name: "--log-level beats -vv", args: []string{"-vv", "--log-level=warn", "probe"}, want: slog.LevelWarn},
		{name: "env sets info", args: []string{"probe"}, env: "info", want: slog.LevelInfo},
		{name: "-v beats env", args: []string{"-v", "probe"}, env: "warn", want: slog.LevelInfo},
		{name: "invalid env falls back to warn", args: []string{"probe"}, env: "shouting", want: slog.LevelWarn},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("RALPH_LOG", c.env)
			ios, _ := iostreams.Test()
			lvl := new(slog.LevelVar)
			lvl.Set(slog.LevelError) // sentinel — must be overwritten
			f := &cmdutil.Factory{
				IOStreams: ios,
				Logger:    slog.New(slog.NewTextHandler(ios.ErrOut, &slog.HandlerOptions{Level: lvl})),
				LogLevel:  lvl,
			}
			root := NewCmdRoot(f)
			root.AddCommand(&cobra.Command{
				Use:  "probe",
				RunE: func(*cobra.Command, []string) error { return nil },
			})
			root.SetArgs(c.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got := lvl.Level(); got != c.want {
				t.Errorf("LogLevel = %v; want %v", got, c.want)
			}
		})
	}
}

// An unparseable --log-level value must surface as a *FlagError so the
// top-level runner maps it to exit 2.
func TestRootInvalidLogLevelIsFlagError(t *testing.T) {
	t.Setenv("RALPH_LOG", "")
	ios, _ := iostreams.Test()
	lvl := new(slog.LevelVar)
	f := &cmdutil.Factory{
		IOStreams: ios,
		Logger:    slog.New(slog.NewTextHandler(ios.ErrOut, &slog.HandlerOptions{Level: lvl})),
		LogLevel:  lvl,
	}
	root := NewCmdRoot(f)
	root.AddCommand(&cobra.Command{
		Use:  "probe",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.SetArgs([]string{"--log-level=shouting", "probe"})

	err := root.Execute()
	if err == nil {
		t.Fatal("Execute: want error for invalid --log-level, got nil")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v (%T); want errors.As(_, **FlagError)", err, err)
	}
}

// A nil LogLevel field (bare Factory in older tests) must not panic the
// verbosity wiring.
func TestRootVerbositySurvivesNilLogLevel(t *testing.T) {
	t.Setenv("RALPH_LOG", "")
	ios, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	root := NewCmdRoot(f)
	root.AddCommand(&cobra.Command{
		Use:  "probe",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.SetArgs([]string{"-vv", "probe"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

// byob-logging.4: default invocation (no --log-file, no --log-format)
// must leave the Factory's logger untouched — chatter and logs share
// ErrOut, but the Warn default keeps logs silent in the common case.
func TestRootDefaultKeepsFactoryLogger(t *testing.T) {
	t.Setenv("RALPH_LOG", "")
	ios, _ := iostreams.Test()
	var buf bytes.Buffer
	lvl := new(slog.LevelVar)
	original := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: lvl}))
	f := &cmdutil.Factory{IOStreams: ios, Logger: original, LogLevel: lvl}

	root := NewCmdRoot(f)
	root.AddCommand(&cobra.Command{
		Use: "probe",
		RunE: func(c *cobra.Command, _ []string) error {
			ralphlog.From(c.Context()).Warn("probed")
			return nil
		},
	})
	root.SetArgs([]string{"probe"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "probed") {
		t.Errorf("default logger did not receive record; got %q", got)
	}
}

// byob-logging.4: --log-format=json swaps the handler to JSONHandler.
// Records reach the Factory's ErrOut sink as one JSON object per line.
func TestRootLogFormatJSON(t *testing.T) {
	t.Setenv("RALPH_LOG", "")
	ios, bufs := iostreams.Test()
	lvl := new(slog.LevelVar)
	f := &cmdutil.Factory{
		IOStreams: ios,
		Logger:    slog.New(slog.NewTextHandler(ios.ErrOut, &slog.HandlerOptions{Level: lvl})),
		LogLevel:  lvl,
	}
	root := NewCmdRoot(f)
	root.AddCommand(&cobra.Command{
		Use: "probe",
		RunE: func(c *cobra.Command, _ []string) error {
			ralphlog.From(c.Context()).Warn("probed")
			return nil
		},
	})
	root.SetArgs([]string{"--log-format=json", "probe"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := bufs.ErrOut.String()
	if !strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Errorf("--log-format=json: stderr does not look like JSON; got %q", got)
	}
	if !strings.Contains(got, `"msg":"probed"`) {
		t.Errorf("--log-format=json: missing msg field; got %q", got)
	}
}

// byob-logging.4: --log-file diverts records to a file, leaving ErrOut
// to chatter alone. The level wiring must still honor the verbosity
// ladder so -v / --log-level keep working through the new sink.
func TestRootLogFileDivertsRecords(t *testing.T) {
	t.Setenv("RALPH_LOG", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "ralph.log")

	ios, bufs := iostreams.Test()
	lvl := new(slog.LevelVar)
	f := &cmdutil.Factory{
		IOStreams: ios,
		Logger:    slog.New(slog.NewTextHandler(ios.ErrOut, &slog.HandlerOptions{Level: lvl})),
		LogLevel:  lvl,
	}
	root := NewCmdRoot(f)
	root.AddCommand(&cobra.Command{
		Use: "probe",
		RunE: func(c *cobra.Command, _ []string) error {
			ralphlog.From(c.Context()).Info("probed")
			return nil
		},
	})
	root.SetArgs([]string{"-v", "--log-file=" + path, "probe"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := bufs.ErrOut.String(); strings.Contains(got, "probed") {
		t.Errorf("--log-file: record leaked onto ErrOut: %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "probed") {
		t.Errorf("--log-file: file missing record; got %q", string(data))
	}
}

// An unknown --log-format value must surface as a *FlagError so the
// top-level runner maps it to exit 2.
func TestRootLogFormatInvalidIsFlagError(t *testing.T) {
	t.Setenv("RALPH_LOG", "")
	ios, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	root := NewCmdRoot(f)
	root.AddCommand(&cobra.Command{
		Use:  "probe",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.SetArgs([]string{"--log-format=yaml", "probe"})

	err := root.Execute()
	if err == nil {
		t.Fatal("Execute: want error for invalid --log-format, got nil")
	}
	var fe *cmdutil.FlagError
	if !errors.As(err, &fe) {
		t.Errorf("err = %v (%T); want *FlagError", err, err)
	}
}

// --log-file with an unwritable path (parent does not exist) returns a
// plain error — not a *FlagError. The flag itself parsed cleanly; the
// problem is a runtime filesystem condition.
func TestRootLogFileUnopenableReturnsError(t *testing.T) {
	t.Setenv("RALPH_LOG", "")
	ios, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	root := NewCmdRoot(f)
	root.AddCommand(&cobra.Command{
		Use:  "probe",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	root.SetArgs([]string{"--log-file=/no/such/dir/ralph.log", "probe"})

	err := root.Execute()
	if err == nil {
		t.Fatal("Execute: want error for unopenable --log-file, got nil")
	}
	var fe *cmdutil.FlagError
	if errors.As(err, &fe) {
		t.Errorf("err = %v (%T); want plain error, got *FlagError", err, err)
	}
}

// byob-release.2: `ralph --version` must print the short one-liner —
// `ralph <version>\n` — to Out, not the multi-line block. The flag and
// the subcommand share build.Info() but produce different shapes.
func TestRootVersionFlagPrintsShortBanner(t *testing.T) {
	ios, bufs := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	root := NewCmdRoot(f)
	root.SetArgs([]string{"--version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute(--version): %v", err)
	}
	want := "ralph " + build.Info().Version + "\n"
	if got := bufs.Out.String(); got != want {
		t.Errorf("--version Out = %q, want %q", got, want)
	}
	if bufs.ErrOut.Len() != 0 {
		t.Errorf("--version ErrOut = %q, want empty", bufs.ErrOut.String())
	}
}

// byob-command-shape.3: root declares a small set of command groups and
// every feature subcommand carries a GroupID so `ralph --help` renders
// semantically grouped. Cobra's auto-generated `help` and `completion`
// commands are exempt — they belong in "Additional Commands".
func TestRootGroupsCoverAllFeatureCommands(t *testing.T) {
	ios, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	root := NewCmdRoot(f)

	wantGroups := map[string]bool{"core": false, "obs": false, "info": false, "setup": false}
	for _, g := range root.Groups() {
		if _, ok := wantGroups[g.ID]; !ok {
			t.Errorf("unexpected group %q on root", g.ID)
			continue
		}
		wantGroups[g.ID] = true
	}
	for id, found := range wantGroups {
		if !found {
			t.Errorf("missing group %q on root", id)
		}
	}

	cobraAuto := map[string]bool{"help": true, "completion": true}
	for _, c := range root.Commands() {
		if cobraAuto[c.Name()] {
			continue
		}
		if c.GroupID == "" {
			t.Errorf("command %q has no GroupID; every feature command must opt into a group", c.Name())
		}
	}
}

// When f.Logger is nil (tests that construct a bare Factory),
// PersistentPreRunE must fall back to slog.Default() rather than panic.
func TestRootSurvivesNilLogger(t *testing.T) {
	ios, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}

	root := NewCmdRoot(f)
	probe := &cobra.Command{
		Use: "probe",
		RunE: func(c *cobra.Command, _ []string) error {
			if ralphlog.From(c.Context()) == nil {
				t.Error("From(ctx) = nil; want fallback logger")
			}
			return nil
		},
	}
	root.AddCommand(probe)
	root.SetArgs([]string{"probe"})
	root.SetContext(context.Background())

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestRootRegistersEnumFlagCompletions(t *testing.T) {
	ios, _ := iostreams.Test()
	f := &cmdutil.Factory{IOStreams: ios}
	root := NewCmdRoot(f)
	cases := map[string][]string{
		"log-level":  {"warn", "info", "debug"},
		"log-format": {"text", "json"},
	}
	for flag, want := range cases {
		fn, ok := root.GetFlagCompletionFunc(flag)
		if !ok {
			t.Errorf("no completion func for --%s", flag)
			continue
		}
		got, dir := fn(root, nil, "")
		if dir != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("--%s directive = %v, want NoFileComp", flag, dir)
		}
		if len(got) != len(want) {
			t.Errorf("--%s got %v, want %v", flag, got, want)
			continue
		}
		for i, v := range want {
			if got[i] != v {
				t.Errorf("--%s [%d] = %q, want %q", flag, i, got[i], v)
			}
		}
	}
}
