package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// The panes are divided by a single rule, the way gh-dash divides its own. As a
// rounded box the preview read as a separate object floating beside the table,
// and it left the rule above the table meeting nothing on its far side.
func TestPreviewIsDividedByARuleNotABox(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	m = next.(Model)
	next, _ = m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: time.Now()})
	m = next.(Model)

	out := plain(m.View())
	if strings.ContainsAny(out, "╭╮╰╯") {
		t.Errorf("the preview should not be boxed: %q", out)
	}
	// The divider has to reach every line of the pane, not just the first.
	divided := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "│") {
			divided++
		}
	}
	if divided < 5 {
		t.Errorf("only %d lines carry the divider: %q", divided, out)
	}
}

func TestRenderRowHoldsKeyStatusAndAge(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	issue := Issue{Key: "ABC-1234", Summary: "トークン更新で 500 が出る", Status: "In Progress", Type: "Bug"}
	issue.Updated.Time = now.Add(-2 * time.Hour)

	row := plainRow(issue, 100, now)

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

	row := plainRow(issue, 60, now)

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
	a := runewidth.StringWidth(plainRow(ascii, 100, now))
	b := runewidth.StringWidth(plainRow(japanese, 100, now))
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
		if got := runewidth.StringWidth(plainRow(issue, width, now)); got > width {
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

// ExecProcess used to own the terminal for every configured command, so a
// failure repainted the whole dashboard over whatever the command had printed
// and the footer could only ever say "exit status 1". The footer must carry
// the command's own stderr instead, once it has one.
func TestCommandRanMsgReportsStderrOverTheBareExitStatus(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})

	next, _ := m.Update(commandRanMsg{key: "a", err: errTest, stderr: "real-error-message"})
	m = next.(Model)
	if !strings.Contains(m.status, "real-error-message") {
		t.Errorf("status = %q, want the command's own stderr", m.status)
	}
	if strings.Contains(m.status, "kaboom") {
		t.Errorf("status = %q, want the stderr snippet, not the exit status, once one is available", m.status)
	}
}

// A CLI commonly prints a warning before its actual error, so the first line of
// stderr is the least likely one to say what went wrong - lastMeaningfulLine
// has to walk from the end.
func TestLastMeaningfulLinePicksTheLastNonEmptyLine(t *testing.T) {
	got := lastMeaningfulLine("warning-line-1\nreal-error-message\n")
	if got != "real-error-message" {
		t.Errorf("lastMeaningfulLine = %q, want the last non-empty line", got)
	}
}

// A long stderr line would otherwise push the age, the issue count and the
// help hint off the one-line footer.
func TestLastMeaningfulLineTrimsToFitOneFooterLine(t *testing.T) {
	got := lastMeaningfulLine(strings.Repeat("x", stderrSnippetMaxLen*2))
	if runewidth.StringWidth(got) > stderrSnippetMaxLen {
		t.Errorf("lastMeaningfulLine returned %d cells, want at most %d", runewidth.StringWidth(got), stderrSnippetMaxLen)
	}
}

// terminal: true is the only setting that may hand the terminal to a command:
// only then can tea.ExecProcess be the right call, since it tears the
// dashboard down and repaints it on exit. This is asserted on the message type
// the returned tea.Cmd produces rather than by actually running a command
// against a real TTY - calling the tea.Cmd from tea.ExecProcess only wraps the
// *exec.Cmd in bubbletea's own internal message; it does not execute it.
func TestTerminalKeybindingTakesTheExecProcessPathAndOthersDoNot(t *testing.T) {
	terminalCmd := commandCmd(Keybinding{Key: "e", Terminal: true}, "true", ".")
	msg := terminalCmd()
	if _, ok := msg.(commandRanMsg); ok {
		t.Errorf("terminal: true produced a commandRanMsg directly, want bubbletea's own exec message")
	}

	plainCmd := commandCmd(Keybinding{Key: "o"}, "true", ".")
	msg = plainCmd()
	if _, ok := msg.(commandRanMsg); !ok {
		t.Errorf("terminal unset produced %T, want commandRanMsg directly - no ExecProcess hand-off", msg)
	}
}

// A configured command's stderr must reach the footer even though it never
// touches the terminal - this is the whole point of not going through
// ExecProcess for the common case.
func TestNonTerminalCommandCapturesStderrOnFailure(t *testing.T) {
	cmd := commandCmd(Keybinding{Key: "x"}, "echo warning-line-1 >&2; echo real-error-message >&2; exit 1", ".")
	msg := cmd().(commandRanMsg)
	if msg.err == nil {
		t.Fatal("want an error from a command that exits 1")
	}
	if msg.stderr != "real-error-message" {
		t.Errorf("stderr = %q, want the last non-empty line", msg.stderr)
	}
}

// The success path must still clear the footer and still let refresh: true
// trigger a refetch - runConfigured and Update's commandRanMsg handling must
// keep agreeing on that regardless of which path produced the message.
func TestNonTerminalCommandSucceedsAndReportsNoError(t *testing.T) {
	cmd := commandCmd(Keybinding{Key: "x", Refresh: true}, "true", ".")
	msg := cmd().(commandRanMsg)
	if msg.err != nil {
		t.Errorf("err = %v, want nil for a command that exits 0", msg.err)
	}
	if !msg.refresh {
		t.Error("refresh should be carried through from the keybinding")
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

// The project and sprint are inherited, not typed, so the box has to say where
// the issue will land before it is submitted.
func TestCreateBoxNamesItsTarget(t *testing.T) {
	var got []NewIssueRequest
	m := createTestModel(t, &got)
	m = press(m, "c")
	for _, r := range "hi" {
		m = press(m, string(r))
	}

	out := plain(m.View())
	for _, want := range []string{"Task", "ABC", "Team 0803-0807", "hi"} {
		if !strings.Contains(out, want) {
			t.Errorf("the box is missing %q: %q", want, out)
		}
	}
	// The keys that work inside it are named there, because they are not the ones
	// that work anywhere else.
	for _, want := range []string{"Ctrl+d", "esc"} {
		if !strings.Contains(out, want) {
			t.Errorf("the box should state %q: %q", want, out)
		}
	}
}

// The box is framed like gh-dash's approve comment, and takes its height out of
// the table rather than off the bottom of the terminal.
func TestCreateBoxIsFramedAndFitsTheScreen(t *testing.T) {
	var got []NewIssueRequest
	m := createTestModel(t, &got)

	m = press(m, "c")

	out := plain(m.View())
	if !strings.Contains(out, "\u256d") || !strings.Contains(out, "\u2570") {
		t.Errorf("the prompt should be framed: %q", out)
	}
	if lines := strings.Count(out, "\n") + 1; lines > m.height {
		t.Errorf("the view is %d lines on a %d line terminal", lines, m.height)
	}
}

// "refreshing" as a static word gave no sign the dashboard was alive while the
// section's search call was in flight. The footer carries the animated frame.
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

	row := plainRow(issue, 100, now)

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
	row := plainRow(Issue{Key: "ABC-1"}, 100, now)

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

	medium := plainRow(Issue{Key: "ABC-1", Summary: "x", Priority: "Medium"}, 100, now)
	if strings.Contains(medium, "Medium") {
		t.Errorf("the default priority should not be drawn: %q", medium)
	}

	high := plainRow(Issue{Key: "ABC-2", Summary: "x", Priority: "Highest"}, 100, now)
	if !strings.Contains(high, "Highest") {
		t.Errorf("a priority worth reading should be drawn: %q", high)
	}
}

// The rule caps both panes, so it spans the terminal and meets the rule that
// divides them - the same as gh-dash. It only worked at the table's width while
// the preview was a box, when there was nothing on the far side for it to meet.
func TestTheRuleSpansTheTerminal(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("A-1"), at: fixedNow()()})
	m = settled(next.(Model))

	found := false
	for _, line := range strings.Split(plain(m.View()), "\n") {
		if !strings.Contains(line, "━") {
			continue
		}
		found = true
		if got := runewidth.StringWidth(line); got != m.width {
			t.Errorf("the rule is %d cells, want the terminal's %d", got, m.width)
		}
	}
	if !found {
		t.Error("no rule was drawn under the tab strip")
	}
}

// The help used to replace the whole screen - it took the list away in order to
// tell you how to move around the list. It expands the footer's "?:help" in
// place now, so the rows stay visible beside it.
func TestHelpOpensBelowTheListNotOverIt(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = settled(next.(Model))

	m = press(m, "?")
	out := m.View()

	if !strings.Contains(out, "ABC-1") {
		t.Errorf("the list should stay on screen with the help open: %q", out)
	}
	if !strings.Contains(out, "switch section") && !strings.Contains(out, "section") {
		t.Errorf("the help should be on screen: %q", out)
	}
	// The footer is what the help expands, so the help comes after it.
	lines := strings.Split(out, "\n")
	footer, help := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "r:refresh") {
			footer = i
		}
		if strings.Contains(line, "copy url") {
			help = i
		}
	}
	if footer < 0 || help < 0 || help < footer {
		t.Errorf("help (line %d) should sit below the footer (line %d)", help, footer)
	}
}

// Reading the keys is not a mode: being able to try one while the list is still
// there is the reason the help no longer takes the screen.
func TestKeysStillWorkWhileTheHelpIsOpen(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "ABC-2"), at: fixedNow()()})
	m = settled(next.(Model))

	m = press(m, "?")
	m = press(m, "j")
	if got := m.sections[0].cursor; got != 1 {
		t.Errorf("cursor = %d, want the help not to swallow j", got)
	}

	m = press(m, "l")
	if m.active != 1 {
		t.Errorf("active = %d, want the section switch to still work", m.active)
	}
	if !strings.Contains(m.View(), "copy url") {
		t.Error("the help should stay open across other keys")
	}
}

// Rows used to run off the bottom of the terminal, which pushed the tab strip
// off the top: a 41-issue section on a 45-line terminal had already lost it.
//
// Every state that changes how tall the chrome is has to tell the preview, and
// the preview and the table sit side by side - so the taller of the two decides
// where the footer lands. Each state is driven through its real key here, which
// is what makes a missed resize call fail in this test rather than on screen.
func TestViewNeverDrawsMoreLinesThanTheTerminalHas(t *testing.T) {
	base := func(t *testing.T) Model {
		m := newTestModel(t, fakeSearcher{})
		m.cfg.Create = []CreateKey{{Key: "c", Type: "Task"}}
		// Twelve choices against a cap of eight, so the picker case below is also
		// the proof that a long list scrolls instead of growing its box.
		long := make([]Choice, 0, 12)
		for i := 0; i < 12; i++ {
			long = append(long, Choice{Value: "status " + strconv.Itoa(i)})
		}
		m.cfg.Keybindings.Issues = []Keybinding{
			{Key: "a", Name: "ask", Prompt: true, Command: "true"},
			{Key: "s", Name: "status", Choices: long, Command: "true"},
		}
		keys := make([]string, 0, 80)
		for i := 0; i < 80; i++ {
			keys = append(keys, "ABC-"+strconv.Itoa(i))
		}
		next, _ := m.Update(fetchedMsg{idx: 0, issues: issues(keys...), at: fixedNow()()})
		return settled(next.(Model))
	}

	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"nothing open", nil},
		{"help", []string{"?"}},
		{"create box", []string{"c"}},
		{"ask box", []string{"a"}},
		{"ask box and help", []string{"?", "a"}},
		{"picker", []string{"s"}},
		{"picker and help", []string{"?", "s"}},
		// Narrows 12 entries down to 3 ("status 1", "status 10", "status 11"):
		// the box's height has to track the narrowed count, not the cap the
		// unfiltered "picker" case above already covers - a filter that left 6
		// blank rows under 3 matches would pass the >m.height check here for
		// the wrong reason.
		{"picker filtered", []string{"s", "1"}},
		{"filter", []string{"/"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base(t)
			for _, k := range tc.keys {
				m = press(m, k)
			}

			if got := strings.Count(m.View(), "\n") + 1; got > m.height {
				t.Errorf("rendered %d lines on a %d line terminal", got, m.height)
			}
			// Whatever gets cut, it must not be the row the cursor is on.
			m.sections[0].cursor = 79
			if !strings.Contains(plain(m.View()), "ABC-79") {
				t.Error("the selected row scrolled off screen")
			}
		})
	}
}

func ptr(s string) *string { return &s }

func fptr(f float64) *float64 { return &f }

// gh-dash gives a row four weights - identity, metadata, age, title - and that
// hierarchy is most of why its list reads as designed. Drawn in one colour a row
// has nothing to lead the eye with.
// Asserted on the styles rather than on rendered output: lipgloss strips colour
// when it is not writing to a terminal, so under `go test` every row renders
// bare and an assertion on the escape sequences would pass no matter what.
func TestRowGivesItsColumnsDifferentWeights(t *testing.T) {
	rs := newRowStyles(Theme{}, false)

	weights := map[string]bool{}
	for _, s := range []lipgloss.Style{rs.key, rs.meta, rs.age, rs.summary} {
		weights[fmt.Sprintf("%v-%v", s.GetForeground(), s.GetBold())] = true
	}
	if len(weights) < 4 {
		t.Errorf("a row has %d distinct weights, want 4: %v", len(weights), weights)
	}
	if !rs.summary.GetBold() {
		t.Error("the summary is the field you read; it should be the bold one")
	}
	if rs.key.GetForeground() == rs.meta.GetForeground() {
		t.Error("the key should stand out from the metadata beside it")
	}
}

// The fill is the only thing marking the selected row now that the arrow is
// gone, so it has to be unbroken. Styling the columns individually means every
// gap between them is its own segment, and one left without the background
// punches a hole through the fill.
func TestSelectedRowCarriesTheFillInEverySegment(t *testing.T) {
	rs := newRowStyles(Theme{}, true)

	for name, s := range map[string]lipgloss.Style{
		"key": rs.key, "meta": rs.meta, "age": rs.age, "summary": rs.summary,
	} {
		if s.GetBackground() == nil {
			t.Errorf("the %s segment carries no fill", name)
		}
	}

	// And the row has to reach the full width, or the fill stops at the end of a
	// short summary instead of the edge of the table.
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	short := Issue{Key: "ABC-1", Type: "Bug", Status: "Open", Summary: "short"}
	if got := runewidth.StringWidth(plainRow(short, 100, now)); got != 100 {
		t.Errorf("a row with a short summary is %d cells, want the full 100", got)
	}
}

// plainRow renders a row with the styles stripped. Widths have to be measured on
// the text alone - a styled string counts its escape sequences as cells - and
// every assertion here is about layout, not colour.
func plainRow(i Issue, width int, now time.Time) string {
	return plain(renderRow(i, width, now, newRowStyles(Theme{}, false)))
}

// The help said "create keys come from the config" while four configured keys
// went unnamed, so the features they run were invisible to the person who set
// them up - which is how a working create key came to be forgotten.
func TestHelpNamesTheConfiguredKeys(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	m.cfg.Create = []CreateKey{{Key: "c", Type: "タスク"}}
	m.cfg.Keybindings.Issues = []Keybinding{
		{Key: "R", Name: "claude", Command: "tmux new-window claude"},
		{Key: "o", Command: "open {{.IssueURL}}"},
	}

	m = press(m, "?")
	out := plain(renderHelp(m, 200))

	for _, want := range []string{"c", "new タスク", "R", "claude"} {
		if !strings.Contains(out, want) {
			t.Errorf("help is missing %q: %q", want, out)
		}
	}
	// Without a name the command itself is shown - unhelpful is better than absent.
	if !strings.Contains(out, "open {{.IssueURL}}") {
		t.Errorf("an unnamed key should fall back to its command: %q", out)
	}
	// helpHeight is what the table's room is taken from, so it has to be what the
	// layout actually drew - a constant the layout was supposed to match is how the
	// footer ends up off screen.
	if got, want := strings.Count(out, "\n")+1, m.helpHeight(); got != want {
		t.Errorf("help drew %d lines but helpHeight says %d", got, want)
	}
}

// The keys should form one straight edge and the descriptions another - that
// alignment is what makes a list this long scannable, and it is the whole point
// of laying the help out in columns rather than joining it with separators.
func TestHelpColumnsAreAligned(t *testing.T) {
	entries := []helpEntry{
		{"↑/k", "move up"},
		{"Ctrl+d", "preview page down"},
		{"q", "quit"},
		{"a", "ask claude"},
		{"p", "toggle preview"},
		{"r", "refresh section"},
		{"y", "copy key"},
		{"?", "help"},
	}
	plainStyle := lipgloss.NewStyle()

	lines := layoutHelp(entries, 200, plainStyle, plainStyle)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 rows of 4 columns: %q", len(lines), lines)
	}

	// The first column holds "move up" under "preview page down", whose keys are
	// three cells apart. Both descriptions must still start at the same cell.
	//
	// Measured in cells, not bytes: "↑" is three bytes and one cell, so comparing
	// byte offsets reports a misalignment that is not there.
	first := columnOf(lines[0], "move up")
	second := columnOf(lines[1], "preview page down")
	if first != second {
		t.Errorf("column 1 descriptions start at %d and %d; the keys should be padded to one edge\n%q\n%q",
			first, second, lines[0], lines[1])
	}
	for _, line := range lines {
		if got := runewidth.StringWidth(line); got > 200 {
			t.Errorf("a help line is %d cells: %q", got, line)
		}
	}
}

// A configured command can be any length. It is cut to what its column can
// spare, rather than widening the grid until the columns after it fall off.
func TestHelpCutsALongCommandInsteadOfOverflowing(t *testing.T) {
	entries := []helpEntry{
		{"a", strings.Repeat("tmux new-window -n very-long ", 20)},
		{"q", "quit"},
	}
	plainStyle := lipgloss.NewStyle()

	for _, width := range []int{200, 80, 40, 12} {
		for _, line := range layoutHelp(entries, width, plainStyle, plainStyle) {
			if got := runewidth.StringWidth(line); got > width {
				t.Errorf("width %d: a help line is %d cells: %q", width, got, line)
			}
		}
	}
}

// columnOf is which display cell text starts at within line.
func columnOf(line, text string) int {
	i := strings.Index(line, text)
	if i < 0 {
		return -1
	}
	return runewidth.StringWidth(line[:i])
}
