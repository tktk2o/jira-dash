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
	colKey      = 10
	colIcon     = 3
	colStatus   = 13
	colPoints   = 3
	colPriority = 7
	colAssignee = 10
	colUpdated  = 5
	colGaps     = 6 // single spaces between the seven columns

	// The whole meta line. Named so the column header and the row cannot drift
	// apart: a header that does not line up with its rows is worse than none.
	rowFixedWidth = colKey + colIcon + colStatus + colPoints +
		colPriority + colAssignee + colUpdated + colGaps

	// "→ " / "  " drawn by View in front of every row.
	cursorMarkerWidth = 2

	// Chrome around the preview viewport, which it does not draw itself.
	borderChrome = 2 // the rounded border, both edges of one axis
	paneGap      = 1 // the space View puts between table and preview
	// Lines View spends on chrome above and below the table: the tab strip, the
	// rule under it, the three-line query box, the column header, the footer and
	// the prompt line.
	verticalChrome = 8
)

type styles struct {
	activeTab   lipgloss.Style
	inactiveTab lipgloss.Style
	selectedRow lipgloss.Style
	row         lipgloss.Style
	footer      lipgloss.Style
	border      lipgloss.Style
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
		border:      lipgloss.NewStyle().Foreground(border).Border(lipgloss.RoundedBorder()),
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

// renderQueryBox shows what the section actually asks Jira for. Two tabs can
// look alike and query entirely different things, and sprintPrefix narrows the
// result after the query, so it belongs here too or the row count looks wrong
// for the JQL beside it.
//
// The JQL is folded onto one line: a config may write it across several lines
// with YAML's >- and the newlines would break the frame.
func renderQueryBox(m Model, width int) string {
	sec := m.sections[m.active].cfg
	query := strings.Join(strings.Fields(sec.JQL), " ")
	if sec.SprintPrefix != "" {
		query += "  ·  sprint ^ " + sec.SprintPrefix
	}

	// 2 for the frame's own columns, 2 for the padding inside it.
	inner := maxInt(0, width-4)
	st := lipgloss.NewStyle().
		Foreground(lipgloss.Color(orDefault(m.cfg.Theme.Colors.Text.Secondary, "#6272a4"))).
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(inner)
	return st.Render(Truncate(query, inner))
}

// renderColumnHeader labels the columns renderRow lays out, using the same
// widths - a header that does not line up with its rows is worse than none.
func renderColumnHeader(width int) string {
	header := strings.Join([]string{
		runewidth.FillRight("KEY", colKey),
		runewidth.FillRight("T", colIcon),
		runewidth.FillRight("STATUS", colStatus),
		runewidth.FillLeft("SP", colPoints),
		runewidth.FillRight("PRIO", colPriority),
		runewidth.FillRight("ASSIGNEE", colAssignee),
		runewidth.FillLeft("AGE", colUpdated),
	}, " ")
	return Truncate(header, width)
}

// renderRow lays out one issue. The summary takes whatever is left, so a
// narrow terminal loses summary text rather than the key or the status.
//
// Padding goes through runewidth, not fmt: `%-*s` pads to a rune count, so a
// Japanese summary would end up the wrong number of cells wide and the columns
// would stop lining up between rows.
func renderRow(i Issue, width int, now time.Time) string {
	meta := strings.Join([]string{
		runewidth.FillRight(Truncate(i.Key, colKey), colKey),
		runewidth.FillRight(TypeIcon(i.Type), colIcon),
		runewidth.FillRight(Truncate(i.Status, colStatus), colStatus),
		runewidth.FillLeft(StoryPointText(i.StoryPoints), colPoints),
		runewidth.FillRight(Truncate(orDefault(i.Priority, "-"), colPriority), colPriority),
		runewidth.FillRight(Truncate(i.AssigneeName(), colAssignee), colAssignee),
		runewidth.FillLeft(RelTime(now, i.Updated.Time), colUpdated),
	}, " ")

	// The summary is indented under the meta line so the eye can tell the two
	// apart at a glance, the way gh-dash indents a PR title under its metadata.
	summary := "  " + Truncate(i.Summary, maxInt(0, width-2))

	// The meta columns alone are 50 cells, so a very narrow terminal cannot fit
	// them. Cutting each line keeps the invariant that a row never draws wider
	// than the width it was handed - without it the table spills past the pane
	// and the preview beside it gets pushed off screen.
	return Truncate(meta, width) + "\n" + Truncate(summary, width)
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

	age := RelTime(m.now(), s.fetchedAt)
	state := "updated " + age + " ago"
	if s.fetchedAt.IsZero() {
		state = "never fetched"
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

func renderHelp() string {
	return strings.Join([]string{
		"h/l ←/→ tab    switch section",
		"j/k gg/G       move",
		"p              toggle preview",
		"ctrl+d/ctrl+u  scroll the preview",
		"/              filter (esc clears)",
		"r              refresh section",
		"y/Y            copy key / url",
		"?              help",
		"q              quit",
		"",
		"create keys come from the config (esc cancels)",
	}, "\n")
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
	if m.showHelp {
		return renderHelp()
	}

	st := newStyles(m.cfg.Theme)
	s := m.sections[m.active]

	tableWidth := m.width
	showPreview := PreviewVisible(m.previewOpen, m.width, m.cfg.Defaults.Preview.Width)
	if showPreview {
		tableWidth = m.width - int(float64(m.width)*m.cfg.Defaults.Preview.Width)
	}

	rows := make([]string, 0, len(s.visible()))
	now := m.now()
	// The cursor marker is drawn outside renderRow, so it has to come out of
	// the row's budget or every line would be two cells wider than the pane it
	// was measured for.
	rowWidth := tableWidth - cursorMarkerWidth
	rule := st.footer.Render(" " + strings.Repeat("─", maxInt(0, tableWidth-1)))
	for idx, issue := range s.visible() {
		// The arrow marks the row once, on its first line; the fill is what
		// carries the selection across both. Repeating the arrow on the summary
		// line read as a second, pointless pointer.
		style := st.row
		marker := "  "
		if idx == s.cursor {
			style, marker = st.selectedRow, "→ "
		}
		for i, line := range strings.Split(renderRow(issue, rowWidth, now), "\n") {
			prefix := marker
			if i > 0 {
				prefix = strings.Repeat(" ", cursorMarkerWidth)
			}
			rows = append(rows, style.Render(prefix+line))
		}
		// A rule between rows, not after the last one: with two-line rows the
		// next issue's meta line otherwise reads as part of this summary.
		if idx < len(s.visible())-1 {
			rows = append(rows, rule)
		}
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

	table := strings.Join(rows, "\n")
	body := table
	if showPreview {
		// The border is what separates the two panes; without it the markdown
		// ran straight into the table rows and read as part of them.
		body = lipgloss.JoinHorizontal(lipgloss.Top, table, " ", st.border.Render(m.detail.View()))
	}

	// The create prompt takes the filter's line instead of adding one, so the
	// table does not shift by a row when it opens.
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

	// The chrome above the table, in the order gh-dash stacks it: tabs, a rule
	// across the whole width, the query the tab is showing, then the column
	// names. verticalChrome has to match how many lines this adds, or the
	// preview viewport is sized for a taller pane than it gets.
	sections := []string{
		renderTabs(m),
		st.footer.Render(strings.Repeat("━", maxInt(0, m.width))),
		renderQueryBox(m, tableWidth),
		st.footer.Render(strings.Repeat(" ", cursorMarkerWidth) + renderColumnHeader(rowWidth)),
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
	return strings.Join(sections, "\n")
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
