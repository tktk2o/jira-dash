package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
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
	// The prompt box, laid out like gh-dash's "Approve with comment…": a border,
	// a title, a blank line, the input, a blank line, and the keys that work
	// inside it. Everything but the input is fixed, so promptBoxChrome plus the
	// input's height is the whole box.
	promptBoxChrome = 6
	// A Jira summary is one line - `jira create -s` takes one string - so the
	// create box does not offer more.
	createInputHeight = 1
	// An instruction is worth more room: "do X, and note Y" is two thoughts, and
	// on one line you cannot see the first while typing the second.
	askInputHeight = 3
	// The picker shows its whole list where it can. The cap is what stops a long
	// one from eating the table: past it the list scrolls, the same way the table
	// does.
	chooseMaxHeight = 8
	// stderrSnippetMaxLen bounds how much of a failed command's stderr the
	// footer carries. The footer is one line, and a stack trace would otherwise
	// push the rest of it - the age, the issue count, the help hint - off the
	// terminal.
	stderrSnippetMaxLen = 200
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
	return st.Render(indent + Truncate("jql  "+query, max(0, width-leftMargin)))
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
	summary := chip + Truncate(i.Summary, max(0, width-rowFixedWidth-runewidth.StringWidth(chip)))

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
		// dashboard was alive through the 0.5-1.2s Jira search call.
		state += " · " + m.spinner.View() + " refreshing"
	}
	if m.status != "" {
		state += " · " + m.status
	}
	return st.footer.Render(fmt.Sprintf("%s · %d issues · r:refresh ?:help", state, len(s.visible())))
}

// helpEntry is one key and what it does, held as a pair rather than a formatted
// string so the columns can be aligned across entries of wildly different
// lengths - a configured command is many times the width of "q".
type helpEntry struct{ keys, what string }

// helpEntries is every key that works: the built-ins, then whatever the config
// adds. The configured ones are here because the help used to summarise them as
// "create keys come from the config", which left four working keys unnamed - and
// that is how one of them came to be forgotten.
func helpEntries(m Model) []helpEntry {
	out := []helpEntry{
		{"↑/k", "move up"},
		{"↓/j", "move down"},
		{"←/h", "previous section"},
		{"→/l", "next section"},
		{"gg", "first item"},
		{"G", "last item"},
		{"Ctrl+d", "preview page down"},
		{"Ctrl+u", "preview page up"},
		{"p", "toggle preview"},
		{"r", "refresh section"},
		{"/", "filter"},
		{"esc", "clear filter"},
		{"y", "copy key"},
		{"Y", "copy url"},
		{"?", "help"},
		{"q", "quit"},
	}
	for _, ck := range m.cfg.Create {
		out = append(out, helpEntry{ck.Key, "new " + ck.Type})
	}
	for _, kb := range m.cfg.Keybindings.Issues {
		out = append(out, helpEntry{kb.Key, orDefault(kb.Name, kb.Command)})
	}
	return out
}

// helpColumnGap is the air between one column's description and the next
// column's key.
const helpColumnGap = 3

// layoutHelp arranges the entries into aligned columns, filling each column top
// to bottom before starting the next. That is gh-dash's layout, and it is why its
// help stays readable at this many entries: the keys form one straight edge to
// scan and the descriptions form another.
//
// The widest layout that fits is used. Columns are measured on their own
// contents, so a column of "q" and "p" is not made as wide as one holding
// "Ctrl+d".
func layoutHelp(entries []helpEntry, width int, keyStyle, whatStyle lipgloss.Style) []string {
	if len(entries) == 0 || width <= 0 {
		return nil
	}
	for cols := 4; cols > 1; cols-- {
		rows := (len(entries) + cols - 1) / cols
		if lines, ok := helpGrid(entries, rows, width, keyStyle, whatStyle); ok {
			return lines
		}
	}
	// One column always fits: its descriptions are cut to whatever is left.
	lines, _ := helpGrid(entries, len(entries), width, keyStyle, whatStyle)
	return lines
}

// helpGrid lays the entries out column-major in columns of rows each, reporting
// whether the result fitted the width.
func helpGrid(entries []helpEntry, rows, width int, keyStyle, whatStyle lipgloss.Style) ([]string, bool) {
	if rows <= 0 {
		return nil, false
	}
	var columns [][]helpEntry
	for start := 0; start < len(entries); start += rows {
		columns = append(columns, entries[start:min(start+rows, len(entries))])
	}

	keyWidths, whatWidths := make([]int, len(columns)), make([]int, len(columns))
	total := leftMargin
	for c, column := range columns {
		for _, e := range column {
			keyWidths[c] = max(keyWidths[c], runewidth.StringWidth(e.keys))
			whatWidths[c] = max(whatWidths[c], runewidth.StringWidth(e.what))
		}
		total += keyWidths[c] + 1 + whatWidths[c]
		if c < len(columns)-1 {
			total += helpColumnGap
		}
	}
	if total > width && len(columns) > 1 {
		return nil, false
	}

	lines := make([]string, rows)
	for r := range rows {
		var b strings.Builder
		b.WriteString(strings.Repeat(" ", leftMargin))
		spent := leftMargin
		for c, column := range columns {
			if r >= len(column) {
				continue
			}
			if c > 0 {
				b.WriteString(strings.Repeat(" ", helpColumnGap))
				spent += helpColumnGap
			}
			b.WriteString(keyStyle.Render(runewidth.FillRight(column[r].keys, keyWidths[c]) + " "))
			spent += keyWidths[c] + 1
			// Cut here rather than widening the column: a configured command has no
			// bound, and one long one would push every column after it off screen.
			room := max(0, width-spent)
			text := Truncate(column[r].what, room)
			b.WriteString(whatStyle.Render(runewidth.FillRight(text, min(whatWidths[c], room))))
			spent += runewidth.StringWidth(text)
		}
		// The trailing padding of the last column on a row is nothing but width.
		lines[r] = strings.TrimRight(b.String(), " ")
	}
	return lines, true
}

// renderHelp expands the footer's "?:help" in place, below it, the way gh-dash
// does. As a whole-screen replacement it took the list away to tell you how to
// move around the list, and there was no way to try a key while reading it.
func renderHelp(m Model, width int) string {
	st := newStyles(m.cfg.Theme)
	return strings.Join(layoutHelp(helpEntries(m), width, st.header, st.footer), "\n")
}

// renderCreatePrompt states where the new issue will land before it is created,
// because the project and sprint are inherited rather than typed: without them
// on screen there is nothing to check against before pressing enter.
func createPromptTitle(m Model) string {
	target := ""
	if row, ok := m.sections[m.active].selected(); ok {
		target = row.Project.Key
		if sprint, ok := row.CurrentSprint(); ok {
			target += " / " + sprint.Name
		}
	}
	return fmt.Sprintf("new %s in %s…", m.createType, target)
}

// askPromptTitle names the issue the instruction will be about, because the box
// sits at the bottom of the screen and the row it came from may have scrolled
// out of the window by the time you finish typing.
func askPromptTitle(m Model) string {
	label := "ask"
	for _, kb := range m.cfg.Keybindings.Issues {
		if kb.Key == m.askKey {
			label = orDefault(kb.Name, "ask")
		}
	}
	key := "?"
	if row, ok := m.sections[m.active].selected(); ok {
		key = row.Key
	}
	return fmt.Sprintf("%s about %s…", label, key)
}

// newPromptInput is the box's input. Line numbers and the highlighted current
// line are gh-dash's, and they are what make a multi-line box read as an editor
// rather than as a wrapped line.
func newPromptInput(t Theme) textarea.Model {
	primary := lipgloss.Color(orDefault(t.Colors.Text.Primary, "#f8f8f2"))
	secondary := lipgloss.Color(orDefault(t.Colors.Text.Secondary, "#6272a4"))

	ta := textarea.New()
	ta.ShowLineNumbers = true
	// The textarea draws its own per-line prompt as well as the line number; one
	// marker is enough.
	ta.Prompt = ""
	ta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(primary)
	ta.FocusedStyle.LineNumber = lipgloss.NewStyle().Foreground(secondary)
	ta.FocusedStyle.CursorLineNumber = lipgloss.NewStyle().Foreground(secondary)
	// Near-black rather than the selection colour: the row cursor is elsewhere on
	// screen and two things claiming to be "where you are" is one too many.
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(lipgloss.Color(faintRule))
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(secondary)
	// The end-of-buffer tildes say "this box is taller than what you typed",
	// which is noise on a box sized to the task.
	ta.EndOfBufferCharacter = ' '
	return ta
}

// renderPromptBox draws the box gh-dash puts at the bottom of the screen for an
// approve comment: a title, the input, and the keys that work inside it.
//
// The keys are named in the box rather than left to the help, because they are
// not the keys that work anywhere else - enter inserts a newline here, and the
// only way out is stated on the line above the border.
// The body is passed in rather than read off the model, because the two boxes
// hold different things - an input for typing, a list for picking - and the frame
// around them is the only part they share.
func renderPromptBox(m Model, title, inner string, keys string, width int) string {
	st := newStyles(m.cfg.Theme)
	// 2 for the border's own columns, 2 for the padding inside it.
	innerWidth := max(0, width-4)

	body := strings.Join([]string{
		st.header.Render(Truncate(title, innerWidth)),
		"",
		inner,
		"",
		st.footer.Render(Truncate(keys, innerWidth)),
	}, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(orDefault(m.cfg.Theme.Colors.Border.Primary, "#bd93f9"))).
		Padding(0, 1).
		Width(innerWidth).
		Render(body)
}

// choosePromptTitle names the issue the picked value will be set on, for the same
// reason the ask box does: the row may have scrolled out of the window.
func choosePromptTitle(m Model) string {
	label := "choose"
	for _, kb := range m.cfg.Keybindings.Issues {
		if kb.Key == m.chooseKey {
			label = orDefault(kb.Name, "choose")
		}
	}
	key := "?"
	if row, ok := m.sections[m.active].selected(); ok {
		key = row.Key
	}
	return fmt.Sprintf("%s on %s…", label, key)
}

// renderChoices is the picker's body: the typed filter and its match count on
// its own line, then the narrowed, ranked list windowed the same way the
// table is, with the entry under the cursor filled to the pane's edge so it
// reads as the selected row it is.
//
// The count is folded into this line rather than added as a second readout
// because renderPromptBox only has a title and a keys line free, and the
// title already carries the issue key - putting the count there too would
// make one line say two unrelated things.
func renderChoices(m Model, width int) string {
	st := newStyles(m.cfg.Theme)
	matches := m.visibleChoices()

	filterLine := st.footer.Render(runewidth.FillRight(
		Truncate(fmt.Sprintf("%s (%d/%d)", m.chooseFilter, len(matches), len(m.chooseList)), width), width))

	lines := make([]string, 0, len(matches))
	for i, c := range matches {
		style := st.row
		if i == m.chooseCursor {
			style = st.selectedRow
		}
		lines = append(lines,
			style.Render(runewidth.FillRight("  "+Truncate(c.Name(), max(0, width-2)), width)))
	}
	if len(lines) == 0 {
		// Distinct from an empty chooseList (refused before the box ever opens,
		// see openChoosePrompt): this is a live typed filter that has ruled out
		// every candidate, and the box should say so rather than draw nothing.
		lines = append(lines, st.footer.Render(runewidth.FillRight("  (no matches)", width)))
	}
	body := append([]string{filterLine},
		windowRows(lines, m.chooseCursor, chooseHeight(len(matches)))...)
	return strings.Join(body, "\n")
}

// chooseHeight is how many entries the box shows. Capped so that a long list
// cannot push the table off the screen; beyond the cap the list scrolls.
func chooseHeight(n int) int {
	return min(max(n, 1), chooseMaxHeight)
}

// View draws the whole frame from the model alone, top to bottom: the tabs, the
// table, the preview beside it, the prompt, the footer, and the help below it.
// Nothing here fetches or mutates - what is not in the model cannot be drawn.
func (m Model) View() string {
	st := newStyles(m.cfg.Theme)
	s := m.sections[m.active]

	// Through the model, so the prompt box - which is sized when it opens, not
	// when it draws - cannot disagree with the table about how wide the pane is.
	showPreview := PreviewVisible(m.previewOpen, m.width, m.cfg.Defaults.Preview.Width)
	tableWidth := m.tableWidth()

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

	// Resolved before the rows are windowed: how tall the prompt is decides how
	// many rows fit.
	// This switch and promptLines' switch below are two lists of the same modes
	// and must stay in lockstep: a mode added here without a matching case there
	// is under-counted, which pushes the footer off-screen rather than failing
	// loudly. TestPromptLinesMatchesEveryPromptMode in model_test.go asserts the
	// two never disagree - add the new mode to that test's table too.
	promptLine, prompting := "", true
	switch {
	case m.creating:
		promptLine = renderPromptBox(m, createPromptTitle(m), m.prompt.View(),
			"Ctrl+d submit ⋅ esc cancel", tableWidth)
		prompting = false
	case m.asking:
		promptLine = renderPromptBox(m, askPromptTitle(m), m.prompt.View(),
			"Ctrl+d submit ⋅ enter newline ⋅ esc cancel", tableWidth)
		prompting = false
	case m.choosing:
		promptLine = renderPromptBox(m, choosePromptTitle(m),
			// 2 for the border and 2 for the padding, as renderPromptBox counts
			// them, and 2 more for the padding lipgloss adds inside the width it was
			// given: a line as wide as the box wraps, and each entry became two.
			renderChoices(m, max(0, tableWidth-6)),
			"type to filter ⋅ ↑/↓ move ⋅ enter select ⋅ esc cancel", tableWidth)
		prompting = false
	case m.filtering:
		promptLine = "/" + m.filterDraft
	case s.filter != "":
		promptLine, prompting = "filter: "+s.filter, false
	default:
		prompting = false
	}

	rows = windowRows(rows, s.cursor, m.tableHeight())
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
		st.rule.Render(strings.Repeat("━", max(0, m.width))),
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
		sections = append(sections, st.footer.Render(renderHelp(m, tableWidth)))
	}
	return strings.Join(sections, "\n")
}

// promptLines is how many lines the prompt currently occupies. It is derived
// from the same state View renders from, so the two cannot disagree about how
// much room is left for the table.
//
// This switch mirrors View's switch above, case for case: forgetting a case
// here for a mode View knows about under-counts the chrome and pushes the
// footer off-screen. TestPromptLinesMatchesEveryPromptMode in model_test.go
// guards this - extend its table when a new mode is added to either switch.
func (m Model) promptLines() int {
	switch {
	case m.creating:
		return promptBoxChrome + createInputHeight
	case m.asking:
		return promptBoxChrome + askInputHeight
	case m.choosing:
		// +1 for the filter/count line renderChoices draws above the list.
		return promptBoxChrome + 1 + chooseHeight(len(m.visibleChoices()))
	case m.filtering, m.sections[m.active].filter != "":
		// The filter stays one line. It is a phrase, not a message, and a box
		// around it would take eight lines of the list to hold six characters.
		return 1
	}
	return 0
}

// chromeLines is everything above and below the table that varies. Update
// compares it before and after a key to decide whether the preview needs
// resizing, so nothing else has to remember to.
func (m Model) chromeLines() int {
	return m.promptLines() + m.helpHeight()
}

// helpHeight is how many lines the help takes. It depends on how many keys the
// config adds and how wide the terminal is, so it is counted off the same layout
// that draws it rather than fixed at a number the layout has to match.
func (m Model) helpHeight() int {
	if !m.showHelp {
		return 0
	}
	st := newStyles(m.cfg.Theme)
	return len(layoutHelp(helpEntries(m), m.tableWidth(), st.header, st.footer))
}

// tableHeight is how many row lines fit once the chrome has taken its share.
// Without this the rows simply ran past the bottom of the terminal and the tab
// strip was pushed off the top - a 41-issue section on a 45-line terminal had
// already lost it.
func (m Model) tableHeight() int {
	// The tab strip, the rule, the query line, the column header, the footer.
	return max(1, m.height-5-m.chromeLines())
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
	key     string
	err     error
	section int
	// refresh is the keybinding's own refresh: flag, carried on the message so the
	// handler does not have to find the keybinding again to know what to do next.
	refresh bool
	// stderr is the failed command's own last non-empty line of stderr, set only
	// when err is set. A CLI's actual error message is usually printed after any
	// warnings, so the last line rather than the first is what is worth showing -
	// without this the footer only ever said "exit status 1", which names that
	// something failed and nothing about why.
	stderr string
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
//
// A key that declared prompt: true or a choices list does not run yet - it opens
// the box, and comes back through runUserKeybindingWith or
// runUserKeybindingWithChoice once there is something to substitute.
func (m Model) runUserKeybinding(key string) (tea.Model, tea.Cmd) {
	for _, kb := range m.cfg.Keybindings.Issues {
		if kb.Key != key {
			continue
		}
		switch {
		case kb.Prompt:
			return m.openAskPrompt(key)
		case len(kb.Choices) > 0, kb.ChoicesFrom != "":
			return m.openChoosePrompt(kb)
		}
	}
	return m.runUserKeybindingWith(key, "")
}

// runUserKeybindingWith is the same, with an instruction to put in {{.Prompt}}
// and {{.Input}}.
func (m Model) runUserKeybindingWith(key, instruction string) (tea.Model, tea.Cmd) {
	return m.runConfigured(key, func(issue Issue, vars IssueVars) IssueVars {
		if instruction == "" {
			return vars
		}
		return NewAskVars(issue, AskPrompt(issue, m.detailBody, instruction), instruction)
	})
}

// runUserKeybindingWithChoice is the same, with a picked value for {{.Choice}}.
func (m Model) runUserKeybindingWithChoice(key, value string) (tea.Model, tea.Cmd) {
	return m.runConfigured(key, func(_ Issue, vars IssueVars) IssueVars {
		return vars.WithChoice(value)
	})
}

// runConfigured finds the keybinding, lets the caller fill in whatever the box it
// came from collected, and hands the rendered command to the shell.
func (m Model) runConfigured(key string, fill func(Issue, IssueVars) IssueVars) (tea.Model, tea.Cmd) {
	issue, ok := m.sections[m.active].selected()
	if !ok {
		return m, nil
	}
	for _, kb := range m.cfg.Keybindings.Issues {
		if kb.Key != key {
			continue
		}
		vars := fill(issue, NewIssueVars(issue))
		dir := m.sections[m.active].cfg.Dir
		rendered, err := RenderCommand(kb.Command, vars.WithDir(dir))
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		return m, commandCmd(kb, rendered, dir, m.active)
	}
	return m, nil
}

// commandCmd runs one configured keybinding's rendered command. terminal: true
// is the only path that goes through tea.ExecProcess: it hands the terminal
// over and repaints the whole dashboard once the command exits, which is the
// right (and only) way to give an interactive editor or pager the TTY it draws
// itself with. Every other command - the common case: a tmux pane, a browser, a
// posting CLI - never needed the terminal, so giving it up by default is both
// what stops the flicker on every keypress and what makes the command's own
// stderr reachable instead of disappearing under ExecProcess's redraw.
func commandCmd(kb Keybinding, rendered, dir string, sections ...int) tea.Cmd {
	section := -1
	if len(sections) > 0 {
		section = sections[0]
	}
	// The command's own cwd as well as {{.Dir}}, so a command that does not spawn
	// anything - `git log`, an editor - lands in the right checkout without the
	// config having to cd. LoadConfig has already checked the path exists, so
	// this cannot fail the command with a directory error.
	if kb.Terminal {
		cmd := exec.Command("sh", "-c", rendered)
		cmd.Dir = dir
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			return commandRanMsg{key: kb.Key, err: err, refresh: kb.Refresh, section: section}
		})
	}
	return func() tea.Msg {
		// Bounded by fetchTimeout, the same convention fetchSection and the other
		// API calls use: this runs on its own goroutine so the event loop keeps
		// turning, but a hung command must not leave the key looking dead forever.
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "sh", "-c", rendered)
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		msg := commandRanMsg{key: kb.Key, refresh: kb.Refresh, section: section}
		if err := cmd.Run(); err != nil {
			msg.err = err
			msg.stderr = lastMeaningfulLine(stderr.String())
		}
		return msg
	}
}

// lastMeaningfulLine picks the line a failed command's stderr is worth showing
// in a one-line footer. The last non-empty line rather than the first: a CLI
// commonly prints warnings before its actual error, so the first line is the
// least likely one to say what went wrong.
func lastMeaningfulLine(stderr string) string {
	lines := strings.Split(stderr, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return Truncate(line, stderrSnippetMaxLen)
		}
	}
	return ""
}
