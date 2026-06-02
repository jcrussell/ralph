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
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/list"
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
)

func (c categoryKind) String() string {
	switch c {
	case catRuns:
		return "Runs"
	case catIncidents:
		return "Incidents"
	case catIterations:
		return "Iterations"
	default:
		return "?"
	}
}

// --- list rows (bubbles/list items) ---

type categoryRow struct {
	kind  categoryKind
	count int
}

func (c categoryRow) Title() string       { return c.kind.String() }
func (c categoryRow) Description() string { return fmt.Sprintf("%d item(s)", c.count) }
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

// model is the package-private Bubble Tea model (value receivers, per
// the bubbletea convention).
type model struct {
	data dataSource
	r    *lipgloss.Renderer

	pane pane
	cat  categoryKind

	catList     list.Model
	itemList    list.Model
	detail      viewport.Model
	detailTitle string

	width, height int
	ready         bool
	quitting      bool
	status        string // transient error/notice shown in the help line
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

	cats := []list.Item{
		categoryRow{kind: catRuns, count: len(data.runs())},
		categoryRow{kind: catIncidents, count: len(data.incidents())},
		categoryRow{kind: catIterations, count: len(data.iterations())},
	}
	catList := list.New(cats, list.NewDefaultDelegate(), 0, 0)
	catList.SetShowTitle(false)
	catList.SetShowStatusBar(false)
	catList.SetShowHelp(false)
	catList.SetFilteringEnabled(false) // only three rows

	itemList := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	itemList.SetShowTitle(false)
	itemList.SetShowStatusBar(false)
	itemList.SetShowHelp(false)

	return model{
		data:     data,
		r:        r,
		catList:  catList,
		itemList: itemList,
		detail:   viewport.New(0, 0),
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
		m.quitting = true
		return m, tea.Quit
	case "enter", "l", "right":
		return m.descend()
	case "esc", "h", "left", "backspace":
		return m.ascend()
	default:
		return m.forward(msg)
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
		m.cat = sel.kind
		m.loadCategory()
		m.pane = paneList
		m.relayout()
	case paneList:
		title, content, err := m.loadDetail()
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.detailTitle = title
		m.detail.SetContent(content)
		m.detail.GotoTop()
		m.pane = paneDetail
		m.relayout()
	}
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
	switch m.pane {
	case paneList:
		crumb += " › " + m.cat.String()
	case paneDetail:
		crumb += " › " + m.cat.String() + " › " + m.detailTitle
	}
	return m.r.NewStyle().Bold(true).Render(truncate(crumb, m.width))
}

// helpView renders the key hints for the current pane, faint.
func (m model) helpView() string {
	var help string
	switch m.pane {
	case paneList:
		help = "↑/↓ move · enter open · esc back · / filter · q quit"
	case paneDetail:
		help = "↑/↓ scroll · esc back · q quit"
	default:
		help = "↑/↓ move · enter open · q quit"
	}
	if m.status != "" {
		help = m.status + " · " + help
	}
	return m.r.NewStyle().Faint(true).Render(truncate(help, m.width))
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
