package main

import (
	"context"
	"sort"
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

// uiTickInterval redraws the dashboard on a plain clock rather than only on
// input or a fetch landing. Its one job is to keep the relative ages in the
// Updated column and the preview header from freezing between keypresses -
// bubbletea only repaints on a message, and without this one an idle terminal
// otherwise sits on the frame from whenever something last happened. Used only
// when defaults.refetchIntervalMinutes is 0: the auto-refetch tick already
// forces exactly this redraw on its own schedule, so a second clock alongside
// it would be pure waste.
const uiTickInterval = time.Minute

// confirmQuitPrompt is what the footer says while defaults.confirmQuit is
// waiting on a second q/ctrl+c. Named so Update's set and its clear cannot say
// two different strings.
const confirmQuitPrompt = "press q again to quit"

// tickMsg drives both auto-refetch (gh-dash's defaults.refetchIntervalMinutes)
// and the plain redraw clock above; refetch tells Update which job this tick
// is for. Only Init ever starts this loop, and the handler below is the only
// place that reschedules it - unlike the spinner, which a keypress can restart
// mid-flight, nothing else here can start a second copy, so there is no
// pileup to tag a generation against.
type tickMsg struct{ refetch bool }

// tickCmd schedules one tick after d. Called both from Init to start the loop
// and from the tickMsg case to keep it going.
func tickCmd(d time.Duration, refetch bool) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{refetch: refetch} })
}

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
	fetchSeq  int
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
	seq    int
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

	// detailBodyDone separates "the description has not arrived yet" from "it
	// arrived and is empty". Without it an issue with no description read as a
	// fetch that never finished - the pane said "loading..." forever, since an
	// empty detailBody was the only signal either state had.
	detailBodyDone    bool
	detailBodyErr     string
	detailCommentsErr string

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

	// chooseFilter is what has been typed into the picker box. It narrows
	// chooseList the way the table's own filter narrows a section - see
	// visibleChoices - but there is no draft/commit split like filterDraft's:
	// a picker is already a modal box with nothing else competing for the
	// keys, so there is no reason to make typing provisional.
	chooseFilter string

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

	// pendingQuit backs defaults.confirmQuit: the first q/ctrl+c arms it and
	// leaves a prompt in the footer instead of quitting; any other key disarms
	// it again. Kept as a bool rather than folded into pendingG's flag because the
	// two are unrelated motions that could otherwise be pressed in the same
	// sequence (q after gg, say) and would then clear each other.
	pendingQuit bool

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
		st := sectionState{cfg: sec, cacheKey: SectionKey(sec.JQL, sec.Limit), loading: true, fetchSeq: 1}
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
	// +2 for the spinner tick (every section starts out loading, so the
	// animation has to be running from the first frame) and the clock tick that
	// keeps ages live and, if configured, drives auto-refetch.
	cmds := make([]tea.Cmd, 0, len(m.sections)+2)
	for i, s := range m.sections {
		cmds = append(cmds, fetchSection(m.searcher, i, s.fetchSeq, s.cfg))
	}
	cmds = append(cmds, m.spinner.Tick, m.nextTick())
	return tea.Batch(cmds...)
}

// nextTick schedules the one clock tick that is currently in flight: a
// refetch tick on defaults.refetchIntervalMinutes when that is nonzero, or
// else the lighter UI-only redraw tick. Read from cfg on every call rather
// than cached on the model, so the mode can never drift from what LoadConfig
// actually validated.
func (m Model) nextTick() tea.Cmd {
	if n := *m.cfg.Defaults.RefetchIntervalMinutes; n > 0 {
		return tickCmd(time.Duration(n)*time.Minute, true)
	}
	return tickCmd(uiTickInterval, false)
}

func fetchSection(s Searcher, idx, seq int, sec Section) tea.Cmd {
	return func() tea.Msg {
		fetchSem <- struct{}{}
		defer func() { <-fetchSem }()

		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		found, err := s.Search(ctx, sec.JQL, sec.Limit)
		return fetchedMsg{idx: idx, seq: seq, issues: found, at: time.Now(), err: err}
	}
}

// resizeDetail sizes the preview viewport, which does not draw its own chrome
// and starts 0x0 - without this the pane renders nothing at all. What is
// subtracted is what View wraps it in, and which dimension that is depends on
// where the pane sits: beside the table it is width (the rule dividing the
// panes, the column of air after it, and the gap before it); below it it is
// height (one rule, see bottomPreviewChrome).
//
// Its size comes from tableHeight/bottomPreviewHeight rather than its own
// arithmetic, because the table and the pane share whatever chrome is above
// and below them, and computed separately the two drifted - opening the help
// once pushed two lines off the bottom of the terminal.
//
// Update owns calling this, by comparing chromeLines across a key. Asking each
// transition to remember instead lost `/`, and the footer fell off the bottom of
// the terminal - TestViewNeverDrawsMoreLinesThanTheTerminalHas covers every
// prompt state so that class of miss fails there rather than on screen.
func (m *Model) resizeDetail() {
	if m.previewPosition() != "right" {
		// Full table width, since the pane sits below rather than beside it, and
		// only its own chrome line is taken off vertically.
		m.detail.Width = max(0, m.tableWidth())
		m.detail.Height = max(0, m.bottomPreviewHeight()-bottomPreviewChrome)
		return
	}
	previewWidth := int(float64(m.width) * m.cfg.Defaults.Preview.Width)
	m.detail.Width = max(0, previewWidth-previewChrome)
	m.detail.Height = max(0, m.tableHeight())
}

// previewPosition is where defaults.preview.position resolves to for this
// terminal's width - see PreviewPosition for what "auto" decides between.
func (m Model) previewPosition() string {
	return PreviewPosition(m.cfg.Defaults.Preview.Position, m.width)
}

// previewShown answers whether the preview pane draws at all. A "right" pane
// keeps the config-wins-when-closed, terminal-decides-otherwise rule it always
// had; a "bottom" pane (explicit, or "auto" resolved to it) costs the table no
// horizontal room to stay open, so it only depends on the toggle.
func (m Model) previewShown() bool {
	if m.previewPosition() == "right" {
		return PreviewVisible(m.previewOpen, m.width, m.cfg.Defaults.Preview.Width)
	}
	return m.previewOpen
}

// minTableRows is the shortest the table is left with once a "bottom" preview
// has taken its share of the terminal's height - defaults.preview.heightLines
// is clamped against this so a value larger than the terminal cannot push
// every row, and the footer with them, off the screen.
const minTableRows = 5

// bottomPreviewHeight is how many lines a "bottom" pane costs vertically,
// chrome included, or 0 when the pane is not drawing there at all (closed, or
// resolved to "right" instead).
func (m Model) bottomPreviewHeight() int {
	if m.previewPosition() == "right" || !m.previewShown() {
		return 0
	}
	room := max(0, m.height-5-m.chromeLines()-minTableRows)
	return bottomPreviewChrome + min(m.cfg.Defaults.Preview.HeightLines, room)
}

// tableWidth is the pane the table gets: the whole terminal, less a "right"
// preview when one is showing. A "bottom" pane takes height, not width, so it
// never shrinks this.
func (m Model) tableWidth() int {
	if !m.previewShown() || m.previewPosition() != "right" {
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
			if msg.key == "" || msg.key != m.detailKey {
				return m, nil
			}
			m.status = msg.err.Error()
			m.detailBodyDone = true
			m.detailBodyErr = msg.err.Error()
			m.refreshDetail(false)
			return m, nil
		}
		// A reply for a row the cursor has already left must not overwrite the
		// pane with another issue's text.
		if msg.key != "" && msg.key == m.detailKey {
			if m.detailBodyErr != "" && m.status == m.detailBodyErr {
				m.status = ""
			}
			m.detailBody = msg.markdown
			m.detailBodyDone = true
			m.detailBodyErr = ""
			m.refreshDetail(false)
		}
		return m, nil

	case commentsLoadedMsg:
		if msg.err != nil {
			if msg.key == "" || msg.key != m.detailKey {
				return m, nil
			}
			m.status = msg.err.Error()
			m.detailCommentsErr = msg.err.Error()
			return m, nil
		}
		if msg.key != "" && msg.key == m.detailKey {
			if m.detailCommentsErr != "" && m.status == m.detailCommentsErr {
				m.status = ""
			}
			m.detailCommentsErr = ""
			m.detailComments = msg.comments
			m.refreshDetail(false)
		}
		return m, nil

	case tickMsg:
		if !msg.refetch {
			// A plain redraw clock: the tick itself is enough to unfreeze the ages
			// (Update returning a new model repaints), there is nothing to fetch.
			return m, m.nextTick()
		}
		// Refetch every section the way r refetches one, reusing fetchSection and
		// nextFetch so the same fetchSeq staleness guard applies to a reply that
		// lands after another refetch - manual or this tick's next lap - has
		// already superseded it. Unlike r this does not blank the rows first: r's
		// blank-while-loading is right for a refresh you asked for and are looking
		// at, but a background tick firing every few minutes should not make the
		// screen flash empty for issues that have not actually changed.
		cmds := make([]tea.Cmd, 0, len(m.sections)+2)
		for i := range m.sections {
			m.sections[i].loading = true
			cmds = append(cmds, fetchSection(m.searcher, i, m.nextFetch(i), m.sections[i].cfg))
		}
		return m, tea.Batch(append(cmds, m.spinner.Tick, m.nextTick())...)

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
				fetchSection(m.searcher, msg.idx, m.nextFetch(msg.idx), m.sections[msg.idx].cfg),
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
		m.chooseFilter = ""
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
			idx := msg.section
			if idx < 0 || idx >= len(m.sections) {
				idx = m.active
			}
			s := &m.sections[idx]
			s.loading = true
			return m, tea.Batch(fetchSection(m.searcher, idx, m.nextFetch(idx), s.cfg), m.spinner.Tick)
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
	if msg.seq != 0 && msg.seq != s.fetchSeq {
		return m
	}
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

func (m *Model) nextFetch(idx int) int {
	m.sections[idx].fetchSeq++
	return m.sections[idx].fetchSeq
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

	// Any key other than the second q/ctrl+c cancels a pending confirmQuit the
	// same way any key other than a second g disarms pendingG above - except the
	// quit case itself, which the switch below still needs to see armed.
	if m.pendingQuit && msg.String() != "q" && msg.String() != "ctrl+c" {
		m.pendingQuit = false
		if m.status == confirmQuitPrompt {
			m.status = ""
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		if m.cfg.Defaults.ConfirmQuit && !m.pendingQuit {
			m.pendingQuit = true
			m.status = confirmQuitPrompt
			return m, nil
		}
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
		return m, tea.Batch(fetchSection(m.searcher, m.active, m.nextFetch(m.active), s.cfg), m.spinner.Tick)
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
	m.chooseFilter = ""
	return m, nil
}

// visibleChoices is chooseList narrowed and ranked by chooseFilter - the
// picker's equivalent of sectionState.visible(). Empty input returns the full
// list in its original order, same as the table's own filter.
func (m Model) visibleChoices() []Choice {
	if m.chooseFilter == "" {
		return m.chooseList
	}
	type ranked struct {
		choice Choice
		rank   int64
	}
	matches := make([]ranked, 0, len(m.chooseList))
	for _, c := range m.chooseList {
		if rank, ok := fuzzyMatch(c.Name(), m.chooseFilter); ok {
			matches = append(matches, ranked{c, rank})
		}
	}
	// Stable so choices that tie on rank keep chooseList's own order, the same
	// guarantee an untyped filter gives - without it every keystroke could
	// reshuffle entries that are equally good matches, which reads as the list
	// jittering rather than narrowing.
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].rank < matches[j].rank })
	out := make([]Choice, len(matches))
	for i, r := range matches {
		out[i] = r.choice
	}
	return out
}

// fuzzyMatch reports whether every rune of query appears in label in order
// (not necessarily adjacent), case-insensitive, and if so a rank where lower
// sorts first.
//
// Rank orders by the span from first to last matched rune, then by start
// position. This distinguishes a compact later match from a scattered early
// one; ties fall back to chooseList's order via visibleChoices' stable sort.
func fuzzyMatch(label, query string) (rank int64, ok bool) {
	if query == "" {
		return 0, true
	}
	l := []rune(strings.ToLower(label))
	q := []rune(strings.ToLower(query))
	qi, start, end := 0, -1, -1
	for i, r := range l {
		if r == q[qi] {
			if start == -1 {
				start = i
			}
			end = i
			qi++
			if qi == len(q) {
				break
			}
		}
	}
	if qi != len(q) {
		return 0, false
	}
	// Compact matches sort first, then earlier matches. Packing the two values
	// into one integer keeps visibleChoices' stable sort simple.
	return int64(end-start)<<32 | int64(start), true
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

// handleChooseKey routes keys inside the picker. Movement is on the arrow
// keys only, deliberately not j/k: unlike the table's `/` filter, the picker
// has no separate mode to enter before typing takes over, so if a letter
// could mean either "move" or "narrow" a label like "Jane" would send the
// cursor flying every time its first character matched a motion key. gh-dash
// resolves the same clash the same way for its own filter-while-typing input -
// letters always fall through to the buffer, and the arrow keys keep
// movement reachable without reintroducing the ambiguity.
func (m Model) handleChooseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyDown:
		m.chooseCursor = min(m.chooseCursor+1, len(m.visibleChoices())-1)
		return m, nil
	case tea.KeyUp:
		m.chooseCursor = max(m.chooseCursor-1, 0)
		return m, nil
	case tea.KeyEnter:
		list := m.visibleChoices()
		if len(list) == 0 || m.chooseCursor < 0 || m.chooseCursor >= len(list) {
			return m, nil
		}
		key, value := m.chooseKey, list[m.chooseCursor].Value
		m.closeChoosePrompt()
		return m.runUserKeybindingWithChoice(key, value)
	case tea.KeyEsc, tea.KeyCtrlC:
		m.closeChoosePrompt()
		return m, nil
	case tea.KeyBackspace:
		if m.chooseFilter != "" {
			r := []rune(m.chooseFilter)
			m.chooseFilter = string(r[:len(r)-1])
		}
		m.clampChooseCursor()
		return m, nil
	// Space arrives as its own key type, not as a rune - the same reason
	// handleFilterKey has this case for the table's filter.
	case tea.KeySpace:
		m.chooseFilter += " "
		m.clampChooseCursor()
		return m, nil
	case tea.KeyRunes:
		m.chooseFilter += string(msg.Runes)
		m.clampChooseCursor()
		return m, nil
	}
	return m, nil
}

// clampChooseCursor keeps the cursor inside the narrowed list every time
// chooseFilter changes. Without this, filtering a list down while the cursor
// sat near its old end left it pointing past the new, shorter slice - an
// index that enter would then use to index into it.
func (m *Model) clampChooseCursor() {
	if n := len(m.visibleChoices()); m.chooseCursor >= n {
		m.chooseCursor = max(0, n-1)
	}
}

func (m *Model) closeChoosePrompt() {
	m.choosing = false
	m.chooseKey = ""
	m.chooseList = nil
	m.chooseCursor = 0
	m.chooseFilter = ""
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
