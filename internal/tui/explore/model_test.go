package explore

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jcrussell/ralph/pkg/iostreams"
)

// fakeData is an in-memory dataSource so model tests never touch disk.
type fakeData struct {
	runItems  []runItem
	incItems  []incidentItem
	iterItems []iterItem
	details   map[string]string // keyed by id/path/stem
	detailErr error
}

func (f *fakeData) runs() []runItem           { return f.runItems }
func (f *fakeData) incidents() []incidentItem { return f.incItems }
func (f *fakeData) iterations() []iterItem    { return f.iterItems }

func (f *fakeData) runDetail(id string) (string, error)        { return f.lookup(id) }
func (f *fakeData) incidentDetail(path string) (string, error) { return f.lookup(path) }
func (f *fakeData) iterDetail(stem string) (string, error)     { return f.lookup(stem) }

func (f *fakeData) lookup(key string) (string, error) {
	if f.detailErr != nil {
		return "", f.detailErr
	}
	return f.details[key], nil
}

func sampleData() *fakeData {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &fakeData{
		runItems: []runItem{{id: "20260102T030405Z", begun: when}},
		incItems: []incidentItem{{name: "111-revert.md", path: "/x/111-revert.md", kind: "revert", when: when}},
		iterItems: []iterItem{
			{stem: "iter-0001-20260102T030405Z", num: 1, when: when},
		},
		details: map[string]string{
			"20260102T030405Z":           "RUN DETAIL BODY",
			"/x/111-revert.md":           "INCIDENT DETAIL BODY",
			"iter-0001-20260102T030405Z": "ITER DETAIL BODY",
		},
	}
}

func step(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	tm, cmd := m.Update(msg)
	got, ok := tm.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", tm)
	}
	return got, cmd
}

func runeKey(r rune) tea.KeyMsg    { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func newTestModel(t *testing.T, d dataSource) model {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	ios, _ := iostreams.Test()
	return newModel(ios, d)
}

func sized(t *testing.T, d dataSource, w, h int) model {
	t.Helper()
	m, _ := step(t, newTestModel(t, d), tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

func TestViewBeforeResize(t *testing.T) {
	if got := newTestModel(t, sampleData()).View(); got != "initializing…" {
		t.Errorf("pre-resize View = %q, want placeholder", got)
	}
}

func TestStartsAtCategories(t *testing.T) {
	m := sized(t, sampleData(), 100, 24)
	if m.pane != paneCategories {
		t.Fatalf("initial pane = %v, want categories", m.pane)
	}
	v := m.View()
	for _, w := range []string{"ralph explore", "Runs", "Incidents", "Iterations"} {
		if !strings.Contains(v, w) {
			t.Errorf("categories view missing %q\n%s", w, v)
		}
	}
}

func TestDescendCategoriesToListToDetail(t *testing.T) {
	m := sized(t, sampleData(), 100, 24)

	// Enter Runs (first category) → item list.
	m, _ = step(t, m, key(tea.KeyEnter))
	if m.pane != paneList || m.cat != catRuns {
		t.Fatalf("after enter: pane=%v cat=%v, want list/Runs", m.pane, m.cat)
	}
	if !strings.Contains(m.View(), "20260102T030405Z") {
		t.Errorf("run list missing the run id\n%s", m.View())
	}
	// Breadcrumb reflects depth.
	if !strings.Contains(m.headerView(), "Runs") {
		t.Errorf("breadcrumb missing category: %q", m.headerView())
	}

	// Enter the run → detail.
	m, _ = step(t, m, key(tea.KeyEnter))
	if m.pane != paneDetail {
		t.Fatalf("after second enter: pane=%v, want detail", m.pane)
	}
	if !strings.Contains(m.View(), "RUN DETAIL BODY") {
		t.Errorf("detail view missing body\n%s", m.View())
	}

	// esc ascends back to list, then categories.
	m, _ = step(t, m, key(tea.KeyEsc))
	if m.pane != paneList {
		t.Fatalf("after esc: pane=%v, want list", m.pane)
	}
	m, _ = step(t, m, key(tea.KeyEsc))
	if m.pane != paneCategories {
		t.Fatalf("after second esc: pane=%v, want categories", m.pane)
	}
}

func TestIncidentAndIterationDetail(t *testing.T) {
	// Incidents is the second category.
	m := sized(t, sampleData(), 100, 24)
	m, _ = step(t, m, key(tea.KeyDown)) // move to Incidents
	m, _ = step(t, m, key(tea.KeyEnter))
	m, _ = step(t, m, key(tea.KeyEnter))
	if m.pane != paneDetail || !strings.Contains(m.View(), "INCIDENT DETAIL BODY") {
		t.Errorf("incident detail not shown\n%s", m.View())
	}

	// Iterations is the third category.
	m = sized(t, sampleData(), 100, 24)
	m, _ = step(t, m, key(tea.KeyDown))
	m, _ = step(t, m, key(tea.KeyDown))
	m, _ = step(t, m, key(tea.KeyEnter))
	m, _ = step(t, m, key(tea.KeyEnter))
	if m.pane != paneDetail || !strings.Contains(m.View(), "ITER DETAIL BODY") {
		t.Errorf("iteration detail not shown\n%s", m.View())
	}
}

func TestDetailLoadErrorStaysOnList(t *testing.T) {
	d := sampleData()
	d.detailErr = fmt.Errorf("boom")
	m := sized(t, d, 100, 24)
	m, _ = step(t, m, key(tea.KeyEnter)) // into Runs list
	m, _ = step(t, m, key(tea.KeyEnter)) // try to open → error
	if m.pane != paneList {
		t.Errorf("pane = %v after detail error, want to stay on list", m.pane)
	}
	if !strings.Contains(m.helpView(), "boom") {
		t.Errorf("help line missing error status: %q", m.helpView())
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []tea.KeyMsg{runeKey('q'), {Type: tea.KeyCtrlC}} {
		m := sized(t, sampleData(), 100, 24)
		got, cmd := step(t, m, k)
		if !got.quitting {
			t.Errorf("key %v did not set quitting", k)
		}
		if cmd == nil {
			t.Errorf("key %v returned nil cmd, want tea.Quit", k)
		}
	}
}

func TestDegradedViewTinyWindow(t *testing.T) {
	m := sized(t, sampleData(), 10, 3)
	// Must not panic and must render something (the body only).
	if got := m.View(); got == "" {
		t.Errorf("degraded view empty")
	}
}
