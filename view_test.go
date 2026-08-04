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

// Columns only line up if padding counts display cells. A Japanese assignee is
// two cells per rune, so rune-count padding would start the summary column at a
// different place on this row than on the ASCII one.
func TestRenderRowColumnsAlignAcrossScripts(t *testing.T) {
	now := time.Now()
	ascii := Issue{Key: "ABC-1", Status: "Open", Type: "Task", Assignee: ptr("Alex Kim")}
	japanese := Issue{Key: "ABC-2", Status: "Open", Type: "Task", Assignee: ptr("琢人 加藤")}

	// The summary is empty on both, so whatever the row measures is exactly where
	// the fixed columns end.
	a := runewidth.StringWidth(renderRow(ascii, 100, now))
	b := runewidth.StringWidth(renderRow(japanese, 100, now))
	if a != b {
		t.Errorf("summary column starts at %d cells for ascii but %d for japanese", a, b)
	}
}

// A row must never draw wider than the width it was handed, or the table
// spills past its pane and pushes the preview off screen. The fixed columns
// alone are 46 cells, so the narrow cases are the interesting ones.
func TestRenderRowNeverExceedsItsWidth(t *testing.T) {
	now := time.Now()
	issue := Issue{
		Key: "ABC-1234", Summary: strings.Repeat("長い要約", 40),
		Status: "In Progress", Type: "Bug", Priority: "Highest",
	}

	for _, width := range []int{120, 100, 60, 46, 45, 40, 20, 5} {
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

// "refreshing" as a static word gave no sign the dashboard was alive while the
// CLI spent its 360ms of startup. The footer carries the animated frame.
func TestFooterShowsTheSpinnerWhileTheSectionLoads(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	m.sections[0].loading = true

	if got := renderFooter(m); !strings.Contains(got, m.spinner.View()) {
		t.Errorf("footer %q should carry the spinner frame %q", got, m.spinner.View())
	}

	m.sections[0].loading = false
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = next.(Model)
	if got := renderFooter(m); strings.Contains(got, "refreshing") {
		t.Errorf("footer %q should stop saying refreshing once results land", got)
	}
}

// On startup every section fetches at once, so which tabs are still waiting is
// only visible from the tab strip.
func TestTabStripMarksALoadingSection(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	m.sections[1].loading = true

	if got := renderTabs(m); !strings.Contains(got, m.spinner.View()) {
		t.Errorf("tabs %q should mark the loading section", got)
	}

	m.sections[1].loading = false
	if got := renderTabs(m); strings.Contains(got, m.spinner.View()) {
		t.Errorf("tabs %q should carry no spinner when nothing is loading", got)
	}
}

// A cleared list must not read as "(no issues)" - that is a different fact, and
// the wrong one during a refresh.
func TestEmptySectionSaysLoadingWhileFetching(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	m.sections[0].loading = true

	out := m.View()
	if !strings.Contains(out, "loading") {
		t.Errorf("view should say it is loading: %q", out)
	}
	if strings.Contains(out, "(no issues)") {
		t.Errorf("a loading section must not claim to be empty: %q", out)
	}

	m.sections[0].loading = false
	if out := m.View(); !strings.Contains(out, "(no issues)") {
		t.Errorf("a settled empty section should say so: %q", out)
	}
}

// gh-dash puts a count on every tab, not just the one you are on: with four
// sections fetching at once, the counts are how you see what arrived.
func TestEveryTabCarriesItsCount(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("A-1", "A-2"), at: fixedNow()()})
	m = settled(next.(Model))
	next, _ = m.Update(fetchedMsg{idx: 1, issues: issues("B-1"), at: fixedNow()()})
	m = settled(next.(Model))

	out := renderTabs(m)
	if !strings.Contains(out, "Mine (2)") {
		t.Errorf("active tab should show its count: %q", out)
	}
	if !strings.Contains(out, "Sprint (1)") {
		t.Errorf("an inactive tab should show its count too: %q", out)
	}
}

// The JQL is what a section *is*, and it is otherwise invisible: two tabs can
// look alike and query completely different things.
func TestQueryLineShowsTheJQLAndThePrefix(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	m.sections[0].cfg.SprintPrefix = "Team"

	line := renderQueryLine(m, 120)

	if !strings.Contains(line, "assignee = currentUser()") {
		t.Errorf("the query line should carry the JQL: %q", line)
	}
	if !strings.Contains(line, "Team") {
		t.Errorf("it should carry the sprint prefix, which narrows the JQL: %q", line)
	}
}

// As a bordered box this spent three of the screen's lines - and truncated the
// JQL anyway - on a fact that never changes while you are on the tab.
func TestQueryLineIsOneUnframedLine(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	m.sections[0].cfg.JQL = strings.Repeat("project = ABCDEF AND ", 20)

	for _, w := range []int{40, 80, 120} {
		out := renderQueryLine(m, w)
		if strings.Contains(out, "\n") {
			t.Errorf("width %d: the query should stay on one line: %q", w, out)
		}
		if strings.ContainsAny(out, "╭╰│") {
			t.Errorf("width %d: the query line should carry no frame: %q", w, out)
		}
		if got := runewidth.StringWidth(out); got > w {
			t.Errorf("width %d: line is %d cells: %q", w, got, out)
		}
	}
}

// The header names the columns the row lays out, so it has to use the same
// widths - otherwise it is worse than nothing.
func TestColumnHeaderAlignsWithTheRow(t *testing.T) {
	header := renderColumnHeader(120)
	if got := runewidth.StringWidth(header); got > 120 {
		t.Errorf("header is %d cells, want at most 120", got)
	}
	for _, want := range []string{"KEY", "STATUS", "SUMMARY"} {
		if !strings.Contains(header, want) {
			t.Errorf("header should name the %s column: %q", want, header)
		}
	}
}

// One line per issue. Two lines plus a rule between them meant three lines each,
// so a 41-issue backlog showed eleven of them.
func TestRenderRowIsOneLine(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	issue := Issue{
		Key: "ABC-1234", Type: "Story", Status: "In Progress",
		Summary: "トークン更新で 500 が出る問題の調査", StoryPoints: fptr(3),
	}
	issue.Assignee = ptr("琢人 加藤")
	issue.Updated.Time = now.Add(-2 * time.Hour)

	row := renderRow(issue, 100, now)

	if strings.Contains(row, "\n") {
		t.Fatalf("a row should be one line: %q", row)
	}
	for _, want := range []string{"ABC-1234", "In Progress", "2h", "3", "加藤", "トークン更新"} {
		if !strings.Contains(row, want) {
			t.Errorf("row missing %q: %q", want, row)
		}
	}
}

// Story points are often unset, and "0" reads as a real estimate.
func TestRenderRowMarksAbsentStoryPointsWithADash(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	row := renderRow(Issue{Key: "ABC-1"}, 100, now)

	if strings.Contains(row, "0") {
		t.Errorf("an unset story point count must not render as 0: %q", row)
	}
	if !strings.Contains(row, "-") {
		t.Errorf("unset story points should show a dash: %q", row)
	}
}

// Every issue Jira creates is Medium unless someone changed it, so as a column
// it was the same word on every row. It earns its space only when it differs.
func TestPriorityShowsOnlyWhenItIsNotTheDefault(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	medium := renderRow(Issue{Key: "ABC-1", Summary: "x", Priority: "Medium"}, 100, now)
	if strings.Contains(medium, "Medium") {
		t.Errorf("the default priority should not be drawn: %q", medium)
	}

	high := renderRow(Issue{Key: "ABC-2", Summary: "x", Priority: "Highest"}, 100, now)
	if !strings.Contains(high, "Highest") {
		t.Errorf("a priority worth reading should be drawn: %q", high)
	}
}

// The rule under the tab strip used to span the terminal while everything below
// it spanned the table, so it crossed over the preview - which is its own
// bordered box - and met nothing on the far side.
func TestChromeAboveTheTableSharesTheTableWidth(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("A-1"), at: fixedNow()()})
	m = settled(next.(Model))

	tableWidth := m.width - int(float64(m.width)*m.cfg.Defaults.Preview.Width)
	for _, line := range strings.Split(m.View(), "\n") {
		if !strings.Contains(line, "━") {
			continue
		}
		if got := runewidth.StringWidth(line); got != tableWidth {
			t.Errorf("the rule is %d cells, want the table's %d: %q", got, tableWidth, line)
		}
	}
}

func ptr(s string) *string { return &s }

func fptr(f float64) *float64 { return &f }
