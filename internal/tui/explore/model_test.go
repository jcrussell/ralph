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
func (f *fakeData) iterRaw(stem string) (string, error)        { return f.lookup(stem) }

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

func TestQuitArmsConfirmation(t *testing.T) {
	for _, k := range []tea.KeyMsg{runeKey('q'), {Type: tea.KeyCtrlC}} {
		m := sized(t, sampleData(), 100, 24)
		got, cmd := step(t, m, k)
		if got.quitting {
			t.Errorf("key %v quit outright, want it to arm confirmation", k)
		}
		if !got.confirmingQuit {
			t.Errorf("key %v did not arm confirmingQuit", k)
		}
		if cmd != nil {
			t.Errorf("key %v returned a cmd, want nil while arming", k)
		}
		if !strings.Contains(got.View(), "Quit ralph explore? (y/N)") {
			t.Errorf("confirm prompt not shown\n%s", got.View())
		}
	}
}

func TestConfirmYesQuits(t *testing.T) {
	for _, k := range []tea.KeyMsg{runeKey('y'), runeKey('Y')} {
		m := sized(t, sampleData(), 100, 24)
		m, _ = step(t, m, runeKey('q')) // arm
		got, cmd := step(t, m, k)
		if !got.quitting || cmd == nil {
			t.Errorf("key %v after arming did not quit (quitting=%v cmd=%v)", k, got.quitting, cmd)
		}
	}
}

func TestConfirmCancelKeepsRunning(t *testing.T) {
	for _, k := range []tea.KeyMsg{runeKey('n'), runeKey('N'), key(tea.KeyEsc), key(tea.KeyEnter)} {
		m := sized(t, sampleData(), 100, 24)
		m, _ = step(t, m, runeKey('q')) // arm
		got, cmd := step(t, m, k)
		if got.quitting || cmd != nil {
			t.Errorf("key %v should dismiss without quitting", k)
		}
		if got.confirmingQuit {
			t.Errorf("key %v did not clear confirmingQuit", k)
		}
	}
}

func TestSecondCtrlCForceQuits(t *testing.T) {
	m := sized(t, sampleData(), 100, 24)
	m, _ = step(t, m, key(tea.KeyCtrlC)) // arm
	got, cmd := step(t, m, key(tea.KeyCtrlC))
	if !got.quitting || cmd == nil {
		t.Errorf("second ctrl+c did not force quit")
	}
}

func TestKeysSwallowedWhileConfirming(t *testing.T) {
	m := sized(t, sampleData(), 100, 24)
	m, _ = step(t, m, runeKey('q')) // arm
	got, _ := step(t, m, key(tea.KeyEnter))
	// enter dismisses (it's a cancel key), so we should be back at categories,
	// not descended into a list.
	if got.pane != paneCategories {
		t.Errorf("enter while confirming navigated, pane=%v", got.pane)
	}
}

// --- Tab / Shift+Tab navigation ---

func TestTabSwitchesCategoryInList(t *testing.T) {
	m := sized(t, sampleData(), 100, 24)
	m, _ = step(t, m, key(tea.KeyEnter)) // into Runs list
	if m.cat != catRuns {
		t.Fatalf("setup: cat=%v want Runs", m.cat)
	}
	m, _ = step(t, m, key(tea.KeyTab)) // → Incidents
	if m.pane != paneList || m.cat != catIncidents {
		t.Fatalf("after tab: pane=%v cat=%v, want list/Incidents", m.pane, m.cat)
	}
	if !strings.Contains(m.View(), "revert") {
		t.Errorf("incidents list not shown after tab\n%s", m.View())
	}
	m, _ = step(t, m, key(tea.KeyShiftTab)) // back to Runs
	if m.cat != catRuns {
		t.Errorf("after shift+tab: cat=%v, want Runs", m.cat)
	}
	// Shift+Tab from Runs wraps to Iterations.
	m, _ = step(t, m, key(tea.KeyShiftTab))
	if m.cat != catIterations {
		t.Errorf("shift+tab wrap: cat=%v, want Iterations", m.cat)
	}
}

func TestTabFlipsItemsInDetail(t *testing.T) {
	d := twoRunData()
	m := sized(t, d, 100, 24)
	m, _ = step(t, m, key(tea.KeyEnter)) // Runs list
	m, _ = step(t, m, key(tea.KeyEnter)) // open first run (run-a)
	if !strings.Contains(m.View(), "BODY A") {
		t.Fatalf("first detail not run-a\n%s", m.View())
	}
	m, _ = step(t, m, key(tea.KeyTab)) // next item → run-b
	if m.pane != paneDetail || !strings.Contains(m.View(), "BODY B") {
		t.Fatalf("after tab: pane=%v, want detail with BODY B\n%s", m.pane, m.View())
	}
	// At the last item, tab clamps (no wrap, no change).
	m2, _ := step(t, m, key(tea.KeyTab))
	if !strings.Contains(m2.View(), "BODY B") {
		t.Errorf("tab past last item should clamp\n%s", m2.View())
	}
	m, _ = step(t, m, key(tea.KeyShiftTab)) // back to run-a
	if !strings.Contains(m.View(), "BODY A") {
		t.Errorf("shift+tab did not return to run-a\n%s", m.View())
	}
}

// --- Global search ---

func typeString(t *testing.T, m model, s string) model {
	t.Helper()
	for _, r := range s {
		m, _ = step(t, m, runeKey(r))
	}
	return m
}

func TestGlobalSearchOpensBodyHit(t *testing.T) {
	m := sized(t, sampleData(), 100, 24)
	// Navigate to the Search category (4th row) and open it.
	m, _ = step(t, m, key(tea.KeyDown))
	m, _ = step(t, m, key(tea.KeyDown))
	m, _ = step(t, m, key(tea.KeyDown))
	m, _ = step(t, m, key(tea.KeyEnter))
	if !m.searching {
		t.Fatalf("opening Search did not enter search mode")
	}
	// "run" matches only the run body ("RUN DETAIL BODY").
	m = typeString(t, m, "run")
	m, _ = step(t, m, key(tea.KeyEnter))
	if m.pane != paneList || m.cat != catSearch {
		t.Fatalf("after search: pane=%v cat=%v, want list/Search", m.pane, m.cat)
	}
	if got := len(m.itemList.Items()); got != 1 {
		t.Fatalf("search results = %d, want 1", got)
	}
	// Opening the hit loads the run detail and jumps to the match.
	m, _ = step(t, m, key(tea.KeyEnter))
	if m.pane != paneDetail || !strings.Contains(m.View(), "RUN DETAIL BODY") {
		t.Errorf("search hit did not open run detail\n%s", m.View())
	}
	if len(m.findMatches) == 0 {
		t.Errorf("opening a search hit did not seed find matches")
	}
}

func TestGlobalSearchNoMatches(t *testing.T) {
	m := sized(t, sampleData(), 100, 24)
	for i := 0; i < 3; i++ {
		m, _ = step(t, m, key(tea.KeyDown))
	}
	m, _ = step(t, m, key(tea.KeyEnter))
	m = typeString(t, m, "zzzznope")
	m, _ = step(t, m, key(tea.KeyEnter))
	if len(m.itemList.Items()) != 0 {
		t.Errorf("expected no results, got %d", len(m.itemList.Items()))
	}
	if !strings.Contains(m.helpView(), "no matches") {
		t.Errorf("help line missing 'no matches': %q", m.helpView())
	}
}

func TestGlobalSearchEscCancels(t *testing.T) {
	m := sized(t, sampleData(), 100, 24)
	for i := 0; i < 3; i++ {
		m, _ = step(t, m, key(tea.KeyDown))
	}
	m, _ = step(t, m, key(tea.KeyEnter))
	m, _ = step(t, m, key(tea.KeyEsc))
	if m.searching || m.pane != paneCategories {
		t.Errorf("esc did not cancel search: searching=%v pane=%v", m.searching, m.pane)
	}
}

// --- In-page find ---

func TestInPageFindJumpsAndCycles(t *testing.T) {
	d := sampleData()
	// Build a body taller than the viewport (so YOffset can actually move),
	// with matches on lines 2 and 5.
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	lines[2] = "MARK one"
	lines[5] = "MARK two"
	d.details["iter-0001-20260102T030405Z"] = strings.Join(lines, "\n")
	// Small window → small detail viewport → large max scroll offset.
	m := sized(t, d, 100, 6)
	// Open the iteration detail (3rd category).
	m, _ = step(t, m, key(tea.KeyDown))
	m, _ = step(t, m, key(tea.KeyDown))
	m, _ = step(t, m, key(tea.KeyEnter))
	m, _ = step(t, m, key(tea.KeyEnter))

	m, _ = step(t, m, runeKey('/')) // begin find
	if !m.finding {
		t.Fatalf("/ did not enter find mode")
	}
	m = typeString(t, m, "MARK")
	m, _ = step(t, m, key(tea.KeyEnter))
	if got := m.findMatches; len(got) != 2 || got[0] != 2 || got[1] != 5 {
		t.Fatalf("findMatches = %v, want [2 5]", got)
	}
	if m.detail.YOffset != 2 {
		t.Errorf("first match YOffset = %d, want 2", m.detail.YOffset)
	}
	m, _ = step(t, m, runeKey('n')) // next match
	if m.detail.YOffset != 5 {
		t.Errorf("after n YOffset = %d, want 5", m.detail.YOffset)
	}
	m, _ = step(t, m, runeKey('n')) // wraps to first
	if m.detail.YOffset != 2 {
		t.Errorf("after wrap YOffset = %d, want 2", m.detail.YOffset)
	}
	m, _ = step(t, m, runeKey('N')) // previous wraps to last
	if m.detail.YOffset != 5 {
		t.Errorf("after N YOffset = %d, want 5", m.detail.YOffset)
	}
}

func TestInPageFindNoMatches(t *testing.T) {
	m := sized(t, sampleData(), 100, 24)
	m, _ = step(t, m, key(tea.KeyEnter)) // Runs list
	m, _ = step(t, m, key(tea.KeyEnter)) // detail
	m, _ = step(t, m, runeKey('/'))
	m = typeString(t, m, "absent")
	m, _ = step(t, m, key(tea.KeyEnter))
	if len(m.findMatches) != 0 {
		t.Errorf("findMatches = %v, want none", m.findMatches)
	}
	if !strings.Contains(m.helpView(), "no matches") {
		t.Errorf("help missing 'no matches': %q", m.helpView())
	}
}

func TestInPageFindEscCancels(t *testing.T) {
	m := sized(t, sampleData(), 100, 24)
	m, _ = step(t, m, key(tea.KeyEnter))
	m, _ = step(t, m, key(tea.KeyEnter))
	m, _ = step(t, m, runeKey('/'))
	m, _ = step(t, m, key(tea.KeyEsc))
	if m.finding {
		t.Errorf("esc did not cancel find mode")
	}
}

func TestFindCancelClearsStaleStatus(t *testing.T) {
	m := sized(t, sampleData(), 100, 24)
	m, _ = step(t, m, key(tea.KeyEnter)) // Runs list
	m, _ = step(t, m, key(tea.KeyEnter)) // detail
	// A find with no matches leaves a "no matches" status.
	m, _ = step(t, m, runeKey('/'))
	m = typeString(t, m, "absent")
	m, _ = step(t, m, key(tea.KeyEnter))
	if !strings.Contains(m.helpView(), "no matches") {
		t.Fatalf("setup: expected 'no matches' status, got %q", m.helpView())
	}
	// Opening find again and cancelling should drop the stale notice.
	m, _ = step(t, m, runeKey('/'))
	m, _ = step(t, m, key(tea.KeyEsc))
	if strings.Contains(m.helpView(), "no matches") {
		t.Errorf("esc did not clear stale status: %q", m.helpView())
	}
}

func TestRefreshKeepsFindMatchesConsistent(t *testing.T) {
	d := sampleData()
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	lines[2] = "MARK one"
	lines[5] = "MARK two"
	d.details["iter-0001-20260102T030405Z"] = strings.Join(lines, "\n")
	m := sized(t, d, 100, 6)
	m, _ = step(t, m, key(tea.KeyDown))
	m, _ = step(t, m, key(tea.KeyDown))
	m, _ = step(t, m, key(tea.KeyEnter)) // Iterations list
	m, _ = step(t, m, key(tea.KeyEnter)) // detail

	m, _ = step(t, m, runeKey('/'))
	m = typeString(t, m, "MARK")
	m, _ = step(t, m, key(tea.KeyEnter))
	if len(m.findMatches) != 2 {
		t.Fatalf("setup findMatches = %v, want 2", m.findMatches)
	}

	// The iteration's content shrinks to a single match (e.g. a rewrite) and
	// the user refreshes. findMatches must track the new content.
	d.details["iter-0001-20260102T030405Z"] = strings.Join(append([]string{"MARK only"}, lines[6:]...), "\n")
	m, _ = step(t, m, runeKey('r'))
	if len(m.findMatches) != 1 || m.findMatches[0] != 0 {
		t.Errorf("after refresh findMatches = %v, want [0]", m.findMatches)
	}
	if m.findIdx != 0 {
		t.Errorf("after refresh findIdx = %d, want 0 (clamped)", m.findIdx)
	}
	// n/N now operate on the fresh matches without going out of range.
	m, _ = step(t, m, runeKey('n'))
	if m.detail.YOffset != 0 {
		t.Errorf("n after refresh YOffset = %d, want 0", m.detail.YOffset)
	}
}

// --- Refresh ---

func TestRefreshPicksUpNewData(t *testing.T) {
	d := sampleData()
	m := sized(t, d, 100, 24)
	m, _ = step(t, m, key(tea.KeyDown))  // to Incidents
	m, _ = step(t, m, key(tea.KeyEnter)) // into Incidents list (1 item)
	if got := len(m.itemList.Items()); got != 1 {
		t.Fatalf("setup incidents = %d, want 1", got)
	}
	// A new incident lands on disk (mutate the fake) and the user refreshes.
	d.incItems = append(d.incItems, incidentItem{name: "222-stuck.md", path: "/x/222-stuck.md", kind: "stuck"})
	m, _ = step(t, m, runeKey('r'))
	if got := len(m.itemList.Items()); got != 2 {
		t.Errorf("after refresh incidents = %d, want 2", got)
	}
	if !strings.Contains(m.helpView(), "refreshed") {
		t.Errorf("help missing 'refreshed': %q", m.helpView())
	}
}

// --- Pure helpers ---

func TestFindMatches(t *testing.T) {
	content := "Foo\nbar foo\nBAZ\nfoobar"
	got := findMatches(content, "foo")
	want := []int{0, 1, 3}
	if len(got) != len(want) {
		t.Fatalf("findMatches = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("findMatches = %v, want %v", got, want)
		}
	}
	if got := findMatches(content, "   "); got != nil {
		t.Errorf("blank query = %v, want nil", got)
	}
}

func TestSearchCorpusOrderAndCategories(t *testing.T) {
	// "DETAIL" appears in every category body → one hit each, ordered
	// runs → incidents → iterations.
	hits := searchCorpus(sampleData(), "detail")
	if len(hits) != 3 {
		t.Fatalf("searchCorpus = %d hits, want 3", len(hits))
	}
	wantCats := []categoryKind{catRuns, catIncidents, catIterations}
	for i, h := range hits {
		if h.cat != wantCats[i] {
			t.Errorf("hit[%d].cat = %v, want %v", i, h.cat, wantCats[i])
		}
	}
	if got := searchCorpus(sampleData(), ""); got != nil {
		t.Errorf("empty query = %v, want nil", got)
	}
}

func twoRunData() *fakeData {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return &fakeData{
		runItems: []runItem{
			{id: "run-a", begun: when},
			{id: "run-b", begun: when},
		},
		details: map[string]string{
			"run-a": "BODY A",
			"run-b": "BODY B",
		},
	}
}

func TestDegradedViewTinyWindow(t *testing.T) {
	m := sized(t, sampleData(), 10, 3)
	// Must not panic and must render something (the body only).
	if got := m.View(); got == "" {
		t.Errorf("degraded view empty")
	}
}
