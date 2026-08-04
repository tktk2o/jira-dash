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

// commentsLoadedMsg carries the reply from `jira comment list`. It is its own
// type, and carries its key, so a reply that arrives after the cursor moved can
// be recognised as stale.
type commentsLoadedMsg struct {
	key      string
	comments []Comment
	err      error
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

	// The two halves of the pane that need a call. Held apart from the viewport
	// so an arrival can re-render the whole pane from what is known so far.
	detailBody     string
	detailComments []Comment

	filtering   bool
	filterDraft string

	// The create prompt is the one place the dashboard writes to Jira. It holds
	// only the summary: the type comes from which key opened it, and the project
	// and sprint from the row the cursor was on.
	creating    bool
	createDraft string
	createType  string

	// The ask prompt collects an instruction to hand a configured command, which
	// in practice means handing it to Claude. askKey is which keybinding opened
	// it: the command to run lives in the config, not here.
	asking   bool
	askDraft string
	askKey   string

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

// resizeDetail sizes the preview viewport, which does not draw its own chrome
// and starts 0x0 - without this the pane renders nothing at all. What is
// subtracted horizontally is what View wraps it in: the rule dividing the panes,
// the column of air after it, and the gap before it.
//
// Its height comes from tableHeight rather than its own arithmetic, because the
// two sit side by side and the taller one decides how far down the screen the
// footer lands. Computed separately they drifted, and opening the help pushed
// two lines off the bottom of the terminal. tableHeight is asked for its
// smallest answer - the one with the prompt line open - since the prompt appears
// without the viewport being resized. Nothing is taken off vertically: the
// divider is a left border only, so it adds no lines.
func (m *Model) resizeDetail() {
	previewWidth := int(float64(m.width) * m.cfg.Defaults.Preview.Width)
	m.detail.Width = maxInt(0, previewWidth-previewChrome)
	m.detail.Height = maxInt(0, m.tableHeight(true))
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeDetail()
		return m, nil

	case fetchedMsg:
		m = m.applyFetched(msg)
		// Rows landing in the section you are looking at is a selection change:
		// the cursor now points at an issue it did not point at before. Without
		// this the preview stayed empty until the first keypress.
		if msg.idx == m.active {
			return m, m.selectionChanged()
		}
		return m, nil

	case issueLoadedMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		// A reply for a row the cursor has already left must not overwrite the
		// pane with another issue's text.
		if msg.key != "" && msg.key == m.detailKey {
			m.detailBody = msg.markdown
			m.refreshDetail(false)
		}
		return m, nil

	case commentsLoadedMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		if msg.key != "" && msg.key == m.detailKey {
			m.detailComments = msg.comments
			m.refreshDetail(false)
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
		return m, tea.Batch(m.loadIssue(msg.key), m.loadComments(msg.key))

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
	if m.asking {
		return m.handleAskKey(msg)
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
	// The preview scrolls on its own keys, not j/k: the table and the pane are
	// separate places, and comments sit below a long description, so without
	// these they were unreachable. The row cursor is deliberately untouched.
	case "ctrl+d":
		m.detail.HalfViewDown()
		return m, nil
	case "ctrl+u":
		m.detail.HalfViewUp()
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
		// An explicit refresh blanks the list, the way gh-dash does: while the
		// query is in flight there is then no moment where old rows look
		// current. The cost is that a failed refresh has no stale rows to fall
		// back on, which is why only r does this - the refetch after a create
		// deliberately keeps its rows.
		s.issues = nil
		s.cursor = 0
		s.err = nil
		m.detailKey = ""
		m.detail.SetContent("")
		// The tick is batched in because the loop stops itself whenever nothing
		// is loading; without this the spinner would sit frozen on one frame.
		return m, tea.Batch(fetchSection(m.searcher, m.active, s.cfg), m.spinner.Tick)
	case "y":
		return m, m.copySelected(func(i Issue) string { return i.Key })
	case "Y":
		return m, m.copySelected(func(i Issue) string { return i.URL })
	case "?":
		// The help opens below the footer rather than over the screen, so it takes
		// lines the preview was using and the pane has to be told.
		m.showHelp = !m.showHelp
		m.resizeDetail()
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
// openAskPrompt starts the ask flow for a keybinding that declared prompt: true.
// The instruction is typed here rather than baked into the config command,
// because what you want done with an issue is different every time - a fixed
// prompt is a different feature, and the keys without prompt: true still are one.
func (m Model) openAskPrompt(key string) (tea.Model, tea.Cmd) {
	if _, ok := m.sections[m.active].selected(); !ok {
		m.status = "nothing to ask about: this section has no rows"
		return m, nil
	}
	m.asking = true
	m.askDraft = ""
	m.askKey = key
	return m, nil
}

func (m Model) handleAskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		// An empty instruction would hand over the issue and nothing to do with
		// it, which is what the keys without prompt: true are for. The prompt
		// stays open rather than launching something pointless.
		if strings.TrimSpace(m.askDraft) == "" {
			return m, nil
		}
		key, draft := m.askKey, strings.TrimSpace(m.askDraft)
		m.asking = false
		m.askDraft = ""
		m.askKey = ""
		return m.runUserKeybindingWith(key, draft)

	case tea.KeyEsc:
		m.asking = false
		m.askDraft = ""
		m.askKey = ""
		return m, nil
	case tea.KeyBackspace:
		if m.askDraft != "" {
			r := []rune(m.askDraft)
			m.askDraft = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.askDraft += string(msg.Runes)
		if msg.Type == tea.KeySpace {
			m.askDraft += " "
		}
		return m, nil
	}
	return m, nil
}

// AskPrompt assembles what the configured command receives as {{.Prompt}}: the
// issue, then the instruction. The description is included because the preview
// already fetched it - without it the receiving end has to spend another ~360ms
// on `jira get` to learn what the issue says, and it may not have credentials at
// all. An empty body is simply left out rather than announced.
func AskPrompt(i Issue, body, instruction string) string {
	parts := []string{i.Key + " " + i.Summary}
	if b := strings.TrimSpace(body); b != "" && b != "*no description*" {
		parts = append(parts, b)
	}
	return strings.Join(append(parts, "---", instruction), "\n\n")
}

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
