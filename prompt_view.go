package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// renderCreatePrompt states where the new issue will land before it is created,
// because the project and sprint are inherited rather than typed: without them
// on screen there is nothing to check against before pressing enter.
func createPromptTitle(m Model) string {
	row, ok := m.sections[m.active].selected()
	if !ok {
		return fmt.Sprintf("new %s…", m.createType)
	}
	if m.createParent {
		return fmt.Sprintf("new %s under %s…", m.createType, row.Key)
	}
	target := row.Project.Key
	if sprint, ok := row.CurrentSprint(); ok {
		target += " / " + sprint.Name
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
