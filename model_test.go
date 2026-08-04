package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeSearcher struct {
	issues map[string][]Issue
	err    error

	// created records what the create prompt asked for, so a test can assert the
	// project and sprint were read off the row rather than typed by hand.
	created *[]NewIssueRequest
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

func testConfig() *Config {
	open := true
	return &Config{
		Sections: []Section{
			{Title: "Mine", JQL: "assignee = currentUser()", Limit: 20},
			{Title: "Sprint", JQL: "sprint in openSprints()", Limit: 20},
		},
		Defaults: Defaults{Limit: 20, Preview: Preview{Open: &open, Position: "right", Width: 0.5}},
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

func TestModelSeedsFromCacheBeforeAnyFetch(t *testing.T) {
	cache := NewCache(t.TempDir())
	key := SectionKey("assignee = currentUser()", 20)
	at := time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC)
	if err := cache.WriteSection(key, issues("ABC-1"), at); err != nil {
		t.Fatal(err)
	}

	m := NewModel(testConfig(), fakeSearcher{}, cache, fixedNow())

	if len(m.sections[0].issues) != 1 {
		t.Fatalf("want the cached row rendered immediately, got %d", len(m.sections[0].issues))
	}
	if !m.sections[0].fetchedAt.Equal(at) {
		t.Error("the cached timestamp should drive the age shown in the footer")
	}
}

func TestFetchedMsgReplacesRows(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})

	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "ABC-2"), at: time.Now()})
	m = next.(Model)

	if len(m.sections[0].issues) != 2 {
		t.Errorf("got %d issues, want 2", len(m.sections[0].issues))
	}
	if m.sections[0].loading {
		t.Error("loading should be cleared once results land")
	}
}

// A failed refresh must not blank the dashboard: stale rows beat no rows when
// Jira is down or you are off the VPN.
func TestFetchedMsgWithErrorKeepsStaleRows(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: time.Now()})
	m = next.(Model)

	next, _ = m.Update(fetchedMsg{idx: 0, err: errors.New("boom")})
	m = next.(Model)

	if len(m.sections[0].issues) != 1 {
		t.Error("rows should survive a failed refresh")
	}
	if m.sections[0].err == nil {
		t.Error("the error should be recorded for the footer")
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
		t.Errorf("G should land on the last row, got %d", m.sections[0].cursor)
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

func TestFilterNarrowsRowsWithoutRefetching(t *testing.T) {
	fake := fakeSearcher{}
	m := newTestModel(t, fake)
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "DEF-2"), at: time.Now()})
	m = next.(Model)

	m = press(m, "/")
	if !m.filtering {
		t.Fatal("/ should enter filter mode")
	}
	for _, r := range "DEF" {
		m = press(m, string(r))
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	visible := m.sections[0].visible()
	if len(visible) != 1 || visible[0].Key != "DEF-2" {
		t.Errorf("filter did not narrow the rows: %+v", visible)
	}
	if len(m.sections[0].issues) != 2 {
		t.Error("filtering must not drop the fetched rows themselves")
	}
}

func TestEscapeClearsFilter(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "DEF-2"), at: time.Now()})
	m = next.(Model)
	m.sections[0].filter = "DEF"

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.sections[0].filter != "" {
		t.Errorf("filter = %q, want empty", m.sections[0].filter)
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

func createTestModel(t *testing.T, recorded *[]NewIssueRequest) Model {
	t.Helper()
	cfg := testConfig()
	cfg.Create = []CreateKey{{Key: "c", Type: "Task"}, {Key: "C", Type: "Story"}}
	m := NewModel(cfg, fakeSearcher{created: recorded}, NewCache(t.TempDir()), fixedNow())
	m.width, m.height = 200, 40

	row := Issue{Key: "ABC-1", Summary: "a row", Sprint: []Sprint{
		{ID: 9, Name: "Team 0727-0731", State: "closed"},
		{ID: 13126, Name: "Team 0803-0807", State: "active"},
	}}
	row.Project.Key = "ABC"
	next, _ := m.Update(fetchedMsg{idx: 0, issues: []Issue{row}, at: fixedNow()()})
	return next.(Model)
}

// The whole point of creating from a tab: the project and sprint are taken from
// the row under the cursor, so the new issue lands beside what you were looking
// at. Only the summary is typed.
func TestCreateTakesProjectAndSprintFromTheRow(t *testing.T) {
	var got []NewIssueRequest
	m := createTestModel(t, &got)

	m = press(m, "c")
	if !m.creating {
		t.Fatal("c should open the create prompt")
	}
	for _, r := range "new thing" {
		m = press(m, string(r))
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("enter should submit")
	}
	cmd() // the create runs in a tea.Cmd

	if len(got) != 1 {
		t.Fatalf("create calls = %d, want 1", len(got))
	}
	want := NewIssueRequest{Project: "ABC", Type: "Task", Summary: "new thing", SprintID: 13126}
	if got[0] != want {
		t.Errorf("request = %+v, want %+v", got[0], want)
	}
	if m.creating {
		t.Error("the prompt should close on submit")
	}
}

// The key decides the type; that is how gh-dash works and it keeps the type out
// of the typed text.
func TestCreateKeyChoosesTheIssueType(t *testing.T) {
	var got []NewIssueRequest
	m := createTestModel(t, &got)

	m = press(m, "C")
	m = press(m, "x")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	_ = next
	cmd()

	if len(got) != 1 || got[0].Type != "Story" {
		t.Fatalf("request = %+v, want type Story", got)
	}
}

func TestCreateIsCancelledByEsc(t *testing.T) {
	var got []NewIssueRequest
	m := createTestModel(t, &got)

	m = press(m, "c")
	m = press(m, "x")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.creating {
		t.Error("esc should close the prompt")
	}
	if len(got) != 0 {
		t.Errorf("nothing should have been created: %+v", got)
	}
	// A cancelled draft must not come back on the next c.
	m = press(m, "c")
	if got := m.prompt.Value(); got != "" {
		t.Errorf("draft = %q, want empty", got)
	}
}

// An empty summary is rejected locally rather than sent: `jira create` requires
// -s, and the error would come back 360ms later as a CLI failure.
func TestCreateRefusesAnEmptySummary(t *testing.T) {
	var got []NewIssueRequest
	m := createTestModel(t, &got)

	m = press(m, "c")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)

	if cmd != nil {
		t.Error("an empty summary should not submit")
	}
	if !m.creating {
		t.Error("the prompt should stay open so the summary can be typed")
	}
	if len(got) != 0 {
		t.Errorf("nothing should have been created: %+v", got)
	}
}

// A tab with no rows has no project or sprint to inherit, so there is nothing
// to create from. Better to say so than to send a request with an empty -p.
func TestCreateIsRefusedOnAnEmptySection(t *testing.T) {
	cfg := testConfig()
	cfg.Create = []CreateKey{{Key: "c", Type: "Task"}}
	m := NewModel(cfg, fakeSearcher{}, NewCache(t.TempDir()), fixedNow())
	m.width, m.height = 200, 40

	m = press(m, "c")

	if m.creating {
		t.Error("the prompt should not open with no row to inherit from")
	}
	if m.status == "" {
		t.Error("the footer should say why nothing happened")
	}
}

// Once created, the row should appear without pressing r.
func TestCreatedIssueRefetchesItsSection(t *testing.T) {
	m := createTestModel(t, nil)
	next, cmd := m.Update(createdMsg{issue: Issue{Key: "NEW-1"}, idx: 0})
	m = next.(Model)

	if cmd == nil {
		t.Error("a successful create should refresh the section it went into")
	}
	if !strings.Contains(m.status, "NEW-1") {
		t.Errorf("status = %q, want the new key in it", m.status)
	}
}

func TestCreateFailureIsReportedNotSwallowed(t *testing.T) {
	m := createTestModel(t, nil)
	next, cmd := m.Update(createdMsg{err: errors.New("boom"), idx: 0})
	m = next.(Model)

	if cmd != nil {
		t.Error("a failed create should not trigger a refresh")
	}
	if !strings.Contains(m.status, "boom") {
		t.Errorf("status = %q, want the error in it", m.status)
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

// The refetch a create triggers must not blank the section: you would be
// staring at an empty list right after adding a row to it.
func TestCreateRefetchKeepsTheRowsVisible(t *testing.T) {
	m := createTestModel(t, nil)
	before := len(m.sections[0].issues)

	next, _ := m.Update(createdMsg{issue: Issue{Key: "NEW-1"}, idx: 0})
	m = next.(Model)

	if len(m.sections[0].issues) != before {
		t.Errorf("issues = %d, want the %d existing rows kept", len(m.sections[0].issues), before)
	}
}

// askTestModel is a model with one prompt: true keybinding, capturing what the
// rendered command came out as instead of running it.
func askTestModel(t *testing.T) Model {
	m := newTestModel(t, fakeSearcher{})
	m.cfg.Keybindings.Issues = []Keybinding{{
		Key: "a", Name: "ask claude", Prompt: true,
		Command: "tmux new-window claude {{.Prompt}}",
	}}
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	return settled(next.(Model))
}

// A prompt: true key must not run on the keypress - the instruction is the whole
// point, and a command launched without one is a different feature.
func TestAskKeyOpensAPromptInsteadOfRunning(t *testing.T) {
	m := askTestModel(t)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = next.(Model)

	if !m.asking {
		t.Fatal("the ask prompt should be open")
	}
	if cmd != nil {
		t.Error("nothing should run until an instruction has been typed")
	}
	if !strings.Contains(m.View(), "ask claude about ABC-1") {
		t.Errorf("the prompt should name the issue: %q", m.View())
	}
}

// An empty instruction hands over an issue and nothing to do with it. The prompt
// stays open rather than launching something pointless.
func TestAskRefusesAnEmptyInstruction(t *testing.T) {
	m := askTestModel(t)
	m = press(m, "a")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)

	if cmd != nil || !m.asking {
		t.Error("Ctrl+d on an empty instruction should do nothing and keep the box open")
	}
}

func TestAskEscapeClosesThePrompt(t *testing.T) {
	m := askTestModel(t)
	m = press(m, "a")
	for _, r := range "hi" {
		m = press(m, string(r))
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.asking || m.prompt.Value() != "" {
		t.Errorf("esc should cancel: asking=%v draft=%q", m.asking, m.prompt.Value())
	}
}

// The prompt carries the description because the preview already fetched it:
// without it the receiving end spends another ~360ms on `jira get`, and may have
// no credentials at all.
func TestAskPromptCarriesTheIssueAndTheInstruction(t *testing.T) {
	issue := Issue{Key: "ABC-1", Summary: "トークン更新で 500 が出る"}

	got := AskPrompt(issue, "## 再現手順\n\n1. ログインする", "影響範囲を調べて")

	for _, want := range []string{"ABC-1", "トークン更新で 500 が出る", "再現手順", "影響範囲を調べて"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q: %q", want, got)
		}
	}
}

// "*no description*" is this program's own words for an absent body, not
// something the issue says. Forwarding it would have Claude answer about it.
func TestAskPromptLeavesOutAnAbsentDescription(t *testing.T) {
	issue := Issue{Key: "ABC-1", Summary: "a title"}

	for _, body := range []string{"", "  ", "*no description*"} {
		if got := AskPrompt(issue, body, "do the thing"); strings.Contains(got, "no description") {
			t.Errorf("body %q leaked into the prompt: %q", body, got)
		}
	}
}

// A title and a description are free text from whoever filed the issue, and the
// prompt becomes one shell argument.
func TestAskPromptIsShellQuotedLikeEveryOtherVariable(t *testing.T) {
	issue := Issue{Key: "ABC-1", Summary: `x'; rm -rf ~ #`}

	vars := NewAskVars(issue, AskPrompt(issue, "", "go"))
	got, err := RenderCommand("claude {{.Prompt}}", vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := strings.TrimPrefix(got, "claude ")
	if !strings.HasPrefix(body, "'") || !strings.HasSuffix(body, "'") {
		t.Fatalf("the prompt was not quoted: %s", got)
	}
	if strings.Contains(strings.ReplaceAll(body[1:len(body)-1], `'\''`, ""), "'") {
		t.Errorf("quoting was broken out of: %s", got)
	}
}

// A key without prompt: true still runs at once - that shape is right for a
// fixed command, and it is what the existing keys are.
func TestAKeyWithoutPromptStillRunsImmediately(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	m.cfg.Keybindings.Issues = []Keybinding{{Key: "o", Command: "true"}}
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = settled(next.(Model))

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if next.(Model).asking {
		t.Error("a key without prompt: true must not open the prompt")
	}
	if cmd == nil {
		t.Error("it should run at once")
	}
}

// Space arrives as its own key type rather than as a rune, so without a case for
// it the filter could only ever hold one word - and a summary is where you want
// two.
func TestFilterAcceptsSpaces(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = settled(next.(Model))

	m = press(m, "/")
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("two")},
		{Type: tea.KeySpace, Runes: []rune(" ")},
		{Type: tea.KeyRunes, Runes: []rune("words")},
	} {
		next, _ = m.Update(k)
		m = next.(Model)
	}
	if m.filterDraft != "two words" {
		t.Errorf("filterDraft = %q, want %q", m.filterDraft, "two words")
	}
}
