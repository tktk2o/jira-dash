package main

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

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
