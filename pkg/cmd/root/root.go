// Package root assembles the cobra command tree for ralph.
//
//go:generate go run ../../../tools/gendocs ../../../docs/reference
package root

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	ralphlog "github.com/jcrussell/ralph/internal/log"
	"github.com/jcrussell/ralph/internal/ralphcmd/build"
	"github.com/jcrussell/ralph/pkg/cmd/doctor"
	fsmcmd "github.com/jcrussell/ralph/pkg/cmd/fsm"
	"github.com/jcrussell/ralph/pkg/cmd/hook"
	"github.com/jcrussell/ralph/pkg/cmd/initcmd"
	"github.com/jcrussell/ralph/pkg/cmd/logs"
	"github.com/jcrussell/ralph/pkg/cmd/prompt"
	"github.com/jcrussell/ralph/pkg/cmd/report"
	reviewcmd "github.com/jcrussell/ralph/pkg/cmd/review"
	runcmd "github.com/jcrussell/ralph/pkg/cmd/run"
	"github.com/jcrussell/ralph/pkg/cmd/status"
	"github.com/jcrussell/ralph/pkg/cmd/timeline"
	"github.com/jcrussell/ralph/pkg/cmd/trace"
	"github.com/jcrussell/ralph/pkg/cmd/version"
	"github.com/jcrussell/ralph/pkg/cmdutil"
)

// envLogLevel is consulted as the lowest-priority verbosity input
// (byob-logging.3). Loses to both --log-level and -v/-vv.
const envLogLevel = "RALPH_LOG"

// NewCmdRoot returns the root cobra command for `ralph`.
func NewCmdRoot(f *cmdutil.Factory) *cobra.Command {
	var verbose int
	var logLevel string
	var logFormat string
	var logFile string

	root := &cobra.Command{
		Use:   "ralph",
		Short: "FSM-driven autonomous-loop CLI",
		Long: `ralph runs an AI coding agent in a loop, routed by a built-in
state machine. Each iteration ends in clean, dirty, revert, or one
of the terminal done{*}/failed{*} states; the next prompt is chosen
from the resulting state.

See docs/concepts/ralph-fsm.md for the state diagram and
docs/concepts/configuration.md for the .ralph/config.toml schema.`,
		Example: `  # one-time setup
  ralph doctor && ralph init

  # autonomous run against the bd queue
  ralph run

  # review an open PR
  ralph review --pr=123

  # mid-run observability
  ralph status
  ralph logs --tail`,
		Version:       build.Info().Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Application-wide middleware (byob-command-shape.5). Resolves
		// the per-invocation log level (byob-logging.3) and attaches a
		// decorated slog.Logger to cmd.Context() so leaf commands reach
		// it via log.From(ctx) (byob-logging.2).
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Precedence: --log-level > -v/-vv > $RALPH_LOG > Warn.
			// Flags beat env deliberately — a -vv on the command line
			// must not be silenced by a stale RALPH_LOG=warn.
			lvl := slog.LevelWarn
			switch {
			case logLevel != "":
				parsed, err := ralphlog.ParseLevel(logLevel)
				if err != nil {
					return &cmdutil.FlagError{Err: err}
				}
				lvl = parsed
			case verbose >= 2:
				lvl = slog.LevelDebug
			case verbose == 1:
				lvl = slog.LevelInfo
			case os.Getenv(envLogLevel) != "":
				// Invalid env value silently falls through to Warn —
				// env is the implicit channel; only flags are loud
				// about errors.
				if parsed, err := ralphlog.ParseLevel(os.Getenv(envLogLevel)); err == nil {
					lvl = parsed
				}
			}
			if f.LogLevel != nil {
				f.LogLevel.Set(lvl)
			}

			logger := f.Logger
			if logger == nil {
				logger = slog.Default()
			}
			// byob-logging.4: when the user opts into a different sink
			// or format, rebuild the logger. The new handler reuses
			// f.LogLevel so the verbosity ladder still applies; if the
			// Factory has no LevelVar (bare test factory) we fall back
			// to the resolved level as a fixed Leveler.
			if logFile != "" || logFormat != "" {
				rebuilt, err := buildLogger(f, logFile, logFormat, lvl)
				if err != nil {
					return err
				}
				logger = rebuilt
			}
			cmd.SetContext(ralphlog.WithLogger(cmd.Context(), logger.With("cmd", cmd.CommandPath())))
			return nil
		},
	}
	// Cobra's auto-generated --version flag uses the Version field above
	// and our short template — keeping the one-liner identical to the
	// first line of `ralph version` (byob-release.2).
	root.SetVersionTemplate("ralph {{.Version}}\n")
	root.PersistentFlags().CountVarP(&verbose, "verbose", "v",
		"increase log verbosity (-v=info, -vv=debug)")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "",
		"explicit log level (warn|info|debug); overrides -v")
	root.PersistentFlags().StringVar(&logFormat, "log-format", "",
		"log record format (text|json); default text")
	root.PersistentFlags().StringVar(&logFile, "log-file", "",
		"append log records to this file instead of stderr")
	// Route cobra's own help/usage/error output through IOStreams (per
	// byob-iostreams.1). Cascades to subcommands via cobra's writer lookup.
	root.SetIn(f.IOStreams.In)
	root.SetOut(f.IOStreams.Out)
	root.SetErr(f.IOStreams.ErrOut)
	// Wrap pflag's flag-parse errors as *FlagError so the top-level
	// runner maps them to exit 2. Cascades to subcommands via cobra's
	// FlagErrorFunc lookup.
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &cmdutil.FlagError{Err: err}
	})

	root.AddGroup(
		&cobra.Group{ID: "core", Title: "Core commands:"},
		&cobra.Group{ID: "obs", Title: "Observability commands:"},
		&cobra.Group{ID: "info", Title: "Info commands:"},
		&cobra.Group{ID: "setup", Title: "Setup commands:"},
	)

	addTo := func(c *cobra.Command, group string) {
		c.GroupID = group
		root.AddCommand(c)
	}

	addTo(doctor.NewCmdDoctor(f, nil), "setup")
	addTo(fsmcmd.NewCmdFSM(f, nil, nil), "obs")
	addTo(hook.NewCmdHook(f, nil), "setup")
	addTo(initcmd.NewCmdInit(f, nil), "setup")
	addTo(logs.NewCmdLogs(f, nil), "obs")
	addTo(prompt.NewCmdPrompt(f, nil), "setup")
	addTo(report.NewCmdReport(f, nil), "obs")
	addTo(reviewcmd.NewCmdReview(f, nil), "core")
	addTo(runcmd.NewCmdRun(f, nil), "core")
	addTo(status.NewCmdStatus(f, nil), "obs")
	addTo(timeline.NewCmdTimeline(f, nil), "obs")
	addTo(trace.NewCmdTrace(f, nil), "obs")
	addTo(version.NewCmdVersion(f, nil), "info")

	return root
}

// buildLogger constructs a fresh slog.Logger honoring --log-file and
// --log-format (byob-logging.4). The handler uses f.LogLevel when
// available so the verbosity ladder still applies; otherwise the
// resolved level is baked in as a fixed Leveler. The file handle
// deliberately lives for the process lifetime — a short-lived CLI
// exits and the OS reclaims it.
func buildLogger(f *cmdutil.Factory, logFile, logFormat string, lvl slog.Level) (*slog.Logger, error) {
	w := f.IOStreams.ErrOut
	if logFile != "" {
		// #nosec G304 -- --log-file is a user-supplied destination by design.
		fh, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open --log-file: %w", err)
		}
		w = fh
	}
	var leveler slog.Leveler = lvl
	if f.LogLevel != nil {
		leveler = f.LogLevel
	}
	hopts := &slog.HandlerOptions{Level: leveler}
	var h slog.Handler
	switch logFormat {
	case "", "text":
		h = slog.NewTextHandler(w, hopts)
	case "json":
		h = slog.NewJSONHandler(w, hopts)
	default:
		return nil, &cmdutil.FlagError{Err: fmt.Errorf("unknown --log-format %q (want text|json)", logFormat)}
	}
	return slog.New(h), nil
}
