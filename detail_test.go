package main

import (
	"errors"
	"regexp"

	"github.com/mattn/go-runewidth"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Moving the cursor must not fire a fetch per keystroke: a Jira REST call
// costs 0.5-1.2s, so the fetch waits for the cursor to settle.
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

	// The tick fires both halves of the pane: the body and the comments are
	// separate API calls.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("got %T, want a batch of the two loads", cmd())
	}
	if len(batch) != 2 {
		t.Fatalf("batch has %d commands, want 2", len(batch))
	}

	var sawBody, sawComments bool
	for _, c := range batch {
		switch msg := c().(type) {
		case issueLoadedMsg:
			if msg.err != nil {
				t.Fatalf("unexpected error: %v", msg.err)
			}
			if !strings.Contains(msg.markdown, "ABC-1") {
				t.Errorf("markdown = %q", msg.markdown)
			}
			sawBody = true
		case commentsLoadedMsg:
			if msg.err != nil {
				t.Fatalf("unexpected error: %v", msg.err)
			}
			sawComments = true
		default:
			t.Errorf("unexpected message %T", msg)
		}
	}
	if !sawBody || !sawComments {
		t.Errorf("body loaded: %v, comments loaded: %v; want both", sawBody, sawComments)
	}
}

// The second visit to an issue should come from cache, not a fresh API call.
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

// The preview used to be raw `jira get` markdown. The header answers "what am I
// looking at" from data the search already returned, so it costs no extra call.
func TestPreviewHeaderCarriesTheIssueAtAGlance(t *testing.T) {
	issue := Issue{
		Key: "ABC-1234", Summary: "トークン更新で 500 が出る", Type: "Story",
		Status: "In Progress", Priority: "High", StoryPoints: fptr(3),
		Labels: []string{"backend", "urgent"},
		Sprint: []Sprint{{ID: 1, Name: "Team 0803-0807", State: "active"}},
	}
	issue.Assignee = ptr("琢人 加藤")
	issue.Project.Key = "ABC"
	issue.Updated.Time = time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)

	out := renderPreviewHeader(issue, fixedNow()(), 90, newPreviewStyles(Theme{}))

	for _, want := range []string{
		"ABC-1234", "ABC", "トークン更新で 500 が出る",
		"In Progress", "High", "3", "琢人 加藤", "2h", "Team 0803-0807",
		"backend", "urgent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q:\n%s", want, out)
		}
	}
}

// Labels are chips, so an issue with none must not leave an empty frame behind.
func TestPreviewHeaderOmitsAnEmptyLabelRow(t *testing.T) {
	out := renderPreviewHeader(Issue{Key: "ABC-1"}, fixedNow()(), 90, newPreviewStyles(Theme{}))
	if strings.Contains(out, "[]") || strings.Contains(out, "labels") {
		t.Errorf("no label row should be drawn when there are none:\n%s", out)
	}
}

func TestRenderCommentsShowsAuthorAgeAndBody(t *testing.T) {
	now := fixedNow()()
	comments := []Comment{
		{Author: "甲", Body: "割付後は対象外にはできないようにする"},
		{Author: "乙", Body: "対応済み"},
	}
	comments[0].Created.Time = now.Add(-48 * time.Hour)
	comments[1].Created.Time = now.Add(-1 * time.Hour)

	out := renderComments(comments, now, 80, newPreviewStyles(Theme{}))

	for _, want := range []string{"甲", "2d", "割付後は対象外", "乙", "1h", "対応済み"} {
		if !strings.Contains(out, want) {
			t.Errorf("comments missing %q:\n%s", want, out)
		}
	}
}

// Saying "no comments" is information; a blank gap is not.
func TestRenderCommentsSaysWhenThereAreNone(t *testing.T) {
	if out := renderComments(nil, fixedNow()(), 80, newPreviewStyles(Theme{})); !strings.Contains(out, "no comments") {
		t.Errorf("out = %q", out)
	}
}

// The pane is assembled from three sources that arrive at different times: the
// header from the search (already in hand), the body from `jira get`, and the
// comments from `jira comment list`. Each has to appear as it lands.
func TestPreviewAssemblesHeaderBodyAndComments(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = settled(next.(Model))

	// Header alone, before either call comes back.
	if !strings.Contains(m.detail.View(), "ABC-1") {
		t.Errorf("the header should be on screen immediately: %q", m.detail.View())
	}

	next, _ = m.Update(issueLoadedMsg{key: "ABC-1", markdown: "the description body"})
	m = next.(Model)
	next, _ = m.Update(commentsLoadedMsg{key: "ABC-1", comments: []Comment{{Author: "甲", Body: "a remark"}}})
	m = next.(Model)

	out := plain(m.detail.View())
	for _, want := range []string{"ABC-1", "description body", "甲", "a remark"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q:\n%s", want, out)
		}
	}
}

// Most issues on a real board carry no description at all, and the pane used to
// hold "loading..." for every one of them - indistinguishable from a fetch that
// never returned, which is exactly how it was reported.
func TestPreviewSaysNoDescriptionRatherThanLoadingForever(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = settled(next.(Model))

	// Before the reply, "loading..." is the honest answer.
	if !strings.Contains(plain(m.detail.View()), "loading...") {
		t.Errorf("expected loading before the reply:\n%s", plain(m.detail.View()))
	}

	next, _ = m.Update(issueLoadedMsg{key: "ABC-1", markdown: ""})
	m = next.(Model)

	out := plain(m.detail.View())
	if strings.Contains(out, "loading...") {
		t.Errorf("the fetch finished; the pane must not still say loading:\n%s", out)
	}
	if !strings.Contains(out, "no description") {
		t.Errorf("expected 'no description':\n%s", out)
	}
}

// A failed fetch is a finished one: leaving the pane on "loading..." would
// contradict the error the footer is showing.
func TestPreviewStopsLoadingWhenTheBodyFetchFails(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = settled(next.(Model))

	next, _ = m.Update(issueLoadedMsg{key: "ABC-1", err: errors.New("boom")})
	m = next.(Model)

	if strings.Contains(plain(m.detail.View()), "loading...") {
		t.Errorf("expected the pane to stop loading after an error:\n%s", plain(m.detail.View()))
	}
	if !strings.Contains(m.status, "boom") {
		t.Errorf("status = %q, want the error", m.status)
	}
}

// A reply for a row the cursor has already left must not overwrite the pane.
func TestPreviewIgnoresACommentsReplyForAnotherIssue(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = settled(next.(Model))

	next, _ = m.Update(commentsLoadedMsg{key: "OTHER-9", comments: []Comment{{Author: "乙", Body: "stale"}}})
	m = next.(Model)

	if strings.Contains(m.detail.View(), "stale") {
		t.Errorf("a stale reply should be dropped: %q", m.detail.View())
	}
}

// Moving the cursor must not leave the previous issue's body under the new
// header.
func TestPreviewClearsTheBodyWhenTheSelectionMoves(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "ABC-2"), at: fixedNow()()})
	m = settled(next.(Model))
	next, _ = m.Update(issueLoadedMsg{key: "ABC-1", markdown: "body of the first issue"})
	m = next.(Model)

	m = press(m, "j")

	if strings.Contains(m.detail.View(), "body of the first issue") {
		t.Errorf("the old body should be gone: %q", m.detail.View())
	}
	if !strings.Contains(m.detail.View(), "ABC-2") {
		t.Errorf("the new header should be there: %q", m.detail.View())
	}
}

// Rows arriving is a selection change: the cursor points at an issue it did not
// point at before. Without that, the preview stayed empty until the first
// keypress - which is how it actually behaved.
func TestPreviewFillsWhenRowsFirstArrive(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))

	next, cmd := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = next.(Model)

	if cmd == nil {
		t.Error("arriving rows should arm the detail load")
	}
	if m.detailKey != "ABC-1" {
		t.Errorf("detailKey = %q, want ABC-1", m.detailKey)
	}
	if !strings.Contains(m.detail.View(), "ABC-1") {
		t.Errorf("the header should be on screen without a keypress: %q", m.detail.View())
	}
}

// A section that is not on screen must not hijack the preview.
func TestPreviewIgnoresRowsForAnInactiveSection(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	next, _ := m.Update(fetchedMsg{idx: 1, issues: issues("ZZZ-9"), at: fixedNow()()})
	m = next.(Model)

	if m.detailKey == "ZZZ-9" {
		t.Error("an inactive section's rows should not take over the preview")
	}
}

// plain strips the ANSI escapes glamour paints the body with. It colours each
// word separately, so a two-word phrase has escape sequences inside it and a
// plain Contains would miss text that is on screen.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

// Comments sit below a long description, so without a way to scroll the pane
// they are unreachable - which made fetching them pointless.
func TestPreviewScrolls(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = settled(next.(Model))
	next, _ = m.Update(issueLoadedMsg{
		key: "ABC-1", markdown: strings.Repeat("a line of the description\n\n", 60),
	})
	m = next.(Model)

	if m.detail.YOffset != 0 {
		t.Fatalf("the pane should start at the top, got offset %d", m.detail.YOffset)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)
	scrolled := m.detail.YOffset
	if scrolled == 0 {
		t.Fatal("ctrl+d should scroll the preview down")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(Model)
	if m.detail.YOffset >= scrolled {
		t.Errorf("ctrl+u should scroll back up: %d then %d", scrolled, m.detail.YOffset)
	}
}

// Scrolling must not move the row cursor: the pane and the table are separate
// places, and losing your row to read a comment would be maddening.
func TestPreviewScrollLeavesTheCursorAlone(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "ABC-2"), at: fixedNow()()})
	m = settled(next.(Model))
	m = press(m, "j")

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)

	if m.sections[0].cursor != 1 {
		t.Errorf("cursor = %d, want it left at 1", m.sections[0].cursor)
	}
}

// A new issue has to open at the top. The viewport keeps its offset across a
// SetContent, so without a reset the next issue opens scrolled to wherever the
// last one was left.
func TestPreviewReturnsToTheTopOnANewSelection(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "ABC-2"), at: fixedNow()()})
	m = settled(next.(Model))
	next, _ = m.Update(issueLoadedMsg{
		key: "ABC-1", markdown: strings.Repeat("a line of the description\n\n", 60),
	})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)

	m = press(m, "j")

	if m.detail.YOffset != 0 {
		t.Errorf("offset = %d, want the new issue to open at the top", m.detail.YOffset)
	}
}

// The preview had the same flatness the rows did: every line one colour, so
// nothing separated the title from the facts under it or a comment's author from
// its body. These are the weights read off gh-dash's own pane.
//
// Asserted on the styles, not on rendered output: lipgloss strips colour when it
// is not writing to a terminal, so under `go test` the pane renders bare.
func TestPreviewWeightsItsBlocks(t *testing.T) {
	ps := newPreviewStyles(Theme{})

	if !ps.title.GetBold() {
		t.Error("the title should be the bold thing in the header")
	}
	if ps.identity.GetForeground() == ps.title.GetForeground() {
		t.Error("the identity line should recede behind the title")
	}
	if !ps.heading.GetUnderline() || !ps.heading.GetBold() {
		t.Error("a block heading should be bold and underlined, like gh-dash's")
	}
	if ps.rule.GetForeground() == ps.meta.GetForeground() {
		t.Error("the rules should be fainter than the text they divide")
	}
	if ps.age.GetForeground() == ps.meta.GetForeground() {
		t.Error("the age should be its own weight, as it is in a row")
	}
}

// Widths are measured on the plain text, so a segment styled before it is cut
// would count its escape sequences as cells and overflow the pane.
func TestPreviewHeaderNeverExceedsItsWidth(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	issue := Issue{
		Key: "ABC-1234", Type: "サブタスク", Status: "In Progress",
		Summary: strings.Repeat("長い要約", 30), Priority: "Highest", Labels: []string{"設計", "調査"},
	}
	issue.Assignee = ptr("琢人 加藤")
	issue.Updated.Time = now.Add(-2 * time.Hour)
	issue.Project.Key = "ABC"

	for _, width := range []int{80, 40, 20, 8} {
		for _, line := range strings.Split(
			plain(renderPreviewHeader(issue, now, width, newPreviewStyles(Theme{}))), "\n") {
			if got := runewidth.StringWidth(line); got > width {
				t.Errorf("width %d: a header line is %d cells: %q", width, got, line)
			}
		}
	}
}

// A comment line is the same two-part layout as the header's meta line, and had
// the same defect: only the author half was budgeted, so a narrow pane kept the
// age at full length and overflowed.
func TestCommentLinesNeverExceedTheirWidth(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	c := Comment{Author: "琢人 加藤", Body: "b"}
	c.Created.Time = now.Add(-3 * time.Hour)

	for _, width := range []int{80, 20, 6, 3} {
		for _, line := range strings.Split(
			plain(renderComments([]Comment{c}, now, width, newPreviewStyles(Theme{}))), "\n") {
			if got := runewidth.StringWidth(line); got > width {
				t.Errorf("width %d: %q is %d cells", width, line, got)
			}
		}
	}
}
