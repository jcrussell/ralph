package explore

// program.go assembles the explore tea.Program. Construction mirrors the
// live-run TUI (internal/tui/program.go) but renders to Out — the browser
// is `ralph explore`'s deliverable, not chatter — reads keys from
// os.Stdin, runs in the alternate screen, and disables Bubble Tea's
// signal handler so ralphcmd's signal.NotifyContext stays the single
// cancellation path.

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jcrussell/ralph/internal/paths"
	"github.com/jcrussell/ralph/pkg/iostreams"
)

// Program is the wired explore TUI. New builds it over the operator's
// streams; Run blocks until the user quits.
type Program struct{ p *tea.Program }

// New builds the explore Program over .ralph/state at repo (resolved
// through p). extra ProgramOptions are appended after the base set and
// so override them — tests pass tea.WithoutRenderer() and a scripted
// tea.WithInput() to drive the program headlessly.
func New(ios *iostreams.IOStreams, repo string, p *paths.Paths, extra ...tea.ProgramOption) *Program {
	return newProgram(ios, newFSData(repo, p), extra...)
}

func newProgram(ios *iostreams.IOStreams, data dataSource, extra ...tea.ProgramOption) *Program {
	opts := append([]tea.ProgramOption{
		tea.WithOutput(ios.Out),
		tea.WithInput(os.Stdin), //nolint:forbidigo // bubbletea reads keys from the real stdin; the render target stays Out
		tea.WithoutSignalHandler(),
		tea.WithAltScreen(),
	}, extra...)
	return &Program{p: tea.NewProgram(newModel(ios, data), opts...)}
}

// Run starts the Bubble Tea event loop and blocks until the user quits
// with q / Ctrl-C. It returns tea's run error, if any.
func (pg *Program) Run() error {
	_, err := pg.p.Run()
	return err
}
