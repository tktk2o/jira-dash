package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
)

var errTest = errors.New("kaboom")

func TestRenderTabsMarksTheActiveSectionWithItsCount(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "ABC-2"), at: time.Now()})
	m = next.(Model)

	out := renderTabs(m)
	if !strings.Contains(out, "Mine") || !strings.Contains(out, "Sprint") {
		t.Errorf("both tab titles should appear: %q", out)
	}
	if !strings.Contains(out, "2") {
		t.Errorf("the active tab should carry its row count: %q", out)
	}
}

func TestRenderRowHoldsKeyStatusAndAge(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	issue := Issue{Key: "ABC-1234", Summary: "トークン更新で 500 が出る", Status: "In Progress", Type: "Bug"}
	issue.Updated.Time = now.Add(-2 * time.Hour)

	row := renderRow(issue, 100, now)

	for _, want := range []string{"ABC-1234", "In Progress", "2h", TypeIcon("Bug")} {
		if !strings.Contains(row, want) {
			t.Errorf("row missing %q: %q", want, row)
		}
	}
}

// The summary column is the one that grows, so a narrow width must cut the
// summary rather than the key or the status.
func TestRenderRowKeepsKeyWhenNarrow(t *testing.T) {
	now := time.Now()
	issue := Issue{Key: "ABC-1234", Summary: strings.Repeat("長い要約", 40), Status: "Open", Type: "Task"}

	row := renderRow(issue, 60, now)

	if !strings.Contains(row, "ABC-1234") {
		t.Errorf("the key must survive truncation: %q", row)
	}
	if !strings.Contains(row, "…") {
		t.Errorf("a too-long summary should be elided: %q", row)
	}
}

// Columns only line up if padding counts display cells. A Japanese summary is
// two cells per rune, so rune-count padding would make this row a different
// width from the ASCII one.
func TestRenderRowWidthIsIndependentOfScript(t *testing.T) {
	now := time.Now()
	ascii := Issue{Key: "ABC-1", Summary: "plain summary", Status: "Open", Type: "Task"}
	japanese := Issue{Key: "ABC-2", Summary: "日本語の要約です", Status: "Open", Type: "Task"}

	a := runewidth.StringWidth(renderRow(ascii, 100, now))
	b := runewidth.StringWidth(renderRow(japanese, 100, now))
	if a != b {
		t.Errorf("row widths differ: ascii %d vs japanese %d", a, b)
	}
}

func TestCopiedMsgReportsOnTheFooter(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})

	next, _ := m.Update(copiedMsg{value: "ABC-1"})
	m = next.(Model)
	if !strings.Contains(m.status, "ABC-1") {
		t.Errorf("status = %q, want it to mention what was copied", m.status)
	}

	next, _ = m.Update(copiedMsg{value: "ABC-1", err: errTest})
	m = next.(Model)
	if !strings.Contains(m.status, "kaboom") {
		t.Errorf("status = %q, want the error", m.status)
	}
}

func TestRenderFooterShowsAgeAndCount(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	at := m.now().Add(-5 * time.Minute)
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: at})
	m = next.(Model)

	out := renderFooter(m)
	if !strings.Contains(out, "5m") {
		t.Errorf("footer should show the age: %q", out)
	}
	if !strings.Contains(out, "1") {
		t.Errorf("footer should show the issue count: %q", out)
	}
}

func TestRenderFooterShowsSectionError(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, err: errTest})
	m = next.(Model)

	if !strings.Contains(renderFooter(m), "kaboom") {
		t.Errorf("footer should surface the section error: %q", renderFooter(m))
	}
}

func TestViewRendersWithoutPanicOnEmptySection(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	if out := m.View(); out == "" {
		t.Error("View should render something even with no rows")
	}
}

func TestViewDropsPreviewOnNarrowTerminal(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	m.width = 100

	if PreviewVisible(m.previewOpen, m.width, m.cfg.Defaults.Preview.Width) {
		t.Error("a 100 column terminal should not keep a half-width preview")
	}
	if out := m.View(); out == "" {
		t.Error("View should still render")
	}
}
