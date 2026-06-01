package tui

// program.go assembles the live-run TUI for `ralph run` (epic ralph-g3s,
// bead ralph-g3s.7): it builds the single tea.Program, wires the two event
// sources (the loop's Observer and its redirected log stream) into the one
// Send transport, and exposes the narrow seam the run orchestration
// (bead ralph-g3s.8) drives.
//
// Construction follows the ralph-g3s.1 decision exactly: render to the
// operator's real ErrOut (the TUI is chatter, byob-iostreams.3) with
// tea.WithOutput; read keys from os.Stdin with tea.WithInput; and disable
// Bubble Tea's signal handler with tea.WithoutSignalHandler() so the existing
// signal.NotifyContext in ralphcmd.Run stays the single cancellation path.
//
// The Program returns concrete (byob-interfaces.2); the run package defines
// the consumer interface it satisfies. Two seams leave the package: LoopIO
// (the inner, redirected IOStreams handed to loop.Run) and Observer (the
// metrics sink set on loop.Options). Lifecycle is Run (blocks until quit or
// Done) and Done (Send a terminal signal so Run unblocks).

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jcrussell/ralph/internal/loop"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// observer is the loop.Observer that bridges each per-iteration Snapshot into
// the program as a metricsMsg over the single Send transport (ralph-g3s.1).
// Observe must not block (loop seams.go); Send hands the Snapshot to the
// model's goroutine and returns immediately, and is a safe no-op once the
// program has quit.
type observer struct{ send sender }

func (o observer) Observe(s loop.Snapshot) { o.send.Send(metricsMsg{s: s}) }

// Program is the wired live-run TUI: a tea.Program plus the seams the run
// orchestration needs. The redirected IOStreams (LoopIO) and metrics Observer
// feed loop.Run; Run renders until quit; Done signals loop completion.
type Program struct {
	p   *tea.Program
	ios *iostreams.IOStreams // inner redirected streams handed to loop.Run
	lw  *logWriter
	obs loop.Observer
}

// New builds the live-run Program over the operator's real streams. ios is
// the outer IOStreams: its ErrOut is the render target and its stderr TTY /
// NO_COLOR state gates color (newModel). The returned Program owns a fresh
// inner IOStreams whose Out and ErrOut both tee into the log pane.
//
// extra ProgramOptions are appended after the base set (output, input, no
// signal handler) and so override them — tests pass tea.WithoutRenderer() and
// a scripted tea.WithInput() to exercise the program headlessly. Production
// callers pass none and get os.Stdin input rendered to ErrOut.
func New(ios *iostreams.IOStreams, extra ...tea.ProgramOption) *Program {
	opts := append([]tea.ProgramOption{
		tea.WithOutput(ios.ErrOut),
		tea.WithInput(os.Stdin),
		tea.WithoutSignalHandler(),
	}, extra...)

	p := tea.NewProgram(newModel(ios), opts...)
	inner, lw := newLogIOStreams(p)
	return &Program{
		p:   p,
		ios: inner,
		lw:  lw,
		obs: observer{send: p},
	}
}

// LoopIO returns the inner, redirected IOStreams to set on loop.Options.IO so
// the loop's interleaved Out/ErrOut tee flows into the log pane while its
// artifact files stay the durable sink (logwriter.go).
func (pg *Program) LoopIO() *iostreams.IOStreams { return pg.ios }

// Observer returns the loop.Observer to set on loop.Options.Observer so each
// iteration's Snapshot reaches the metrics panel.
func (pg *Program) Observer() loop.Observer { return pg.obs }

// Run starts the Bubble Tea event loop and blocks until the program quits —
// either a user q / Ctrl-C keypress or a Done() signal. On return it stops the
// log writer so a still-draining loop cannot Send into the torn-down program
// (the quit-before-cancel half of the ralph-g3s.1 ordering); the orchestration
// then cancels the loop and waits. It returns tea's run error, if any.
func (pg *Program) Run() error {
	defer pg.lw.stop()
	_, err := pg.p.Run()
	return err
}

// Done signals that loop.Run has reached its terminal outcome: it Sends a
// doneMsg, which the model folds into tea.Quit so Run unblocks. Safe to call
// after the program has already quit (Send no-ops once the program is down).
func (pg *Program) Done() { pg.p.Send(doneMsg{}) }
