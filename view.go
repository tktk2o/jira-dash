package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Column widths. Only the summary grows; the rest are fixed so the columns
// line up between rows and between sections.
const (
	colKey     = 10
	colIcon    = 3
	colStatus  = 13
	colUpdated = 5
	colGaps    = 4 // single spaces between the five columns

	// "→ " / "  " drawn by View in front of every row.
	cursorMarkerWidth = 2

	// Chrome around the preview viewport, which it does not draw itself.
	borderChrome   = 2 // the rounded border, both edges of one axis
	paneGap        = 1 // the space View puts between table and preview
	verticalChrome = 3 // tab strip, footer, filter line
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
		if i == m.active {
			label = fmt.Sprintf("%s (%d)", label, len(s.visible()))
			parts = append(parts, st.activeTab.Render(label))
			continue
		}
		parts = append(parts, st.inactiveTab.Render(label))
	}
	return strings.Join(parts, "│")
}

// renderRow lays out one issue. The summary takes whatever is left, so a
// narrow terminal loses summary text rather than the key or the status.
//
// Padding goes through runewidth, not fmt: `%-*s` pads to a rune count, so a
// Japanese summary would end up the wrong number of cells wide and the columns
// would stop lining up between rows.
func renderRow(i Issue, width int, now time.Time) string {
	summaryWidth := width - colKey - colIcon - colStatus - colUpdated - colGaps
	if summaryWidth < 0 {
		summaryWidth = 0
	}
	row := strings.Join([]string{
		runewidth.FillRight(Truncate(i.Key, colKey), colKey),
		runewidth.FillRight(TypeIcon(i.Type), colIcon),
		runewidth.FillRight(Truncate(i.Summary, summaryWidth), summaryWidth),
		runewidth.FillRight(Truncate(i.Status, colStatus), colStatus),
		runewidth.FillLeft(RelTime(now, i.Updated.Time), colUpdated),
	}, " ")

	// The fixed columns alone are 35 cells, so a very narrow terminal cannot
	// fit them. Cutting the assembled row keeps the invariant that a row never
	// draws wider than the width it was handed - without it the table spills
	// past the pane and the preview beside it gets pushed off screen.
	return Truncate(row, width)
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
		state += " · refreshing"
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
	for idx, issue := range s.visible() {
		line := renderRow(issue, rowWidth, now)
		if idx == s.cursor {
			rows = append(rows, st.selectedRow.Render("→ "+line))
			continue
		}
		rows = append(rows, st.row.Render("  "+line))
	}
	if len(rows) == 0 {
		rows = append(rows, st.footer.Render("  (no issues)"))
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

	sections := []string{renderTabs(m), body}
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
