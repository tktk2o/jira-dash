package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Column widths. Only the summary grows; the rest are fixed so the columns
// line up between rows and between sections.
const (
	colKey    = 10
	colIcon   = 3
	colStatus = 11 // "In Progress", the longest default status name
	colPoints = 2
	// Long enough for a full name in either script; the preview carries the
	// untruncated one.
	colAssignee = 10
	colUpdated  = 4 // "365d"
	colGaps     = 6 // single spaces between the six columns and the summary

	// Everything to the left of the summary. Named so the column header and the
	// row cannot drift apart: a header that does not line up with its rows is
	// worse than none.
	rowFixedWidth = colKey + colIcon + colStatus + colPoints +
		colAssignee + colUpdated + colGaps

	// The gutter View draws in front of every row. It is not a cursor column -
	// the selected row's fill covers it, the way gh-dash's does - it is the
	// margin the chrome above the table lines up with.
	leftMargin = 2

	// Chrome around the preview, which the viewport does not draw itself: the
	// single rule dividing the panes, one column of air after it, and the gap
	// before it. gh-dash divides its panes with one line rather than boxing the
	// preview, which is also what lets the rule above span the terminal.
	previewChrome = 3
	// Lines renderHelp draws when the help is open. tableHeight subtracts it, so
	// it has to match renderHelp exactly.
	helpHeight = 3
)

// Two colours gh-dash uses that the theme config has no name for: the blue it
// paints an identity in, and the grey it drops an age to. Read off a live
// gh-dash's escape sequences - which report decimal, so 66;160;250 is #42a0fa,
// not the #66a0fa it looks like.
const (
	identityBlue = "#42a0fa"
	ageGrey      = "#8a8a8a"
	// The rules inside gh-dash's preview are almost the background colour: they
	// separate blocks without becoming one of the things on the pane.
	faintRule = "#1c1c1c"
)

// previewStyles is the preview's half of the same weighting the rows got, read
// off gh-dash's own pane: the identity dimmed above a bold title, a meta line
// that drops the age to grey, block headings bold and underlined, and rules dark
// enough to divide without competing.
type previewStyles struct {
	identity, title, meta, age, label, rule, heading, author lipgloss.Style
}

func newPreviewStyles(t Theme) previewStyles {
	primary := lipgloss.Color(orDefault(t.Colors.Text.Primary, "#f8f8f2"))
	secondary := lipgloss.Color(orDefault(t.Colors.Text.Secondary, "#6272a4"))

	base := lipgloss.NewStyle()
	return previewStyles{
		identity: base.Foreground(secondary),
		title:    base.Foreground(primary).Bold(true),
		meta:     base.Foreground(secondary),
		age:      base.Foreground(lipgloss.Color(ageGrey)),
		label:    base.Foreground(secondary),
		rule:     base.Foreground(lipgloss.Color(faintRule)),
		heading:  base.Foreground(primary).Bold(true).Underline(true),
		// An author carries the same weight as a title: on a comment it is the
		// thing you scan for.
		author: base.Foreground(primary).Bold(true),
	}
}

type styles struct {
	activeTab   lipgloss.Style
	inactiveTab lipgloss.Style
	selectedRow lipgloss.Style
	row         lipgloss.Style
	footer      lipgloss.Style
	header      lipgloss.Style
	rule        lipgloss.Style
	divider     lipgloss.Style
}

// rowStyles is the weighting inside one row. gh-dash gives a row four of them -
// identity, metadata, age, title - and that hierarchy is most of why its list
// reads as designed: the eye jumps from the key to the title and the columns
// between recede. Drawn in one colour, as this was, a row has nothing to lead
// the eye with.
type rowStyles struct {
	key, meta, age, summary lipgloss.Style
}

// newRowStyles carries the selection background into every segment rather than
// letting View wrap the finished row in it. A style applied around a string that
// already holds colour sequences loses the background at the first reset inside
// it, so the selected row would come out striped.
func newRowStyles(t Theme, selected bool) rowStyles {
	primary := lipgloss.Color(orDefault(t.Colors.Text.Primary, "#f8f8f2"))
	secondary := lipgloss.Color(orDefault(t.Colors.Text.Secondary, "#6272a4"))

	base := lipgloss.NewStyle()
	if selected {
		base = base.Background(lipgloss.Color(orDefault(t.Colors.Background.Selected, "#44475a")))
	}
	return rowStyles{
		key:     base.Foreground(lipgloss.Color(identityBlue)),
		meta:    base.Foreground(secondary),
		age:     base.Foreground(lipgloss.Color(ageGrey)),
		summary: base.Foreground(primary).Bold(true),
	}
}

func newStyles(t Theme) styles {
	primary := lipgloss.Color(orDefault(t.Colors.Text.Primary, "#f8f8f2"))
	secondary := lipgloss.Color(orDefault(t.Colors.Text.Secondary, "#6272a4"))
	selected := lipgloss.Color(orDefault(t.Colors.Background.Selected, "#44475a"))
	border := lipgloss.Color(orDefault(t.Colors.Border.Primary, "#bd93f9"))

	return styles{
		// Filled, not merely bold: on a live dashboard two sections often open
		// on the same first issue, and a bold title alone read as "tab does
		// nothing". The fill reuses the selected-row colour so the screen has
		// one idea of "this is where you are".
		activeTab:   lipgloss.NewStyle().Foreground(primary).Background(selected).Bold(true).Padding(0, 1),
		inactiveTab: lipgloss.NewStyle().Foreground(secondary).Padding(0, 1),
		selectedRow: lipgloss.NewStyle().Foreground(primary).Background(selected),
		row:         lipgloss.NewStyle().Foreground(primary),
		footer:      lipgloss.NewStyle().Foreground(secondary),
		// Bold and bright, like gh-dash's: the column names are the key to the
		// row beneath them, and dimmed they read as another row of data.
		header: lipgloss.NewStyle().Foreground(primary).Bold(true),
		// The rule takes the border colour rather than the footer's grey, which is
		// what gh-dash draws it in.
		rule: lipgloss.NewStyle().Foreground(border),
		// One rule dividing the panes, with a column of air after it, instead of a
		// box around the preview. A box made the preview a separate object on the
		// screen and left the rule above it meeting nothing.
		divider: lipgloss.NewStyle().Foreground(border).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(border).
			PaddingLeft(1),
	}
}

// newSpinner uses the braille dots: every frame is one cell wide, so the label
// beside it does not shift as it animates. It is painted in the secondary
// colour, the same as the footer text it sits in.
func newSpinner(t Theme) spinner.Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().
		Foreground(lipgloss.Color(orDefault(t.Colors.Text.Secondary, "#6272a4")))
	return s
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func renderTabs(m Model) string {
	st := newStyles(m.cfg.Theme)
	parts := make([]string, 0, len(m.sections))
	for i, s := range m.sections {
		label := s.cfg.Title
		// Every section fetches at once on startup, so which ones are still
		// waiting is only visible here - the footer only speaks for the active
		// tab.
		if s.loading {
			label = m.spinner.View() + " " + label
		}
		// Every tab carries its count, not just the active one: with several
		// sections fetching at once, the counts are how you see what arrived.
		label = fmt.Sprintf("%s (%d)", label, len(s.visible()))
		if i == m.active {
			parts = append(parts, st.activeTab.Render(label))
			continue
		}
		parts = append(parts, st.inactiveTab.Render(label))
	}
	return strings.Join(parts, "│")
}

// renderQueryLine shows what the section actually asks Jira for. Two tabs can
// look alike and query entirely different things, and sprintPrefix narrows the
// result after the query, so it belongs here too or the row count looks wrong
// for the JQL beside it.
//
// One line, no frame. As a bordered box this cost three of the screen's lines
// and still truncated the JQL, for a fact that does not change while you are on
// the tab. The JQL is folded onto that line because a config may write it
// across several with YAML's >- and the newlines would break the layout.
func renderQueryLine(m Model, width int) string {
	sec := m.sections[m.active].cfg
	query := strings.Join(strings.Fields(sec.JQL), " ")
	if sec.SprintPrefix != "" {
		query += "  ·  sprint ^ " + sec.SprintPrefix
	}

	// Indented to the column header's left edge, so the chrome above the table
	// shares one margin.
	indent := strings.Repeat(" ", leftMargin)
	st := lipgloss.NewStyle().
		Foreground(lipgloss.Color(orDefault(m.cfg.Theme.Colors.Text.Secondary, "#6272a4")))
	return st.Render(indent + Truncate("jql  "+query, maxInt(0, width-leftMargin)))
}

// renderColumnHeader labels the columns renderRow lays out, using the same
// widths - a header that does not line up with its rows is worse than none.
func renderColumnHeader(width int) string {
	header := strings.Join([]string{
		runewidth.FillRight("KEY", colKey),
		runewidth.FillRight("T", colIcon),
		runewidth.FillRight("STATUS", colStatus),
		runewidth.FillLeft("SP", colPoints),
		runewidth.FillRight("ASSIGNEE", colAssignee),
		runewidth.FillLeft("AGE", colUpdated),
		"SUMMARY",
	}, " ")
	return Truncate(header, width)
}

// renderRow lays out one issue on one line, the summary taking whatever the
// fixed columns leave. Two lines per issue plus a rule meant a 41-issue backlog
// showed eleven of them; the summary is the one field worth the width, and
// everything trimmed from here is a keypress away in the preview.
//
// Padding goes through runewidth, not fmt: `%-*s` pads to a rune count, so a
// Japanese summary would end up the wrong number of cells wide and the columns
// would stop lining up between rows.
func renderRow(i Issue, width int, now time.Time, rs rowStyles) string {
	chip := PriorityChip(i.Priority)
	summary := chip + Truncate(i.Summary, maxInt(0, width-rowFixedWidth-runewidth.StringWidth(chip)))

	// Padded and truncated before styling, then styled per segment: measuring a
	// string that already holds colour sequences counts them as cells.
	cells := []struct {
		text  string
		style lipgloss.Style
	}{
		{runewidth.FillRight(Truncate(i.Key, colKey), colKey), rs.key},
		{runewidth.FillRight(TypeIcon(i.Type), colIcon), rs.meta},
		{runewidth.FillRight(Truncate(i.Status, colStatus), colStatus), rs.meta},
		{runewidth.FillLeft(StoryPointText(i.StoryPoints), colPoints), rs.meta},
		{runewidth.FillRight(Truncate(i.AssigneeName(), colAssignee), colAssignee), rs.meta},
		{runewidth.FillLeft(RelTime(now, i.Updated.Time), colUpdated), rs.age},
		{summary, rs.summary},
	}

	// The fixed columns alone are 46 cells, so a very narrow terminal cannot fit
	// them. Cutting keeps the invariant that a row never draws wider than the
	// width it was handed - without it the table spills past the pane and the
	// preview beside it gets pushed off screen. It happens per cell, so the
	// budget is spent left to right and the key survives.
	out, spent := make([]string, 0, len(cells)), 0
	for _, c := range cells {
		if spent >= width {
			break
		}
		if spent > 0 {
			// Styled, not a bare space: on the selected row an unstyled gap would
			// punch a hole in the fill between every pair of columns.
			out, spent = append(out, c.style.Render(" ")), spent+1
		}
		text := Truncate(c.text, width-spent)
		spent += runewidth.StringWidth(text)
		out = append(out, c.style.Render(text))
	}
	// Padded out to the full width so the selected row's fill reaches the edge of
	// the table rather than stopping at the end of a short summary.
	if spent < width {
		out = append(out, rs.summary.Render(strings.Repeat(" ", width-spent)))
	}
	return strings.Join(out, "")
}

// PriorityChip prefixes the summary with a priority only when the priority says
// something. Every issue Jira creates is Medium unless someone changed it, so a
// column of it was 8 cells of the same word on every row; as a chip the field
// costs nothing until it is worth reading.
func PriorityChip(priority string) string {
	switch priority {
	case "", "Medium", "medium":
		return ""
	default:
		return "[" + priority + "] "
	}
}

// StoryPointText keeps "unestimated" distinct from a real estimate. Rendering
// an absent value as 0 would state a number Jira never gave, and most issues
// are unestimated. Whole numbers lose the ".0": 3 points, not 3.0.
func StoryPointText(p *float64) string {
	if p == nil {
		return "-"
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

func renderFooter(m Model) string {
	st := newStyles(m.cfg.Theme)
	s := m.sections[m.active]

	if s.err != nil {
		return st.footer.Render("error: " + s.err.Error() + " · r:retry")
	}

	// RelTime says "now" for anything under a minute, and "updated now ago" is
	// not a phrase, so the freshest case gets its own wording.
	age := RelTime(m.now(), s.fetchedAt)
	state := "updated " + age + " ago"
	switch {
	case s.fetchedAt.IsZero():
		state = "never fetched"
	case age == "now":
		state = "updated just now"
	}
	if s.loading {
		// The frame goes before the word: a static "refreshing" gave no sign the
		// dashboard was alive through the CLI's 360ms of startup.
		state += " · " + m.spinner.View() + " refreshing"
	}
	if m.status != "" {
		state += " · " + m.status
	}
	return st.footer.Render(fmt.Sprintf("%s · %d issues · r:refresh ?:help", state, len(s.visible())))
}

// renderHelp expands the footer's "?:help" in place, below it, the way gh-dash
// does. As a whole-screen replacement it took the list away to tell you how to
// move around the list, and there was no way to try a key while reading it.
//
// Exactly helpHeight lines, so the table can be given the room this takes.
func renderHelp(width int) string {
	lines := []string{
		"h/l ←/→ tab  section · j/k gg/G  move · /  filter · esc  clear filter",
		"p  preview · ctrl+d/ctrl+u  scroll preview · y/Y  copy key/url · r  refresh · q  quit",
		"create keys come from the config (esc cancels)",
	}
	for i, line := range lines {
		lines[i] = strings.Repeat(" ", leftMargin) +
			Truncate(line, maxInt(0, width-leftMargin))
	}
	return strings.Join(lines, "\n")
}

// renderCreatePrompt states where the new issue will land before it is created,
// because the project and sprint are inherited rather than typed: without them
// on screen there is nothing to check against before pressing enter.
func renderCreatePrompt(m Model) string {
	target := ""
	if row, ok := m.sections[m.active].selected(); ok {
		target = row.Project.Key
		if sprint, ok := row.CurrentSprint(); ok {
			target += " / " + sprint.Name
		}
	}
	return fmt.Sprintf("new %s in %s: %s_", m.createType, target, m.createDraft)
}

func (m Model) View() string {
	st := newStyles(m.cfg.Theme)
	s := m.sections[m.active]

	tableWidth := m.width
	showPreview := PreviewVisible(m.previewOpen, m.width, m.cfg.Defaults.Preview.Width)
	if showPreview {
		tableWidth = m.width - int(float64(m.width)*m.cfg.Defaults.Preview.Width)
	}

	rows := make([]string, 0, len(s.visible()))
	now := m.now()
	// The margin is drawn outside renderRow, so it has to come out of the row's
	// budget or every line would be two cells wider than the pane it was measured
	// for.
	rowWidth := tableWidth - leftMargin
	unselected, selected := newRowStyles(m.cfg.Theme, false), newRowStyles(m.cfg.Theme, true)
	for idx, issue := range s.visible() {
		// No arrow: the fill marks the selected row on its own, which is how
		// gh-dash does it, and it spans the full width of the table. The margin is
		// rendered in the row's own style so the fill covers it too.
		rs, gutter := unselected, st.row
		if idx == s.cursor {
			rs, gutter = selected, st.selectedRow
		}
		rows = append(rows, gutter.Render(strings.Repeat(" ", leftMargin))+
			renderRow(issue, rowWidth, now, rs))
	}
	if len(rows) == 0 {
		// "(no issues)" and "loading" are different facts, and during a refresh
		// the first is a lie: the rows were cleared, not found to be absent.
		placeholder := "  (no issues)"
		if s.loading {
			placeholder = "  " + m.spinner.View() + " loading..."
		}
		rows = append(rows, st.footer.Render(placeholder))
	}

	// The create prompt takes the filter's line instead of adding one, so the
	// table does not shift by a row when it opens. It is resolved before the rows
	// are windowed because whether it is there decides how many rows fit.
	promptLine, prompting := "", true
	switch {
	case m.creating:
		promptLine = renderCreatePrompt(m)
	case m.filtering:
		promptLine = "/" + m.filterDraft
	case s.filter != "":
		promptLine, prompting = "filter: "+s.filter, false
	default:
		prompting = false
	}

	rows = windowRows(rows, s.cursor, m.tableHeight(promptLine != ""))
	table := strings.Join(rows, "\n")
	body := table
	if showPreview {
		// One rule between the panes, the way gh-dash divides them. Boxing the
		// preview instead made it a separate object floating beside the table, and
		// left the rule above meeting nothing on its far side.
		body = lipgloss.JoinHorizontal(lipgloss.Top, table, " ", st.divider.Render(m.detail.View()))
	}

	// The chrome above the table, in the order gh-dash stacks it: tabs, a rule
	// across the whole terminal, the query the tab is showing, then the column
	// names. The rule spans the terminal rather than the table because the panes
	// are divided by a rule of their own now, which it meets - so it caps both
	// panes instead of stopping in mid-air over the preview.
	sections := []string{
		renderTabs(m),
		st.rule.Render(strings.Repeat("━", maxInt(0, m.width))),
		renderQueryLine(m, tableWidth),
		st.header.Render(strings.Repeat(" ", leftMargin) + renderColumnHeader(rowWidth)),
		body,
	}
	if promptLine != "" {
		if prompting {
			// The prompt is where the keyboard is, so it gets the same fill the
			// active tab and selected row use rather than the footer's grey.
			sections = append(sections, st.selectedRow.Render(promptLine))
		} else {
			sections = append(sections, st.footer.Render(promptLine))
		}
	}
	sections = append(sections, renderFooter(m))
	// Below the footer, because it is that line's "?:help" expanding in place.
	// The keys keep working while it is open - being able to try one while
	// reading it is the point of not taking the screen away.
	if m.showHelp {
		sections = append(sections, st.footer.Render(renderHelp(tableWidth)))
	}
	return strings.Join(sections, "\n")
}

// tableHeight is how many row lines fit once the chrome has taken its share.
// Without this the rows simply ran past the bottom of the terminal and the tab
// strip was pushed off the top - a 41-issue section on a 45-line terminal had
// already lost it.
func (m Model) tableHeight(hasPrompt bool) int {
	// The tab strip, the rule, the query line, the column header, the footer.
	chrome := 5
	if hasPrompt {
		chrome++
	}
	if m.showHelp {
		chrome += helpHeight
	}
	return maxInt(1, m.height-chrome)
}

// windowRows keeps the cursor on screen by holding it in the middle of the
// window once the list is longer than the window. Below the halfway point the
// window stays at the top, so short lists and the first rows of a long one do
// not scroll at all.
func windowRows(rows []string, cursor, height int) []string {
	if height <= 0 || len(rows) <= height {
		return rows
	}
	start := cursor - height/2
	if start < 0 {
		start = 0
	}
	if start > len(rows)-height {
		start = len(rows) - height
	}
	return rows[start : start+height]
}

// copiedMsg reports the outcome of a clipboard write, and commandRanMsg the
// outcome of a configured keybinding. Each is its own type because reusing the
// detail-loading message as a "nothing happened" signal would make Update's
// contract a guessing game.
type copiedMsg struct {
	value string
	err   error
}

type commandRanMsg struct {
	key string
	err error
}

// copySelected puts a field of the selected issue on the clipboard via pbcopy.
// macOS only, like the rest of this repo.
func (m Model) copySelected(field func(Issue) string) tea.Cmd {
	issue, ok := m.sections[m.active].selected()
	if !ok {
		return nil
	}
	value := field(issue)
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(value)
		if err := cmd.Run(); err != nil {
			return copiedMsg{value: value, err: fmt.Errorf("pbcopy: %w", err)}
		}
		return copiedMsg{value: value}
	}
}

// runUserKeybinding runs a command from the config. The dashboard itself never
// writes to Jira; anything that changes state goes through here.
func (m Model) runUserKeybinding(key string) (tea.Model, tea.Cmd) {
	issue, ok := m.sections[m.active].selected()
	if !ok {
		return m, nil
	}
	for _, kb := range m.cfg.Keybindings.Issues {
		if kb.Key != key {
			continue
		}
		rendered, err := RenderCommand(kb.Command, NewIssueVars(issue))
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		return m, tea.ExecProcess(exec.Command("sh", "-c", rendered), func(err error) tea.Msg {
			return commandRanMsg{key: kb.Key, err: err}
		})
	}
	return m, nil
}
