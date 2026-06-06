package explore

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jcrussell/ralph/pkg/iostreams"
)

func runDone(pg *Program) <-chan error {
	errc := make(chan error, 1)
	go func() { errc <- pg.Run() }()
	return errc
}

func waitQuit(t *testing.T, errc <-chan error) {
	t.Helper()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after quit signal")
	}
}

// TestProgramQuits drives the headless program through a resize and a
// Ctrl-C and asserts Run unblocks — the program quits cleanly. Quit now
// confirms: the first ctrl+c arms the prompt, the second forces it.
func TestProgramQuits(t *testing.T) {
	ios, _ := iostreams.Test()
	pg := newProgram(ios, sampleData(), tea.WithoutRenderer(), tea.WithInput(&bytes.Buffer{}))

	errc := runDone(pg)
	pg.p.Send(tea.WindowSizeMsg{Width: 100, Height: 24})
	pg.p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	pg.p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	waitQuit(t, errc)
}
