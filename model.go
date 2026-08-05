package main

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// fetchConcurrency bounds how many section fetches run at once. The exec
// startup that used to make parallelism essential is gone, but Jira is a
// shared API on the other end of these calls, and a tab per core is enough -
// the dashboard should not look like a load test.
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

// choicesLoadedMsg carries the reply from a choicesFrom: transitions/assignees
// fetch. It carries both the issue the fetch was for and the keybinding it was
// for, and the seq the request was issued under, because all three can go
// stale before the reply lands: the cursor can move to another issue, another
// press of a different choices key can start a second fetch, and only comparing
// seq (the debounceMsg pattern) would still open a picker for an issue the
// cursor has since left if the key were pressed twice in a row on two rows.
type choicesLoadedMsg struct {
	seq   int
	key   string
	kbKey string
	list  []Choice
	err   error
}

// Model is the whole state of the dashboard, and Bubble Tea copies it by value
// on every message. The fields it does not own - the config, the searcher, the
// cache - are held as pointers or interfaces so that copy stays cheap.
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
	creating   bool
	createType string

	// The ask prompt collects an instruction to hand a configured command, which
	// in practice means handing it to Claude. askKey is which keybinding opened
	// it: the command to run lives in the config, not here.
	asking bool
	askKey string

	// The picker a keybinding with choices opens. The list is resolved when the
	// box opens rather than read from the config as it draws, because a
	// choicesFrom list is derived from the rows and would otherwise change under
	// the cursor while the box is up.
	choosing     bool
	chooseKey    string
	chooseList   []Choice
	chooseCursor int

	// loadingChoices and chooseSeq back choicesFrom: transitions/assignees, the
	// two sources that need an API call before the box has anything to show.
	// loadingChoices keeps the spinner ticking and the footer saying so while
	// that call is in flight, in the gap where m.choosing is still false because
	// the plan is explicit that an empty picker reads as a broken key. chooseSeq
	// is compared against on arrival, the same way detailSeq is: a second press
	// of a choices key - on this row or another - starts a new fetch, and the
	// reply to the first must not then open a picker nobody asked for anymore.
	loadingChoices bool
	chooseSeq      int

	// prompt is the input both boxes share. One textarea rather than a draft
	// string each: it is what gives the box line numbers, a cursor that moves,
	// and multi-line editing, none of which a string accumulating runes had.
	prompt textarea.Model

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
	if m.loadingChoices {
		return true
	}
	for _, s := range m.sections {
		if s.loading {
			return true
		}
	}
	return false
}

// NewModel builds the dashboard's initial state. now is injected so that every
// age on screen is a pure function of the model in a test.
func NewModel(cfg *Config, s Searcher, c *Cache, now func() time.Time) Model {
	m := Model{
		cfg:         cfg,
		searcher:    s,
		cache:       c,
		now:         now,
		previewOpen: *cfg.Defaults.Preview.Open,
		detail:      viewport.New(0, 0),
		spinner:     newSpinner(cfg.Theme),
		// Built here, not on first use: a zero textarea.Model has nil internals
		// and panics the moment a prompt opens.
		prompt: newPromptInput(cfg.Theme),
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

// Init fetches every section at once. The rows already seeded from cache are on
// screen by then, so what these replace is visible rather than blank.
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
// two lines off the bottom of the terminal. Nothing is taken off vertically: the
// divider is a left border only, so it adds no lines.
//
// Update owns calling this, by comparing chromeLines across a key. Asking each
// transition to remember instead lost `/`, and the footer fell off the bottom of
// the terminal - TestViewNeverDrawsMoreLinesThanTheTerminalHas covers every
// prompt state so that class of miss fails there rather than on screen.
func (m *Model) resizeDetail() {
	previewWidth := int(float64(m.width) * m.cfg.Defaults.Preview.Width)
	m.detail.Width = max(0, previewWidth-previewChrome)
	m.detail.Height = max(0, m.tableHeight())
}

// tableWidth is the pane the table gets: the whole terminal, less the preview
// when one is showing.
func (m Model) tableWidth() int {
	if !PreviewVisible(m.previewOpen, m.width, m.cfg.Defaults.Preview.Width) {
		return m.width
	}
	return m.width - int(float64(m.width)*m.cfg.Defaults.Preview.Width)
}

// Update is the only place the model changes. Every case returns a new copy, so
// a command that has already been handed a model can never see a later one.
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

	case choicesLoadedMsg:
		m.loadingChoices = false
		// Stale: a later press of a choices key - this one or another - has
		// already superseded this fetch.
		if msg.seq != m.chooseSeq {
			return m, nil
		}
		if msg.err != nil {
			m.status = msg.kbKey + ": " + msg.err.Error()
			return m, nil
		}
		// The cursor left this issue before the reply arrived. Opening now would
		// attach transitions or assignees fetched for one issue to whatever row
		// is under the cursor now.
		if cur, ok := m.sections[m.active].selected(); !ok || cur.Key != msg.key {
			m.status = ""
			return m, nil
		}
		if len(msg.list) == 0 {
			m.status = msg.kbKey + ": no choices to pick from"
			return m, nil
		}
		m.status = ""
		m.choosing = true
		m.chooseKey = msg.kbKey
		m.chooseList = msg.list
		m.chooseCursor = 0
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
			// The command's own last line of stderr, not just the exit status: an
			// ExecProcess failure used to redraw straight over whatever the command
			// printed, so the footer could only ever say "exit status 1" and never why.
			detail := msg.err.Error()
			if msg.stderr != "" {
				detail = msg.stderr
			}
			m.status = msg.key + ": " + detail
			return m, nil
		}
		m.status = ""
		if msg.refresh {
			// The row as well as the pane: a status or an assignee shows in the
			// table's own columns, and a comment shows in the preview, and from here
			// the two are indistinguishable. The rows are not blanked the way r does
			// it - this is a refetch behind a change you just made, not a reload you
			// asked to watch. fetchedMsg re-arms the preview load on arrival.
			s := &m.sections[m.active]
			s.loading = true
			return m, tea.Batch(fetchSection(m.searcher, m.active, s.cfg), m.spinner.Tick)
		}
		return m, nil

	case tea.KeyMsg:
		// The preview and the table stand side by side, so whichever is taller
		// decides where the footer lands - and the chrome above and below the table
		// changes height as the help, the prompts and a section's filter come and
		// go. Resizing here, once, rather than at each of those transitions: the
		// version that asked every one of them to remember dropped `/`, and the
		// footer fell off the bottom of the terminal.
		before := m.chromeLines()
		next, cmd := m.handleKey(msg)
		updated, ok := next.(Model)
		if !ok {
			return next, cmd
		}
		if updated.chromeLines() != before {
			updated.resizeDetail()
		}
		return updated, cmd
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
		s.cursor = max(0, len(s.visible())-1)
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
	if m.choosing {
		return m.handleChooseKey(msg)
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
		m.sections[m.active].cursor = max(0, len(m.sections[m.active].visible())-1)
		return m, m.selectionChanged()
	case "p":
		m.previewOpen = !m.previewOpen
		return m, nil
	// The preview scrolls on its own keys, not j/k: the table and the pane are
	// separate places, and comments sit below a long description, so without
	// these they were unreachable. The row cursor is deliberately untouched.
	case "ctrl+d":
		m.detail.HalfPageDown()
		return m, nil
	case "ctrl+u":
		m.detail.HalfPageUp()
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
		// The help opens below the footer rather than over the screen. Update
		// notices the chrome got taller and resizes the preview.
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
	m.askKey = key
	m.openPrompt(askInputHeight)
	return m, nil
}

func (m Model) handleAskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlD:
		// An empty instruction would hand over the issue and nothing to do with
		// it, which is what the keys without prompt: true are for. The box stays
		// open rather than launching something pointless.
		instruction := strings.TrimSpace(m.prompt.Value())
		if instruction == "" {
			return m, nil
		}
		key := m.askKey
		m.closePrompt()
		return m.runUserKeybindingWith(key, instruction)

	case tea.KeyEsc, tea.KeyCtrlC:
		m.closePrompt()
		return m, nil
	}
	return m.updatePrompt(msg)
}

// openChoosePrompt opens the picker for a keybinding with choices, or starts
// loading one for a keybinding with choicesFrom: transitions/assignees.
//
// The two live sources do not open the box here: they hand back a command and
// let choicesLoadedMsg open it once the reply is in. The plan for this is
// explicit - an empty picker reads as a broken key, so the box has to wait for
// candidates rather than show them arriving.
func (m Model) openChoosePrompt(kb Keybinding) (tea.Model, tea.Cmd) {
	issue, ok := m.sections[m.active].selected()
	if !ok {
		m.status = "nothing to change: this section has no rows"
		return m, nil
	}

	if kb.ChoicesFrom == choicesFromTransitions || kb.ChoicesFrom == choicesFromAssignees {
		m.chooseSeq++
		m.loadingChoices = true
		m.status = "loading " + kb.ChoicesFrom + "..."
		return m, tea.Batch(
			fetchChoices(m.searcher, m.chooseSeq, kb.Key, kb.ChoicesFrom, issue.Key),
			m.spinner.Tick,
		)
	}

	// The remaining two sources need no call: choices is written in the config,
	// and statuses is derived from rows jhd already holds. The list is resolved
	// here, once, so that an empty one is reported as the config problem or the
	// empty tab it is, rather than as a box with nothing in it.
	list := kb.Choices
	if kb.ChoicesFrom == choicesFromStatuses {
		list = m.sectionStatuses()
	}
	if len(list) == 0 {
		m.status = kb.Key + ": no choices to pick from"
		return m, nil
	}
	m.choosing = true
	m.chooseKey = kb.Key
	m.chooseList = list
	m.chooseCursor = 0
	return m, nil
}

// fetchChoices calls the API behind choicesFrom: transitions/assignees. seq and
// kbKey travel with the reply so Update can tell a stale answer from a current
// one without having to look the keybinding back up.
func fetchChoices(s Searcher, seq int, kbKey, source, issueKey string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		var list []Choice
		var err error
		switch source {
		case choicesFromTransitions:
			var transitions []Transition
			transitions, err = s.Transitions(ctx, issueKey)
			for _, t := range transitions {
				// Label-less: jira edit -S takes the transition's name, so the name is
				// both what the picker shows and what it sends, same as a status
				// under choicesFrom: statuses.
				list = append(list, Choice{Value: t.Name})
			}
		case choicesFromAssignees:
			var users []User
			users, err = s.AssignableUsers(ctx, issueKey, "")
			for _, u := range users {
				list = append(list, Choice{Label: u.DisplayName, Value: u.AccountID})
			}
		}
		return choicesLoadedMsg{seq: seq, key: issueKey, kbKey: kbKey, list: list, err: err}
	}
}

// sectionStatuses is the choicesFrom: statuses list - the status names the rows
// in view actually carry, in the order they first appear so the list is stable
// between openings rather than reordering itself as the cursor moves.
//
// The limit is real and worth knowing: a status no current row has is not
// offered, so on a tab where nothing is finished there is no "Done" to pick.
func (m Model) sectionStatuses() []Choice {
	seen := map[string]bool{}
	var out []Choice
	for _, i := range m.sections[m.active].visible() {
		if i.Status == "" || seen[i.Status] {
			continue
		}
		seen[i.Status] = true
		out = append(out, Choice{Value: i.Status})
	}
	return out
}

func (m Model) handleChooseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.chooseCursor = min(m.chooseCursor+1, len(m.chooseList)-1)
		return m, nil
	case "k", "up":
		m.chooseCursor = max(m.chooseCursor-1, 0)
		return m, nil
	case "enter":
		key, value := m.chooseKey, m.chooseList[m.chooseCursor].Value
		m.closeChoosePrompt()
		return m.runUserKeybindingWithChoice(key, value)
	case "esc", "ctrl+c":
		m.closeChoosePrompt()
		return m, nil
	}
	return m, nil
}

func (m *Model) closeChoosePrompt() {
	m.choosing = false
	m.chooseKey = ""
	m.chooseList = nil
	m.chooseCursor = 0
}

// AskPrompt assembles what the configured command receives as {{.Prompt}}: the
// issue, then the instruction. The description is included because the preview
// already fetched it - without it the receiving end has to spend another
// 0.5-1.2s Jira REST round trip to learn what the issue says, and it may not
// have credentials at all. An empty body is simply left out rather than
// announced.
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
	m.createType = issueType
	m.openPrompt(createInputHeight)
	return m, nil
}

// handleCreateKey routes keys to the box's input, keeping only the two that
// leave it. Ctrl+d rather than enter submits, because enter now inserts a
// newline inside the box - the box states both on the line above its border.
func (m Model) handleCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlD:
		// Jira requires a summary to create an issue, so an empty one is refused
		// here rather than coming back as an API failure after a round trip. The
		// box stays open.
		summary := strings.TrimSpace(m.prompt.Value())
		if summary == "" {
			return m, nil
		}
		row, ok := m.sections[m.active].selected()
		if !ok {
			m.closePrompt()
			m.status = "the row this create was based on is gone"
			return m, nil
		}
		req := NewIssueRequest{
			Project: row.Project.Key,
			Type:    m.createType,
			Summary: summary,
		}
		if sprint, ok := row.CurrentSprint(); ok {
			req.Sprint = sprint.Name
		}

		m.closePrompt()
		m.status = "creating " + req.Type + "..."
		return m, createIssue(m.searcher, m.active, req)

	case tea.KeyEsc, tea.KeyCtrlC:
		m.closePrompt()
		return m, nil
	}
	return m.updatePrompt(msg)
}

// updatePrompt hands a key to the box's input. Everything the box does not claim
// belongs to the textarea - which is what makes the cursor keys, word deletion
// and the rest work without this file knowing about any of them.
func (m Model) updatePrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	return m, cmd
}

// closePrompt is a method rather than the same three assignments at each exit.
func (m *Model) closePrompt() {
	m.creating, m.asking, m.askKey = false, false, ""
	m.prompt.Reset()
	m.prompt.Blur()
}

// openPrompt sizes the box's input and focuses it. The height is the difference
// between the two boxes: a Jira summary is one line, an instruction is not.
func (m *Model) openPrompt(height int) {
	m.prompt.Reset()
	m.prompt.SetHeight(height)
	m.prompt.Focus()
	// The box is as wide as the table, less its border, its padding and the
	// column the line numbers take.
	m.prompt.SetWidth(max(1, m.tableWidth()-6))
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
	// Space arrives as its own key type, not as a rune, so without it here a
	// filter could never hold more than one word - and a summary filter is
	// exactly where you want two.
	case tea.KeyRunes:
		m.filterDraft += string(msg.Runes)
		return m, nil
	case tea.KeySpace:
		m.filterDraft += " "
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
		next = max(0, limit)
	}
	s.cursor = next
	return m, m.selectionChanged()
}
