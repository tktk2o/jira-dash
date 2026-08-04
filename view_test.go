package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// Bold plus a row count was too weak a cue to notice: switching sections read
// as "tab does nothing" on a live dashboard, because both sections opened on
// the same first issue. The active tab carries the selection background, the
// same colour the selected row uses.
func TestActiveTabIsFilledNotJustBold(t *testing.T) {
	st := newStyles(Theme{})

	if got := st.activeTab.GetBackground(); got != st.selectedRow.GetBackground() {
		t.Errorf("active tab background = %v, want the selected-row background %v",
			got, st.selectedRow.GetBackground())
	}
	if st.inactiveTab.GetBackground() == st.activeTab.GetBackground() {
		t.Error("inactive tab must not share the active tab's background")
	}
}

// The preview used to run straight into the table with nothing between them.
// The border style was built and never applied, so the documented border colour
// had no effect at all.
func TestPreviewIsDrawnInsideABorder(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	m = next.(Model)
	next, _ = m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: time.Now()})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╰") {
		t.Errorf("the preview pane should be framed by a rounded border: %q", out)
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

// A row must never draw wider than the width it was handed, or the table
// spills past its pane and pushes the preview off screen. The fixed columns
// alone are 35 cells, so the narrow cases are the interesting ones.
func TestRenderRowNeverExceedsItsWidth(t *testing.T) {
	now := time.Now()
	issue := Issue{Key: "ABC-1234", Summary: strings.Repeat("長い要約", 40), Status: "In Progress", Type: "Bug"}

	for _, width := range []int{120, 100, 60, 45, 40, 20, 5} {
		if got := runewidth.StringWidth(renderRow(issue, width, now)); got > width {
			t.Errorf("renderRow at width %d rendered %d cells", width, got)
		}
	}
}

// View draws the cursor marker itself, so the marker plus the row has to fit
// the pane - otherwise every line is two cells too wide.
func TestViewRowsFitTheTableWidth(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	m.width, m.previewOpen = 200, false
	next, _ := m.Update(fetchedMsg{
		idx:    0,
		issues: []Issue{{Key: "ABC-1", Summary: strings.Repeat("長い要約", 40), Status: "Open", Type: "Bug"}},
		at:     time.Now(),
	})
	m = next.(Model)

	for _, line := range strings.Split(m.View(), "\n") {
		if got := runewidth.StringWidth(line); got > m.width {
			t.Errorf("a rendered line is %d cells wide, want <= %d: %q", got, m.width, line)
		}
	}
}

func TestCommandRanMsgReportsFailureOnly(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})

	next, _ := m.Update(commandRanMsg{key: "R", err: errTest})
	m = next.(Model)
	if !strings.Contains(m.status, "kaboom") || !strings.Contains(m.status, "R") {
		t.Errorf("status = %q, want the key and the error", m.status)
	}

	next, _ = m.Update(commandRanMsg{key: "R"})
	m = next.(Model)
	if m.status != "" {
		t.Errorf("status = %q, want it cleared on success", m.status)
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

// The project and sprint are inherited, not typed, so the prompt has to show
// where the issue will land before enter is pressed.
func TestCreatePromptNamesItsTarget(t *testing.T) {
	var got []NewIssueRequest
	m := createTestModel(t, &got)
	m = press(m, "c")
	for _, r := range "hi" {
		m = press(m, string(r))
	}

	line := renderCreatePrompt(m)
	for _, want := range []string{"Task", "ABC", "Team 0803-0807", "hi"} {
		if !strings.Contains(line, want) {
			t.Errorf("prompt %q is missing %q", line, want)
		}
	}
}

// It replaces the filter line rather than adding one, so opening it must not
// make the view taller and push the table up.
func TestCreatePromptDoesNotAddALine(t *testing.T) {
	var got []NewIssueRequest
	m := createTestModel(t, &got)
	before := strings.Count(m.View(), "\n")

	m = press(m, "c")

	if after := strings.Count(m.View(), "\n"); after != before+1 {
		t.Errorf("view went from %d to %d lines; the prompt should add exactly one",
			before, after)
	}
}
