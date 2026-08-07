package main

import (
	"context"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
	"time"
)

type fakeSearcher struct {
	issues map[string][]Issue
	err    error

	// created records what the create prompt asked for, so a test can assert the
	// project and sprint were read off the row rather than typed by hand.
	created *[]NewIssueRequest

	// transitions and assignableUsers back choicesFrom: transitions/assignees.
	// Separate from err so a test can make Search succeed (there are rows to
	// pick from) while the choices call itself fails.
	transitions     []Transition
	assignableUsers []User
	choicesErr      error
	// transitionsCalledWith and assignableUsersCalledWith record the issue key
	// each was called with, so a test can assert the fetch went out for the row
	// under the cursor rather than some other one.
	transitionsCalledWith     *[]string
	assignableUsersCalledWith *[]string

	// bulkIssues backs BulkIssues; bulkErr makes it fail without disturbing
	// err/choicesErr, which other calls already use. bulkCalledWith records
	// the keys each call went out with.
	bulkIssues     map[string]string
	bulkErr        error
	bulkCalledWith *[][]string
	nearRateLimit  bool
}

func (f fakeSearcher) Create(_ context.Context, req NewIssueRequest) (Issue, error) {
	if f.created != nil {
		*f.created = append(*f.created, req)
	}
	if f.err != nil {
		return Issue{}, f.err
	}
	return Issue{Key: "NEW-1", URL: "https://example.atlassian.net/browse/NEW-1"}, nil
}

func (f fakeSearcher) Search(_ context.Context, jql string, _ int) ([]Issue, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.issues[jql], nil
}

func (f fakeSearcher) Issue(_ context.Context, key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "# " + key, nil
}

func (f fakeSearcher) Comments(_ context.Context, key string) ([]Comment, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []Comment{{ID: "1", Author: "甲", Body: "a comment on " + key}}, nil
}

func (f fakeSearcher) Transitions(_ context.Context, key string) ([]Transition, error) {
	if f.transitionsCalledWith != nil {
		*f.transitionsCalledWith = append(*f.transitionsCalledWith, key)
	}
	if f.choicesErr != nil {
		return nil, f.choicesErr
	}
	return f.transitions, nil
}

func (f fakeSearcher) AssignableUsers(_ context.Context, issueKey, _ string) ([]User, error) {
	if f.assignableUsersCalledWith != nil {
		*f.assignableUsersCalledWith = append(*f.assignableUsersCalledWith, issueKey)
	}
	if f.choicesErr != nil {
		return nil, f.choicesErr
	}
	return f.assignableUsers, nil
}

func (f fakeSearcher) BulkIssues(_ context.Context, keys []string) (map[string]string, error) {
	if f.bulkCalledWith != nil {
		*f.bulkCalledWith = append(*f.bulkCalledWith, keys)
	}
	if f.bulkErr != nil {
		return nil, f.bulkErr
	}
	return f.bulkIssues, nil
}

func (f fakeSearcher) NearRateLimit() bool {
	return f.nearRateLimit
}

func testConfig() *Config {
	open, showCount := true, true
	refetch := 0 // tests drive ticks explicitly; a running timer would race them
	return &Config{
		Sections: []Section{
			{Title: "Mine", JQL: "assignee = currentUser()", Limit: 20},
			{Title: "Sprint", JQL: "sprint in openSprints()", Limit: 20},
		},
		Defaults: Defaults{
			Limit:                  20,
			Preview:                Preview{Open: &open, Position: "right", Width: 0.5, HeightLines: 15},
			RefetchIntervalMinutes: &refetch,
			SectionsShowCount:      &showCount,
			// Every column, the same default LoadConfig fills in for an omitted
			// defaults.columns key - tests here build a Config by hand, bypassing
			// that default, so it has to be named explicitly.
			Columns: append([]string{}, columnNames...),
		},
	}
}

func fixedNow() func() time.Time {
	at := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return at }
}

func newTestModel(t *testing.T, s Searcher) Model {
	t.Helper()
	m := NewModel(testConfig(), s, NewCache(t.TempDir()), fixedNow())
	// Through a real terminal the size always arrives as a message, and it is
	// what sizes the preview viewport - setting the fields alone left it 0x0,
	// which renders nothing at all.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	return next.(Model)
}

func issues(keys ...string) []Issue {
	out := make([]Issue, 0, len(keys))
	for _, k := range keys {
		out = append(out, Issue{Key: k, Summary: "summary of " + k, Status: "Open", Type: "Task"})
	}
	return out
}

func TestModelStartsOnTheFirstSection(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	if m.active != 0 {
		t.Errorf("active = %d, want 0", m.active)
	}
	if len(m.sections) != 2 {
		t.Fatalf("got %d sections, want 2", len(m.sections))
	}
	if m.sections[0].cacheKey != SectionKey("assignee = currentUser()", 20) {
		t.Error("each section should carry the cache key for its query")
	}
}

// One bad JQL must not take the other tabs with it.
func TestErrorIsScopedToItsSection(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 1, err: errors.New("bad jql")})
	m = next.(Model)

	if m.sections[0].err != nil {
		t.Error("section 0 should be unaffected")
	}
	if m.sections[1].err == nil {
		t.Error("section 1 should hold the error")
	}
}

func TestTabSwitchesSections(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.active != 1 {
		t.Errorf("active = %d, want 1", m.active)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if m.active != 0 {
		t.Errorf("tab should wrap around to 0, got %d", m.active)
	}
}

// gh-dash switches sections with h/l and the arrows, and this dashboard is
// meant to feel like it. Only tab/shift+tab were bound, so reaching for l did
// nothing and the sections looked stuck.
func TestSectionSwitchesOnHLAndArrows(t *testing.T) {
	forward := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'l'}},
		{Type: tea.KeyRight},
	}
	back := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'h'}},
		{Type: tea.KeyLeft},
	}

	for i, key := range forward {
		m := newTestModel(t, fakeSearcher{})
		next, _ := m.Update(key)
		if got := next.(Model).active; got != 1 {
			t.Errorf("forward key %d: active = %d, want 1", i, got)
		}
	}
	for i, key := range back {
		m := newTestModel(t, fakeSearcher{})
		next, _ := m.Update(key)
		if got := next.(Model).active; got != 1 {
			t.Errorf("back key %d should wrap to the last section: active = %d, want 1", i, got)
		}
	}
}

func TestCursorMovesAndClamps(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "ABC-2"), at: time.Now()})
	m = next.(Model)

	m = press(m, "j")
	if m.sections[0].cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.sections[0].cursor)
	}

	m = press(m, "j")
	if m.sections[0].cursor != 1 {
		t.Errorf("cursor should stop at the last row, got %d", m.sections[0].cursor)
	}

	m = press(m, "k")
	m = press(m, "k")
	if m.sections[0].cursor != 0 {
		t.Errorf("cursor should stop at 0, got %d", m.sections[0].cursor)
	}
}

func TestGotoTopAndBottom(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("A-1", "A-2", "A-3"), at: time.Now()})
	m = next.(Model)

	m = press(m, "G")
	if m.sections[0].cursor != 2 {
		t.Errorf("cursor after G = %d, want 2 (the last of three rows)", m.sections[0].cursor)
	}

	m = press(m, "g")
	m = press(m, "g")
	if m.sections[0].cursor != 0 {
		t.Errorf("gg should land on the first row, got %d", m.sections[0].cursor)
	}
}

// vim semantics: an intervening key disarms a half-typed gg. Without this the
// stale arm makes a later single g jump to the top.
func TestInterveningKeyDisarmsGG(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("A-1", "A-2", "A-3"), at: time.Now()})
	m = next.(Model)
	m = press(m, "G")

	m = press(m, "g")
	m = press(m, "j")
	m = press(m, "g")

	if m.sections[0].cursor == 0 {
		t.Error("g j g must not jump to the top; the first g should have been disarmed by j")
	}
}

func TestSelectedFollowsTheFilter(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "DEF-2"), at: time.Now()})
	m = next.(Model)
	m.sections[0].filter = "DEF"
	m.sections[0].cursor = 0

	got, ok := m.sections[0].selected()
	if !ok || got.Key != "DEF-2" {
		t.Errorf("selected = %+v, ok = %v", got, ok)
	}
}

func TestSelectedOnEmptySection(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	if _, ok := m.sections[0].selected(); ok {
		t.Error("an empty section has nothing selected")
	}
}

func TestPreviewToggle(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	before := m.previewOpen

	m = press(m, "p")
	if m.previewOpen == before {
		t.Error("p should toggle the preview")
	}
}

func TestQuitReturnsQuitCmd(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q should return a command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("expected tea.Quit's message")
	}
}

func press(m Model, key string) Model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(Model)
}

// The prefix narrows a section the same way the local filter does, so the tab
// count and the cursor both see the narrowed list.
func TestSprintPrefixNarrowsTheSection(t *testing.T) {
	mine := Issue{Key: "ABC-1", Sprint: []Sprint{{Name: "Team 0803-0807", State: "active"}}}
	theirs := Issue{Key: "ABC-2", Sprint: []Sprint{{Name: "Other 0803-0807", State: "active"}}}

	s := sectionState{cfg: Section{SprintPrefix: "Team"}, issues: []Issue{mine, theirs}}
	got := s.visible()
	if len(got) != 1 || got[0].Key != "ABC-1" {
		t.Fatalf("visible = %+v, want only ABC-1", got)
	}

	// The local filter composes with it rather than replacing it.
	s.filter = "abc-2"
	if got := s.visible(); len(got) != 0 {
		t.Errorf("filter and prefix should both apply: %+v", got)
	}
}

// The animation has to be driven by ticks, and the loop must stop when there is
// nothing to animate: an idle dashboard that keeps waking up to redraw burns
// CPU for no reason.
func TestSpinnerTicksOnlyWhileSomethingLoads(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	m.sections[0].loading = true

	_, cmd := m.Update(m.spinner.Tick())
	if cmd == nil {
		t.Error("a tick should schedule the next one while a section loads")
	}

	m.sections[0].loading = false
	_, cmd = m.Update(m.spinner.Tick())
	if cmd != nil {
		t.Error("the tick loop should stop once nothing is loading")
	}
}

// A refresh started after the loop went idle has to restart it, or the spinner
// sits frozen on one frame.
func TestRefreshRestartsTheSpinner(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = settled(next.(Model))

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("r should return commands")
	}
	// A tea.Batch is opaque, so the check is that the section is marked loading
	// and that a tick then keeps the loop alive.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = next.(Model)
	if !m.sections[0].loading {
		t.Error("r should mark the section loading")
	}
	if _, cmd := m.Update(m.spinner.Tick()); cmd == nil {
		t.Error("the tick loop should run again after a refresh")
	}
}

// NewModel starts every section loading, since Init fetches them all. A test
// about the idle state has to clear all of them, not just the one it touches.
func settled(m Model) Model {
	for i := range m.sections {
		m.sections[i].loading = false
	}
	return m
}

// gh-dash blanks the list on an explicit refresh and says it is loading, so
// there is never a moment where old rows look current. Failure is the price:
// there are no stale rows left to fall back on, which is why only r does this -
// a background refetch after a create keeps the rows you are looking at.
func TestExplicitRefreshClearsTheRows(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "ABC-2"), at: fixedNow()()})
	m = settled(next.(Model))
	m = press(m, "j")

	m = press(m, "r")

	if len(m.sections[0].issues) != 0 {
		t.Errorf("issues = %d, want the list cleared", len(m.sections[0].issues))
	}
	if !m.sections[0].loading {
		t.Error("the section should be marked loading")
	}
	if m.sections[0].cursor != 0 {
		t.Errorf("cursor = %d, want 0: the row it pointed at is gone", m.sections[0].cursor)
	}
	if m.detailKey != "" {
		t.Errorf("detailKey = %q, want the preview cleared with the list", m.detailKey)
	}
}

// promptLines and View's own switch (see the comments on both) are two
// separate lists of the same prompt modes, kept in sync by hand rather than by
// the type system. This walks every mode the model can be in and checks that
// the number of lines View actually draws for the prompt/filter area matches
// what promptLines reports for it - the mechanical guard the comments point
// at, so a mode added to one switch but not the other fails a test instead of
// silently pushing the footer off-screen.
func TestPromptLinesMatchesEveryPromptMode(t *testing.T) {
	t.Run("creating", func(t *testing.T) {
		m := press(createTestModel(t, nil), "c")
		box := renderPromptBox(m, createPromptTitle(m), m.prompt.View(),
			"Ctrl+d submit ⋅ esc cancel", m.tableWidth())
		assertPromptLinesMatches(t, m, box)
	})
	t.Run("asking", func(t *testing.T) {
		m := press(askTestModel(t), "a")
		box := renderPromptBox(m, askPromptTitle(m), m.prompt.View(),
			"Ctrl+d submit ⋅ enter newline ⋅ esc cancel", m.tableWidth())
		assertPromptLinesMatches(t, m, box)
	})
	t.Run("choosing", func(t *testing.T) {
		m := press(chooseTestModel(t), "A")
		box := renderPromptBox(m, choosePromptTitle(m),
			renderChoices(m, max(0, m.tableWidth()-6)),
			"type to filter ⋅ ↑/↓ move ⋅ enter select ⋅ esc cancel", m.tableWidth())
		assertPromptLinesMatches(t, m, box)
	})
	t.Run("filtering", func(t *testing.T) {
		m := newTestModel(t, fakeSearcher{})
		m = press(m, "/")
		if got, want := m.promptLines(), 1; got != want {
			t.Errorf("promptLines() = %d, want %d", got, want)
		}
	})
	t.Run("section filter set", func(t *testing.T) {
		m := newTestModel(t, fakeSearcher{})
		m.sections[m.active].filter = "x"
		if got, want := m.promptLines(), 1; got != want {
			t.Errorf("promptLines() = %d, want %d", got, want)
		}
	})
	t.Run("idle", func(t *testing.T) {
		m := newTestModel(t, fakeSearcher{})
		if got, want := m.promptLines(), 0; got != want {
			t.Errorf("promptLines() = %d, want %d", got, want)
		}
	})
}

// assertPromptLinesMatches checks that a rendered prompt box is exactly as
// tall as promptLines() says it is - the two disagreeing is exactly how the
// footer gets pushed off-screen, since tableHeight is sized from promptLines
// rather than from the box itself.
func assertPromptLinesMatches(t *testing.T, m Model, box string) {
	t.Helper()
	if got, want := strings.Count(box, "\n")+1, m.promptLines(); got != want {
		t.Errorf("the box drew %d lines, promptLines says %d", got, want)
	}
}

// A comment that does not appear until the cursor leaves the row and comes back
// reads as a comment that failed to post.
func TestRefreshKeybindingReloadsThePreviewAfterItRan(t *testing.T) {
	for _, tc := range []struct {
		refresh bool
		want    bool
	}{{true, true}, {false, false}} {
		m := askTestModel(t)

		_, cmd := m.Update(commandRanMsg{key: "a", refresh: tc.refresh})

		if got := cmd != nil; got != tc.want {
			t.Errorf("refresh=%v: reloaded=%v, want %v", tc.refresh, got, tc.want)
		}
	}
}

// The second q/ctrl+c within the same prompt is what actually quits when
// confirmQuit is on; the first only arms it and reports the prompt in status.
func TestConfirmQuitNeedsTwoPresses(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	m.cfg.Defaults.ConfirmQuit = true

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if cmd != nil {
		t.Error("the first q with confirmQuit on should not quit yet")
	}
	if !m.pendingQuit {
		t.Error("the first q should arm pendingQuit")
	}
	if m.status != confirmQuitPrompt {
		t.Errorf("status = %q, want the confirm prompt", m.status)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("the second q should quit")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("expected tea.Quit's message")
	}
}

// Any key other than the second q/ctrl+c cancels a pending confirmQuit, the
// same way any key other than a second g disarms gg.
func TestConfirmQuitIsCancelledByAnyOtherKey(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	m.cfg.Defaults.ConfirmQuit = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if !m.pendingQuit {
		t.Fatal("the first q should arm pendingQuit")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(Model)
	if m.pendingQuit {
		t.Error("any other key should cancel a pending confirmQuit")
	}
	if m.status == confirmQuitPrompt {
		t.Error("cancelling should clear the confirm prompt out of status")
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if cmd != nil {
		t.Error("q after a cancelled confirmQuit should arm it again, not quit")
	}
}

// Without confirmQuit configured, q must still quit on the first press - the
// feature must not change the default behaviour.
func TestQuitsImmediatelyWithoutConfirmQuit(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q should quit at once when confirmQuit is off")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("expected tea.Quit's message")
	}
}
