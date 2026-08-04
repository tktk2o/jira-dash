package main

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeSearcher struct {
	issues map[string][]Issue
	err    error
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
	m.width, m.height = 200, 40
	return m
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
