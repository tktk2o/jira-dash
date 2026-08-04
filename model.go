package main

import (
	"context"
	"strings"
	"time"

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

// visible applies the local filter. The filter never re-queries Jira: the
// config file owns what a section *is*, the filter only narrows what you are
// looking at right now.
func (s sectionState) visible() []Issue {
	if s.filter == "" {
		return s.issues
	}
	q := strings.ToLower(s.filter)
	out := make([]Issue, 0, len(s.issues))
	for _, i := range s.issues {
		haystack := strings.ToLower(i.Key + " " + i.Summary + " " + i.Status)
		if strings.Contains(haystack, q) {
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

	pendingG bool
	showHelp bool
	status   string
}

func NewModel(cfg *Config, s Searcher, c *Cache, now func() time.Time) Model {
	m := Model{
		cfg:         cfg,
		searcher:    s,
		cache:       c,
		now:         now,
		previewOpen: *cfg.Defaults.Preview.Open,
		detail:      viewport.New(0, 0),
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
	cmds := make([]tea.Cmd, 0, len(m.sections))
	for i, s := range m.sections {
		cmds = append(cmds, fetchSection(m.searcher, i, s.cfg))
	}
	return tea.Batch(cmds...)
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
		return m, fetchSection(m.searcher, m.active, s.cfg)
	case "y":
		return m, m.copySelected(func(i Issue) string { return i.Key })
	case "Y":
		return m, m.copySelected(func(i Issue) string { return i.URL })
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	}

	return m.runUserKeybinding(msg.String())
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
