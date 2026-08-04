package main

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// fetchConcurrency bounds how many jira processes run at once. Each costs
// ~360ms of startup, so some parallelism is essential, but a tab per core is
// enough - the dashboard should not look like a load test.
const fetchConcurrency = 4

// fetchTimeout stops a hung call from leaving a section spinning forever.
const fetchTimeout = 15 * time.Second

var fetchSem = make(chan struct{}, fetchConcurrency)

type sectionState struct {
	cfg       Section
	cacheKey  string
	issues    []Issue
	fetchedAt time.Time
	loading   bool
	err       error
	cursor    int
	filter    string
}

// visible applies the section's sprint prefix and then the local filter.
// Neither re-queries Jira: the JQL owns what a section *is*, the prefix trims
// what that query cannot express, and the filter narrows what you are looking
// at right now. Both are applied here rather than at fetch time so the cache
// keeps the full result and editing either takes effect without a round trip.
func (s sectionState) visible() []Issue {
	if s.filter == "" && s.cfg.SprintPrefix == "" {
		return s.issues
	}
	q := strings.ToLower(s.filter)
	out := make([]Issue, 0, len(s.issues))
	for _, i := range s.issues {
		if !i.InActiveSprintPrefix(s.cfg.SprintPrefix) {
			continue
		}
		haystack := strings.ToLower(i.Key + " " + i.Summary + " " + i.Status)
		if q == "" || strings.Contains(haystack, q) {
			out = append(out, i)
		}
	}
	return out
}

func (s sectionState) selected() (Issue, bool) {
	v := s.visible()
	if len(v) == 0 || s.cursor < 0 || s.cursor >= len(v) {
		return Issue{}, false
	}
	return v[s.cursor], true
}

type fetchedMsg struct {
	idx    int
	issues []Issue
	at     time.Time
	err    error
}

type issueLoadedMsg struct {
	key      string
	markdown string
	err      error
}

type debounceMsg struct {
	seq int
	key string
}

type Model struct {
	cfg      *Config
	searcher Searcher
	cache    *Cache
	now      func() time.Time

	sections []sectionState
	active   int

	width, height int
	previewOpen   bool
	detail        viewport.Model
	detailKey     string
	detailSeq     int

	filtering   bool
	filterDraft string

	// The create prompt is the one place the dashboard writes to Jira. It holds
	// only the summary: the type comes from which key opened it, and the project
	// and sprint from the row the cursor was on.
	creating    bool
	createDraft string
	createType  string

	pendingG bool
	showHelp bool
	status   string

	// spinner animates while any section is in flight. Its tick loop is only
	// kept alive while something is loading: an idle dashboard that wakes up
	// several times a second to redraw the same frame is pure waste.
	spinner spinner.Model
}

// anyLoading answers whether the tick loop still has anything to animate.
func (m Model) anyLoading() bool {
	for _, s := range m.sections {
		if s.loading {
			return true
		}
	}
	return false
}

func NewModel(cfg *Config, s Searcher, c *Cache, now func() time.Time) Model {
	m := Model{
		cfg:         cfg,
		searcher:    s,
		cache:       c,
		now:         now,
		previewOpen: *cfg.Defaults.Preview.Open,
		detail:      viewport.New(0, 0),
		spinner:     newSpinner(cfg.Theme),
	}

	// Seed from cache so the first frame is instant; the fetch in Init then
	// replaces these rows.
	for _, sec := range cfg.Sections {
		st := sectionState{cfg: sec, cacheKey: SectionKey(sec.JQL, sec.Limit), loading: true}
		if cached, ok := c.ReadSection(st.cacheKey); ok {
			st.issues = cached.Issues
			st.fetchedAt = cached.FetchedAt
		}
		m.sections = append(m.sections, st)
	}
	return m
}

func (m Model) Init() tea.Cmd {
	// +1 for the spinner tick: every section starts out loading, so the
	// animation has to be running from the first frame.
	cmds := make([]tea.Cmd, 0, len(m.sections)+1)
	for i, s := range m.sections {
		cmds = append(cmds, fetchSection(m.searcher, i, s.cfg))
	}
	return tea.Batch(append(cmds, m.spinner.Tick)...)
}

func fetchSection(s Searcher, idx int, sec Section) tea.Cmd {
	return func() tea.Msg {
		fetchSem <- struct{}{}
		defer func() { <-fetchSem }()

		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		found, err := s.Search(ctx, sec.JQL, sec.Limit)
		return fetchedMsg{idx: idx, issues: found, at: time.Now(), err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// The viewport starts 0x0, so without this the preview pane renders
		// nothing at all. What is subtracted is chrome the viewport does not
		// draw itself: the border View wraps it in (2 cells each way), the
		// single-column gap between the panes, and vertically the tab strip,
		// the footer and the filter line.
		previewWidth := int(float64(m.width) * m.cfg.Defaults.Preview.Width)
		m.detail.Width = maxInt(0, previewWidth-borderChrome-paneGap)
		m.detail.Height = maxInt(0, m.height-verticalChrome-borderChrome)
		return m, nil

	case fetchedMsg:
		return m.applyFetched(msg), nil

	case issueLoadedMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		if msg.key != "" && msg.key == m.detailKey {
			m.detail.SetContent(renderMarkdown(msg.markdown, m.detail.Width))
		}
		return m, nil

	case spinner.TickMsg:
		// The loop ends by simply not scheduling the next tick. bubbles guards
		// against two loops running at once by tagging its ticks, so a refresh
		// restarting the loop cannot double the frame rate.
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if !m.anyLoading() {
			return m, nil
		}
		return m, cmd

	case createdMsg:
		if msg.err != nil {
			m.status = "create failed: " + msg.err.Error()
			return m, nil
		}
		m.status = "created " + msg.issue.Key
		// Refresh the section it went into, so the new row appears without an r.
		if msg.idx >= 0 && msg.idx < len(m.sections) {
			m.sections[msg.idx].loading = true
			return m, tea.Batch(
				fetchSection(m.searcher, msg.idx, m.sections[msg.idx].cfg),
				m.spinner.Tick,
			)
		}
		return m, nil

	case debounceMsg:
		// A stale tick from a cursor position we have already left.
		if msg.seq != m.detailSeq {
			return m, nil
		}
		return m, m.loadIssue(msg.key)

	case copiedMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.status = "copied " + msg.value
		return m, nil

	case commandRanMsg:
		if msg.err != nil {
			m.status = msg.key + ": " + msg.err.Error()
			return m, nil
		}
		m.status = ""
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) applyFetched(msg fetchedMsg) Model {
	if msg.idx < 0 || msg.idx >= len(m.sections) {
		return m
	}
	s := m.sections[msg.idx]
	s.loading = false

	if msg.err != nil {
		// Keep the stale rows: an empty dashboard is worse than an old one.
		s.err = msg.err
		m.sections[msg.idx] = s
		return m
	}

	s.err = nil
	s.issues = msg.issues
	s.fetchedAt = msg.at
	if s.cursor >= len(s.visible()) {
		s.cursor = maxInt(0, len(s.visible())-1)
	}
	m.sections[msg.idx] = s

	// Best effort: a cache write failure must not interrupt browsing.
	_ = m.cache.WriteSection(s.cacheKey, msg.issues, msg.at)
	return m
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		return m.handleFilterKey(msg)
	}
	if m.creating {
		return m.handleCreateKey(msg)
	}

	// Any key other than a second g disarms the gg motion. This has to happen
	// before the switch: most cases return from inside it, so clearing the flag
	// afterwards would only ever run for unhandled keys, and `g j g` would
	// still jump to the top.
	if msg.String() != "g" {
		m.pendingG = false
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	// h/l and the arrows are gh-dash's section keys; tab/shift+tab are kept
	// because they are what the help line has always advertised.
	case "tab", "l", "right":
		m.active = (m.active + 1) % len(m.sections)
		return m, m.selectionChanged()
	case "shift+tab", "h", "left":
		m.active = (m.active - 1 + len(m.sections)) % len(m.sections)
		return m, m.selectionChanged()
	case "j", "down":
		return m.moveCursor(1)
	case "k", "up":
		return m.moveCursor(-1)
	case "g":
		// gg: the first g arms, the second jumps.
		if m.pendingG {
			m.pendingG = false
			m.sections[m.active].cursor = 0
			return m, m.selectionChanged()
		}
		m.pendingG = true
		return m, nil
	case "G":
		m.sections[m.active].cursor = maxInt(0, len(m.sections[m.active].visible())-1)
		return m, m.selectionChanged()
	case "p":
		m.previewOpen = !m.previewOpen
		return m, nil
	case "/":
		m.filtering = true
		m.filterDraft = m.sections[m.active].filter
		return m, nil
	case "esc":
		m.sections[m.active].filter = ""
		m.sections[m.active].cursor = 0
		return m, m.selectionChanged()
	case "r":
		s := &m.sections[m.active]
		s.loading = true
		// The tick is batched in because the loop stops itself whenever nothing
		// is loading; without this the spinner would sit frozen on one frame.
		return m, tea.Batch(fetchSection(m.searcher, m.active, s.cfg), m.spinner.Tick)
	case "y":
		return m, m.copySelected(func(i Issue) string { return i.Key })
	case "Y":
		return m, m.copySelected(func(i Issue) string { return i.URL })
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	}

	// A configured create key wins over a configured issue keybinding; LoadConfig
	// has already refused any create key that collides with the cases above.
	for _, ck := range m.cfg.Create {
		if ck.Key == msg.String() {
			return m.openCreatePrompt(ck.Type)
		}
	}

	return m.runUserKeybinding(msg.String())
}

// openCreatePrompt starts the create flow for one issue type. It refuses on an
// empty section: the project and sprint are inherited from the row under the
// cursor, so with no row there is nothing to inherit and `jira create` would be
// handed an empty -p.
func (m Model) openCreatePrompt(issueType string) (tea.Model, tea.Cmd) {
	if _, ok := m.sections[m.active].selected(); !ok {
		m.status = "nothing to create from: this section has no rows"
		return m, nil
	}
	m.creating = true
	m.createDraft = ""
	m.createType = issueType
	return m, nil
}

func (m Model) handleCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		// `jira create` requires -s, so an empty summary is refused here rather
		// than coming back as a CLI failure 360ms later. The prompt stays open.
		if strings.TrimSpace(m.createDraft) == "" {
			return m, nil
		}
		row, ok := m.sections[m.active].selected()
		if !ok {
			m.creating = false
			m.status = "the row this create was based on is gone"
			return m, nil
		}
		req := NewIssueRequest{
			Project: row.Project.Key,
			Type:    m.createType,
			Summary: strings.TrimSpace(m.createDraft),
		}
		if sprint, ok := row.CurrentSprint(); ok {
			req.SprintID = sprint.ID
		}

		m.creating = false
		m.createDraft = ""
		m.status = "creating " + req.Type + "..."
		return m, createIssue(m.searcher, m.active, req)

	case tea.KeyEsc:
		m.creating = false
		m.createDraft = ""
		return m, nil
	case tea.KeyBackspace:
		if m.createDraft != "" {
			r := []rune(m.createDraft)
			m.createDraft = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.createDraft += string(msg.Runes)
		if msg.Type == tea.KeySpace {
			m.createDraft += " "
		}
		return m, nil
	}
	return m, nil
}

// createdMsg reports the outcome. idx is the section the create was launched
// from, so the refresh lands there even if the cursor has moved on since.
type createdMsg struct {
	issue Issue
	idx   int
	err   error
}

func createIssue(s Searcher, idx int, req NewIssueRequest) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		created, err := s.Create(ctx, req)
		return createdMsg{issue: created, idx: idx, err: err}
	}
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.filtering = false
		m.sections[m.active].filter = m.filterDraft
		m.sections[m.active].cursor = 0
		return m, m.selectionChanged()
	case tea.KeyEsc:
		m.filtering = false
		m.filterDraft = ""
		return m, nil
	case tea.KeyBackspace:
		if m.filterDraft != "" {
			r := []rune(m.filterDraft)
			m.filterDraft = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes:
		m.filterDraft += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

func (m Model) moveCursor(delta int) (tea.Model, tea.Cmd) {
	s := &m.sections[m.active]
	next := s.cursor + delta
	if next < 0 {
		next = 0
	}
	if limit := len(s.visible()) - 1; next > limit {
		next = maxInt(0, limit)
	}
	s.cursor = next
	return m, m.selectionChanged()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
