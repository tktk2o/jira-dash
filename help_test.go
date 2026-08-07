package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

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
