package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// minTableWidth is how many columns the table needs before the preview is
// worth its space. Below this the preview closes rather than squeezing the
// summary column into uselessness.
const minTableWidth = 80

// RelTime renders an age the way the footer and the Updated column want it:
// one unit, no words.
func RelTime(now, then time.Time) string {
	if then.IsZero() {
		return "-"
	}
	d := now.Sub(then)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}

// Truncate cuts to a display width, not a rune count: Japanese summaries are
// two cells per rune and would otherwise overflow the column.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= w {
		return s
	}
	return runewidth.Truncate(s, w, "…")
}

// TypeIcon returns a two-cell icon for every type, including the fallback.
// Mixing widths here would shift every column to its right depending on the
// issue type, so candidates that compute as one cell are avoided: ⚙️ carries a
// variation selector, 🗂 is one cell, and • is one cell.
// A Jira site returns the type name in its own language, so both spellings of
// each default type are listed. A workspace-custom type ("Architecture Spike"
// and the like) matches neither and keeps the fallback - its name would be
// workspace-specific and this repo is public.
func TypeIcon(issueType string) string {
	switch strings.ToLower(issueType) {
	case "bug", "バグ":
		return "🐞"
	case "story", "ストーリー":
		return "📘"
	// Both Japanese names are listed literally: サブタスク ends with タスク, so
	// matching on a suffix would collapse them by accident.
	case "task", "sub-task", "subtask", "タスク", "サブタスク":
		return "🔧"
	case "epic", "エピック":
		return "🧩"
	default:
		return "📄"
	}
}

// PreviewVisible answers whether the preview pane fits. The config wins when
// it says closed; otherwise the terminal decides.
func PreviewVisible(configOpen bool, totalWidth int, ratio float64) bool {
	if !configOpen {
		return false
	}
	return totalWidth-int(float64(totalWidth)*ratio) >= minTableWidth
}
