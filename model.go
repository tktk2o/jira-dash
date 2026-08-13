package main

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// confirmQuitPrompt is what the footer says while defaults.confirmQuit is
// waiting on a second q/ctrl+c. Named so Update's set and its clear cannot say
// two different strings.
const confirmQuitPrompt = "press q again to quit"

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

	// jqlOverride is a session-only JQL typed into the search box (e), taking
	// over from cfg.JQL until cleared. Kept apart from cfg rather than written
	// into it so a tab switch and Init's config-seeded cache lookups never see
	// anything but what the file actually says - only effective() and what it
	// feeds ever see the override.
	jqlOverride string
}

// effective is cfg with JQL swapped for jqlOverride when one is set. Every
// fetch call site - manual r, the auto-refresh tick, a configured refresh:,
// and the description prefetch that follows a landed fetch - routes through
// this rather than reading cfg directly, so none of them can drift back to
// asking Jira for the config's JQL while the box shows an edited one.
func (s sectionState) effective() Section {
	if s.jqlOverride == "" {
		return s.cfg
	}
	cfg := s.cfg
	cfg.JQL = s.jqlOverride
	return cfg
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

	// prefetch holds descriptions warmed by a BulkIssues call right after a
	// section fetch lands. Keyed by issue key rather than by section: a
	// description for ABC-1 is valid regardless of which section's fetch
	// asked for it, so replies are merged in additively instead of being
	// scoped to a fetchSeq - simpler, and correct, because the thing being
	// cached (an issue's description) has no notion of which section it came
	// from. Bounded by prefetchCacheCap so a long session cannot grow it
	// without limit.
	prefetch map[string]string

	filtering   bool
	filterDraft string

	// editingJQL and jqlDraft back the search box (e): a session-only edit of
	// the active section's JQL. jqlDraft is a textinput rather than a plain
	// string for the same reason filterDraft is not one here - the box wants a
	// cursor and horizontal scroll for a query that can run past the pane's
	// width, both of which bubbles/textinput gives for free.
	editingJQL bool
	jqlDraft   textinput.Model

	// The create prompt is the one place the dashboard writes to Jira. It holds
	// only the summary: the type comes from which key opened it, and the project
	// and sprint from the row the cursor was on.
	creating   bool
	createType string
	// createParent marks a create started by a config.create entry with
	// parent: true - the new issue's parent is the row under the cursor
	// instead of the row being where the new issue merely lands beside.
	createParent bool

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
		// Same reason as prompt above: a zero textinput.Model panics the moment
		// the search box is focused.
		jqlDraft: newJQLInput(cfg.Theme),
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
		cmds = append(cmds, fetchSection(m.searcher, i, s.fetchSeq, s.effective()))
	}
	cmds = append(cmds, m.spinner.Tick, m.nextTick())
	return tea.Batch(cmds...)
}

// Update is the only place the model changes. Every case returns a new copy, so
// a command that has already been handed a model can never see a later one.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeDetail()
		m.syncJQLWidth()
		return m, nil

	case fetchedMsg:
		m = m.applyFetched(msg)
		var cmds []tea.Cmd
		// A successful landing gets its descriptions warmed for whatever the
		// user is likely to look at first, in the same batch regardless of
		// whether this section is the one on screen right now.
		if msg.err == nil {
			if keys := prefetchKeys(msg.issues); len(keys) > 0 {
				cmds = append(cmds, prefetchDescriptions(m.searcher, keys))
			}
		}
		// Rows landing in the section you are looking at is a selection change:
		// the cursor now points at an issue it did not point at before. Without
		// this the preview stayed empty until the first keypress.
		if msg.idx == m.active {
			cmds = append(cmds, m.selectionChanged())
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)

	case prefetchLoadedMsg:
		m.mergePrefetch(msg.descriptions)
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
		// A tick landing while the client is near Jira's rate limit skips the
		// fetches this lap - spending the little headroom left on a background
		// refresh nobody is waiting on is the wrong trade - but still
		// reschedules itself: the ages should keep counting up, and the next
		// lap gets to check again rather than the loop dying here.
		if m.searcher.NearRateLimit() {
			m.status = "rate limit near - refresh skipped"
			return m, m.nextTick()
		}
		if m.status == "rate limit near - refresh skipped" {
			m.status = ""
		}
		cmds := make([]tea.Cmd, 0, len(m.sections)+2)
		for i := range m.sections {
			m.sections[i].loading = true
			cmds = append(cmds, fetchSection(m.searcher, i, m.nextFetch(i), m.sections[i].effective()))
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
				fetchSection(m.searcher, msg.idx, m.nextFetch(msg.idx), m.sections[msg.idx].effective()),
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
		// selectionChanged already served the body from m.prefetch when it hit
		// - see there - so the network fetch for it is skipped entirely here;
		// only the comments still need a call.
		if m.detailBodyDone && m.detailKey == msg.key {
			return m, m.loadComments(msg.key)
		}
		return m, tea.Batch(m.loadIssue(msg.key), m.loadComments(msg.key))

	case copiedMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.status = "[" + msg.value + "] copied!"
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
			return m, tea.Batch(fetchSection(m.searcher, idx, m.nextFetch(idx), s.effective()), m.spinner.Tick)
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

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editingJQL {
		return m.handleJQLKey(msg)
	}
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
	case "e":
		m.openJQLEdit()
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
		return m, tea.Batch(fetchSection(m.searcher, m.active, m.nextFetch(m.active), s.effective()), m.spinner.Tick)
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
			return m.openCreatePrompt(ck)
		}
	}

	return m.runUserKeybinding(msg.String())
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
