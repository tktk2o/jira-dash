package main

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Moving the cursor must not fire a fetch per keystroke: each jira call costs
// ~360ms, so the fetch waits for the cursor to settle.
func TestSelectionChangedDebouncesRatherThanFetching(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "ABC-2"), at: time.Now()})
	m = next.(Model)

	before := m.detailSeq
	m = press(m, "j")
	if m.detailSeq == before {
		t.Error("moving the cursor should bump the debounce sequence")
	}

	m2 := press(m, "j")
	if m2.detailSeq == m.detailSeq {
		t.Error("a second move should bump it again, invalidating the first tick")
	}
}

// A tick from a row we have already left must be dropped.
func TestStaleDebounceTickIsIgnored(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: time.Now()})
	m = next.(Model)
	m.detailSeq = 7

	_, cmd := m.Update(debounceMsg{seq: 3, key: "ABC-1"})
	if cmd != nil {
		t.Error("a stale tick should produce no command")
	}
}

func TestCurrentDebounceTickLoadsTheIssue(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: time.Now()})
	m = next.(Model)
	m.detailSeq = 4

	_, cmd := m.Update(debounceMsg{seq: 4, key: "ABC-1"})
	if cmd == nil {
		t.Fatal("the current tick should trigger a load")
	}

	msg, ok := cmd().(issueLoadedMsg)
	if !ok {
		t.Fatalf("got %T, want issueLoadedMsg", cmd())
	}
	if msg.err != nil {
		t.Fatalf("unexpected error: %v", msg.err)
	}
	if !strings.Contains(msg.markdown, "ABC-1") {
		t.Errorf("markdown = %q", msg.markdown)
	}
}

// The second visit to an issue should come from cache, not the CLI.
func TestLoadIssueUsesTheCache(t *testing.T) {
	cache := NewCache(t.TempDir())
	if err := cache.WriteIssue("ABC-1", "# cached body\n"); err != nil {
		t.Fatal(err)
	}
	// time.Now, not fixedNow: the TTL is measured against the file's mtime,
	// which is the real clock. A fixed "now" hours away from it turns a
	// just-written entry into a miss and the test starts depending on what
	// time of day it runs.
	m := NewModel(testConfig(), fakeSearcher{err: errTest}, cache, time.Now)

	cmd := m.loadIssue("ABC-1")
	if cmd == nil {
		t.Fatal("want a command")
	}

	msg := cmd().(issueLoadedMsg)
	if msg.err != nil {
		t.Fatalf("a cache hit must not reach the failing searcher: %v", msg.err)
	}
	if !strings.Contains(msg.markdown, "cached body") {
		t.Errorf("markdown = %q", msg.markdown)
	}
}

func TestLoadIssueSurfacesFetchError(t *testing.T) {
	m := NewModel(testConfig(), fakeSearcher{err: errTest}, NewCache(t.TempDir()), fixedNow())

	msg := m.loadIssue("ABC-9")().(issueLoadedMsg)
	if msg.err == nil {
		t.Fatal("want the searcher error")
	}
}

// The viewport is created 0x0, so a resize has to give it a size or the
// preview pane renders nothing however good the markdown is.
func TestWindowSizeGivesTheDetailPaneASize(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})

	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	m = next.(Model)

	if m.detail.Width <= 0 {
		t.Errorf("detail width = %d, want the preview's share of 200", m.detail.Width)
	}
	if m.detail.Height <= 0 {
		t.Errorf("detail height = %d, want most of 40", m.detail.Height)
	}
	if m.detail.Width >= m.width {
		t.Errorf("detail width = %d, should be a fraction of %d", m.detail.Width, m.width)
	}
}

func TestRenderMarkdownFallsBackToPlainText(t *testing.T) {
	if got := renderMarkdown("# heading\n\nbody", 60); !strings.Contains(got, "heading") {
		t.Errorf("rendered output lost the content: %q", got)
	}
	if got := renderMarkdown("plain", 0); got == "" {
		t.Error("a zero width must not produce empty output")
	}
}
