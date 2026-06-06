package explore

// model.go is the Bubble Tea model for `ralph explore`: a read-only
// browser over .ralph/state with three top-level categories (Runs,
// Incidents, Iterations). Navigation is category → item list → detail,
// with esc/← walking back up. Unlike the live-run TUI (internal/tui) it
// is static — Init returns no ticker — and renders to stdout (the
// deliverable), so color is gated on the stdout TTY.
//
// The model is a pure state machine over messages: feed it KeyMsg /
// WindowSizeMsg and assert on View, with an in-memory dataSource for
// tests.

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jcrussell/ralph/pkg/iostreams"
)

const (
	minWidth     = 24
	minHeight    = 6
	headerHeight = 1
	helpHeight   = 1
)

// pane is the current navigation depth.
type pane int

const (
	paneCategories pane = iota
	paneList
	paneDetail
)

// categoryKind enumerates the three browsable categories.
type categoryKind int

const (
	catRuns categoryKind = iota
	catIncidents
	catIterations
	catSearch // not a stored category — drilling in runs a global full-text search
)

func (c categoryKind) String() string {
	switch c {
	case catRuns:
		return "Runs"
	case catIncidents:
		return "Incidents"
	case catIterations:
		return "Iterations"
	case catSearch:
		return "Search"
	default:
		return "?"
	}
}

// --- list rows (bubbles/list items) ---

type categoryRow struct {
	kind  categoryKind
	count int
}

func (c categoryRow) Title() string { return c.kind.String() }
func (c categoryRow) Description() string {
	if c.kind == catSearch {
		return "full-text search across all categories"
	}
	return fmt.Sprintf("%d item(s)", c.count)
}
func (c categoryRow) FilterValue() string { return c.kind.String() }

type runRow struct{ it runItem }

func (r runRow) Title() string { return r.it.id }
func (r runRow) Description() string {
	if r.it.begun.IsZero() {
		return "(unparseable id)"
	}
	return r.it.begun.Format("2006-01-02 15:04:05Z")
}
func (r runRow) FilterValue() string { return r.it.id }

type incidentRow struct{ it incidentItem }

func (r incidentRow) Title() string {
	if r.it.kind != "" {
		return r.it.kind
	}
	return r.it.name
}
func (r incidentRow) Description() string {
	if r.it.when.IsZero() {
		return r.it.name
	}
	return r.it.when.Format("2006-01-02 15:04:05Z")
}
func (r incidentRow) FilterValue() string { return r.it.name }

type iterRow struct{ it iterItem }

func (r iterRow) Title() string { return r.it.stem }
func (r iterRow) Description() string {
	if r.it.when.IsZero() {
		return fmt.Sprintf("iter %d", r.it.num)
	}
	return fmt.Sprintf("iter %d · %s", r.it.num, r.it.when.Format("2006-01-02 15:04:05Z"))
}
func (r iterRow) FilterValue() string { return r.it.stem }

// searchHitRow is a global-search result rendered in the shared itemList. It
// carries the hit's category so loadDetail can dispatch to the right loader.
type searchHitRow struct{ hit searchHit }

func (r searchHitRow) Title() string       { return r.hit.cat.String() + ": " + r.hit.title }
func (r searchHitRow) Description() string { return r.hit.snippet }
func (r searchHitRow) FilterValue() string { return r.hit.title + " " + r.hit.snippet }

// model is the package-private Bubble Tea model (value receivers, per
// the bubbletea convention).
type model struct {
	data dataSource
	r    *lipgloss.Renderer

	pane pane
	cat  categoryKind

	catList       list.Model
	itemList      list.Model
	detail        viewport.Model
	detailTitle   string
	detailContent string // raw content backing the detail viewport (for find)

	// input is the single shared text field, reused by global search and
	// in-page find — the two modes are never active at once.
	input       textinput.Model
	searching   bool   // global-search query entry (after opening Search)
	searchQuery string // query that produced the current results
	finding     bool   // in-page find query entry (in the detail pane)
	findQuery   string
	findMatches []int // 0-based line indices of matches in detailContent
	findIdx     int

	width, height  int
	ready          bool
	quitting       bool
	confirmingQuit bool   // y/N quit prompt is armed
	status         string // transient error/notice shown in the help line
}

// newModel builds the explore model over ios. explore renders to Out
// (the browser is the deliverable), so color is gated on the stdout TTY
// and NO_COLOR, with the lipgloss renderer's profile pinned to match.
func newModel(ios *iostreams.IOStreams, data dataSource) model {
	colorEnabled := ios.IsStdoutTTY() && iostreams.EnvAllowsColor()
	r := lipgloss.NewRenderer(ios.Out)
	if !colorEnabled {
		r.SetColorProfile(termenv.Ascii)
	}

	catList := list.New(categoryItems(data), list.NewDefaultDelegate(), 0, 0)
	catList.SetShowTitle(false)
	catList.SetShowStatusBar(false)
	catList.SetShowHelp(false)
	catList.SetFilteringEnabled(false) // only the four fixed rows

	itemList := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	itemList.SetShowTitle(false)
	itemList.SetShowStatusBar(false)
	itemList.SetShowHelp(false)

	ti := textinput.New()

	return model{
		data:     data,
		r:        r,
		catList:  catList,
		itemList: itemList,
		detail:   viewport.New(0, 0),
		input:    ti,
	}
}

// categoryItems builds the fixed category rows (with live counts) for catList.
// Shared by newModel and refresh so a refresh re-counts everything.
func categoryItems(d dataSource) []list.Item {
	return []list.Item{
		categoryRow{kind: catRuns, count: len(d.runs())},
		categoryRow{kind: catIncidents, count: len(d.incidents())},
		categoryRow{kind: catIterations, count: len(d.iterations())},
		categoryRow{kind: catSearch},
	}
}

// Init has no work to schedule: explore is static (no live ticker).
func (model) Init() tea.Cmd { return nil }

// Update folds one message into the model.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		m.relayout()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m.forward(msg)
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The quit confirmation owns all input while armed: only an explicit y/Y
	// (or a second ctrl+c) quits; anything else dismisses.
	if m.confirmingQuit {
		switch msg.String() {
		case "y", "Y":
			m.quitting = true
			return m, tea.Quit
		case "ctrl+c": // second ctrl+c forces the quit
			m.quitting = true
			return m, tea.Quit
		case "n", "N", "esc", "enter": // default is NO
			m.confirmingQuit = false
			return m, nil
		default:
			return m, nil // swallow other keys while the prompt is up
		}
	}

	// Transient text-entry modes capture every key.
	if m.searching {
		return m.handleSearchKey(msg)
	}
	if m.finding {
		return m.handleFindKey(msg)
	}
	// While the item list is capturing a filter query, every key edits
	// the query — don't treat q/esc/enter as navigation.
	if m.pane == paneList && m.itemList.SettingFilter() {
		var cmd tea.Cmd
		m.itemList, cmd = m.itemList.Update(msg)
		return m, cmd
	}

	m.status = ""
	switch msg.String() {
	case "q", "ctrl+c":
		m.confirmingQuit = true // arm instead of quitting outright
		return m, nil
	case "r":
		m.refresh()
		return m, nil
	case "tab":
		return m.cycle(1)
	case "shift+tab":
		return m.cycle(-1)
	case "/":
		if m.pane == paneDetail {
			return m.beginFind()
		}
		return m.forward(msg) // paneList: the list's built-in filter
	case "n":
		if m.pane == paneDetail {
			return m.jumpMatch(1)
		}
		return m.forward(msg)
	case "N":
		if m.pane == paneDetail {
			return m.jumpMatch(-1)
		}
		return m.forward(msg)
	case "enter", "l", "right":
		return m.descend()
	case "esc", "h", "left", "backspace":
		return m.ascend()
	default:
		return m.forward(msg)
	}
}

// handleSearchKey drives the global-search query field. Enter runs the search
// and shows results in the shared itemList; esc/ctrl+c cancels back to the
// categories; everything else edits the query.
func (m model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.searchQuery = m.input.Value()
		var items []list.Item
		for _, h := range searchCorpus(m.data, m.searchQuery) {
			items = append(items, searchHitRow{h})
		}
		m.cat = catSearch
		m.itemList.ResetFilter()
		_ = m.itemList.SetItems(items)
		m.itemList.Select(0)
		m.searching = false
		m.input.Blur()
		m.pane = paneList
		if len(items) == 0 {
			m.status = "no matches"
		}
		m.relayout()
		return m, nil
	case "esc", "ctrl+c":
		m.searching = false
		m.status = "" // drop any stale notice from a prior action
		m.input.Blur()
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

// handleFindKey drives in-page find within the detail viewport. Enter computes
// the match lines and jumps to the first; esc/ctrl+c cancels.
func (m model) handleFindKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.findQuery = m.input.Value()
		m.findMatches = findMatches(m.detailContent, m.findQuery)
		m.findIdx = 0
		m.finding = false
		m.input.Blur()
		if len(m.findMatches) == 0 {
			m.status = "no matches"
		} else {
			m.applyMatch()
		}
		return m, nil
	case "esc", "ctrl+c":
		m.finding = false
		m.status = "" // drop any stale notice from a prior action
		m.input.Blur()
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
}

// descend moves one level deeper: categories → list, list → detail.
func (m model) descend() (tea.Model, tea.Cmd) {
	switch m.pane {
	case paneCategories:
		sel, ok := m.catList.SelectedItem().(categoryRow)
		if !ok {
			return m, nil
		}
		if sel.kind == catSearch {
			return m.beginSearch()
		}
		m.cat = sel.kind
		m.loadCategory()
		m.pane = paneList
		m.relayout()
	case paneList:
		return m.openSelectedDetail()
	}
	return m, nil
}

// openSelectedDetail loads the currently selected item into the detail pane.
// Shared by Enter, Tab-flip, and global-search open so they behave identically;
// opening a search hit seeds in-page find and jumps to the first match.
func (m model) openSelectedDetail() (tea.Model, tea.Cmd) {
	title, content, err := m.loadDetail()
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.detailTitle = title
	m.detailContent = content
	m.detail.SetContent(content)
	m.detail.GotoTop()
	m.pane = paneDetail
	// Fresh item: clear any leftover find state...
	m.findQuery, m.findMatches, m.findIdx = "", nil, 0
	// ...unless this is a global-search hit — jump straight to the match.
	if _, ok := m.itemList.SelectedItem().(searchHitRow); ok && m.searchQuery != "" {
		m.findQuery = m.searchQuery
		m.findMatches = findMatches(content, m.findQuery)
		m.applyMatch()
	}
	m.relayout()
	return m, nil
}

// ascend walks one level back up.
func (m model) ascend() (tea.Model, tea.Cmd) {
	switch m.pane {
	case paneDetail:
		m.pane = paneList
		m.relayout()
	case paneList:
		m.pane = paneCategories
		m.relayout()
	}
	return m, nil
}

// cycle handles tab/shift+tab: at the list level it switches the active
// category (skipping Search); in the detail pane it flips to the next/previous
// item. dir is +1 for tab, -1 for shift+tab.
func (m model) cycle(dir int) (tea.Model, tea.Cmd) {
	switch m.pane {
	case paneList:
		order := []categoryKind{catRuns, catIncidents, catIterations}
		idx := 0
		for i, c := range order {
			if c == m.cat {
				idx = i
				break
			}
		}
		idx = (idx + dir + len(order)) % len(order)
		m.cat = order[idx]
		m.loadCategory()
		m.catList.Select(int(m.cat))
		m.relayout()
		return m, nil
	case paneDetail:
		next := m.itemList.Index() + dir
		if next < 0 || next >= len(m.itemList.Items()) {
			return m, nil // clamp at the ends — no wrap
		}
		m.itemList.Select(next)
		return m.openSelectedDetail()
	}
	return m, nil
}

// beginSearch enters the global-search query mode (from the Search category).
func (m model) beginSearch() (tea.Model, tea.Cmd) {
	m.searching = true
	m.input.Reset()
	m.input.Prompt = "search: "
	m.input.Placeholder = "text across runs, incidents, iterations…"
	return m, m.input.Focus() // Focus returns the cursor-blink cmd — keep it
}

// beginFind enters in-page find mode within the open detail.
func (m model) beginFind() (tea.Model, tea.Cmd) {
	m.finding = true
	m.input.Reset()
	m.input.Prompt = "/"
	m.input.Placeholder = "find in page…"
	return m, m.input.Focus() // Focus returns the cursor-blink cmd — keep it
}

// jumpMatch advances the in-page find cursor by dir (wrapping) and scrolls to it.
func (m model) jumpMatch(dir int) (tea.Model, tea.Cmd) {
	if len(m.findMatches) == 0 {
		m.status = "no matches"
		return m, nil
	}
	m.findIdx = (m.findIdx + dir + len(m.findMatches)) % len(m.findMatches)
	m.applyMatch()
	return m, nil
}

// applyMatch scrolls the detail viewport to the current match line and updates
// the status indicator.
func (m *model) applyMatch() {
	if len(m.findMatches) == 0 {
		return
	}
	if m.findIdx < 0 {
		m.findIdx = 0
	}
	if m.findIdx >= len(m.findMatches) {
		m.findIdx = len(m.findMatches) - 1
	}
	m.detail.SetYOffset(m.findMatches[m.findIdx])
	m.status = fmt.Sprintf("match %d/%d", m.findIdx+1, len(m.findMatches))
}

// refresh re-reads .ralph/state so a finished run / new incident / grown
// iteration appears. fsData reads disk on every call, so there is no cache to
// invalidate — this just re-invokes it.
func (m *model) refresh() {
	m.catList.SetItems(categoryItems(m.data))
	switch m.pane {
	case paneList:
		if m.cat != catSearch { // search results are not re-derivable here
			idx := m.itemList.Index()
			m.loadCategory()
			if idx < len(m.itemList.Items()) {
				m.itemList.Select(idx)
			}
		}
	case paneDetail:
		if title, content, err := m.loadDetail(); err == nil {
			off := m.detail.YOffset
			m.detailTitle = title
			m.detailContent = content
			m.detail.SetContent(content)
			m.detail.SetYOffset(off)
			// Keep an active in-page find consistent with the reloaded
			// content so n/N still land on real matches — but preserve the
			// scroll position rather than jumping.
			if m.findQuery != "" {
				m.findMatches = findMatches(content, m.findQuery)
				if m.findIdx >= len(m.findMatches) {
					m.findIdx = 0
				}
			}
		}
	}
	m.status = "refreshed"
}

// findMatches returns the 0-based indices of lines in content that contain
// query (case-insensitive). A free function: pure and directly testable.
func findMatches(content, query string) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var out []int
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(strings.ToLower(line), q) {
			out = append(out, i)
		}
	}
	return out
}

func (m *model) loadCategory() {
	var items []list.Item
	switch m.cat {
	case catRuns:
		for _, it := range m.data.runs() {
			items = append(items, runRow{it})
		}
	case catIncidents:
		for _, it := range m.data.incidents() {
			items = append(items, incidentRow{it})
		}
	case catIterations:
		for _, it := range m.data.iterations() {
			items = append(items, iterRow{it})
		}
	}
	m.itemList.ResetFilter()
	_ = m.itemList.SetItems(items)
	m.itemList.Select(0)
}

func (m model) loadDetail() (title, content string, err error) {
	sel := m.itemList.SelectedItem()
	if sel == nil {
		return "", "", fmt.Errorf("nothing to open")
	}
	switch r := sel.(type) {
	case runRow:
		c, e := m.data.runDetail(r.it.id)
		return "run " + r.it.id, c, e
	case incidentRow:
		c, e := m.data.incidentDetail(r.it.path)
		return r.it.name, c, e
	case iterRow:
		c, e := m.data.iterDetail(r.it.stem)
		return r.it.stem, c, e
	case searchHitRow:
		switch r.hit.cat {
		case catRuns:
			c, e := m.data.runDetail(r.hit.ref)
			return "run " + r.hit.ref, c, e
		case catIncidents:
			c, e := m.data.incidentDetail(r.hit.ref)
			return r.hit.title, c, e
		case catIterations:
			c, e := m.data.iterDetail(r.hit.ref)
			return r.hit.ref, c, e
		default:
			return "", "", fmt.Errorf("unknown search hit category")
		}
	default:
		return "", "", fmt.Errorf("unknown item")
	}
}

// forward routes a message to the focused component.
func (m model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.pane {
	case paneCategories:
		m.catList, cmd = m.catList.Update(msg)
	case paneList:
		m.itemList, cmd = m.itemList.Update(msg)
	case paneDetail:
		m.detail, cmd = m.detail.Update(msg)
	}
	return m, cmd
}

// relayout sizes the active component, reserving the header and help rows.
func (m *model) relayout() {
	if !m.ready {
		return
	}
	h := m.height - headerHeight - helpHeight
	if h < 1 {
		h = 1
	}
	m.catList.SetSize(m.width, h)
	m.itemList.SetSize(m.width, h)
	m.detail.Width = m.width
	m.detail.Height = h
	if w := m.width - 4; w > 0 {
		m.input.Width = w
	}
}

// View renders header · body · help, or a degraded single-pane view in a
// tiny window.
func (m model) View() string {
	if !m.ready {
		return "initializing…"
	}
	if m.width < minWidth || m.height < minHeight {
		return m.bodyView()
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.headerView(), m.bodyView(), m.helpView())
}

func (m model) bodyView() string {
	if m.searching {
		return m.input.View()
	}
	switch m.pane {
	case paneList:
		return m.itemList.View()
	case paneDetail:
		return m.detail.View()
	default:
		return m.catList.View()
	}
}

// headerView renders the breadcrumb identity line.
func (m model) headerView() string {
	crumb := "ralph explore"
	switch {
	case m.searching:
		crumb += " › Search"
	case m.pane == paneList:
		crumb += " › " + m.cat.String()
	case m.pane == paneDetail:
		crumb += " › " + m.cat.String() + " › " + m.detailTitle
	}
	return m.r.NewStyle().Bold(true).Render(truncate(crumb, m.width))
}

// helpView renders the key hints for the current pane, faint. The quit prompt
// and the in-page find field replace it (each a single line, so the reserved
// layout height is unchanged).
func (m model) helpView() string {
	if m.confirmingQuit {
		return m.confirmView()
	}
	if m.finding {
		return m.r.NewStyle().Render(truncate(m.input.View(), m.width))
	}
	var help string
	switch {
	case m.searching:
		help = "type query · enter search · esc cancel"
	case m.pane == paneList:
		help = "↑/↓ move · enter open · tab/⇧tab switch · / filter · esc back · r refresh · q quit"
	case m.pane == paneDetail:
		help = "↑/↓ scroll · tab/⇧tab item · / find · n/N match · esc back · r refresh · q quit"
	default:
		help = "↑/↓ move · enter open · r refresh · q quit"
	}
	if m.status != "" {
		help = m.status + " · " + help
	}
	return m.r.NewStyle().Faint(true).Render(truncate(help, m.width))
}

// confirmView renders the y/N quit prompt that replaces the help line while
// confirmingQuit is set. Capital N signals the default — only y/Y quits.
func (m model) confirmView() string {
	return m.r.NewStyle().Bold(true).Render(truncate("Quit ralph explore? (y/N)", m.width))
}

// truncate clips s to w runes, appending an ellipsis when it overflows.
func truncate(s string, w int) string {
	if w <= 0 || utf8.RuneCountInString(s) <= w {
		return s
	}
	r := []rune(s)
	if w == 1 {
		return string(r[:1])
	}
	return string(r[:w-1]) + "…"
}
