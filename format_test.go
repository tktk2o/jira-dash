package main

import (
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
)

func TestRelTime(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		then time.Time
		want string
	}{
		{"seconds ago", now.Add(-30 * time.Second), "now"},
		{"minutes ago", now.Add(-5 * time.Minute), "5m"},
		{"hours ago", now.Add(-2 * time.Hour), "2h"},
		{"yesterday", now.Add(-26 * time.Hour), "1d"},
		{"a week ago", now.Add(-7 * 24 * time.Hour), "7d"},
		{"never", time.Time{}, "-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RelTime(now, c.then); got != c.want {
				t.Errorf("RelTime = %q, want %q", got, c.want)
			}
		})
	}
}

// Truncation is by display width: a Japanese summary is 2 cells per rune, so
// counting runes would overflow the column and break the table borders.
func TestTruncateUsesDisplayWidth(t *testing.T) {
	s := "トークン更新で 500 が出る"

	got := Truncate(s, 10)
	if w := runewidth.StringWidth(got); w > 10 {
		t.Errorf("width = %d, want <= 10 (got %q)", w, got)
	}
	if got == s {
		t.Error("a long string should have been shortened")
	}

	if got := Truncate("short", 20); got != "short" {
		t.Errorf("a string that fits must be untouched, got %q", got)
	}
	if got := Truncate("anything", 0); got != "" {
		t.Errorf("zero width = %q, want empty", got)
	}
}

func TestTypeIcon(t *testing.T) {
	for input, want := range map[string]string{
		"Bug":      "🐞",
		"bug":      "🐞",
		"Story":    "📘",
		"Task":     "🔧",
		"Sub-task": "🔧",
		"Epic":     "🧩",
		"Whatever": "📄",
		"":         "📄",

		// A real Jira returns the type in the site's language, so the English
		// names above never match on a Japanese site. Measured against a live
		// instance: 100 issues came back as ストーリー / タスク / サブタスク /
		// バグ / エピック plus workspace-custom names.
		"ストーリー": "📘",
		"タスク":   "🔧",
		"サブタスク": "🔧",
		"バグ":    "🐞",
		"エピック":  "🧩",

		// A custom type is not in either language's defaults and stays on the
		// fallback; its name would be workspace-specific and this repo is public.
		"運用系タスク": "📄",
	} {
		if got := TypeIcon(input); got != want {
			t.Errorf("TypeIcon(%q) = %q, want %q", input, got, want)
		}
	}
}

// Every icon must occupy the same number of cells, or the column after it
// shifts depending on the issue type. Measured with runewidth v0.0.27: the
// obvious picks ⚙️ (U+2699 plus a variation selector) and 🗂 compute as 1 cell
// while most terminals draw them as 2, and • is genuinely 1 — all three would
// misalign the table.
func TestTypeIconsAreAllTwoCellsWide(t *testing.T) {
	for _, input := range []string{"Bug", "Story", "Task", "Sub-task", "Epic", "Whatever", "",
		"ストーリー", "タスク", "サブタスク", "バグ", "エピック", "運用系タスク"} {
		if got := runewidth.StringWidth(TypeIcon(input)); got != 2 {
			t.Errorf("TypeIcon(%q) is %d cells wide, want 2", input, got)
		}
	}
}

// gh-dash drops to a stacked layout when the table would be squeezed; here the
// preview simply closes, so a narrow terminal still shows a usable table.
func TestPreviewVisible(t *testing.T) {
	if PreviewVisible(true, 200, 0.5) != true {
		t.Error("a wide terminal should keep the preview open")
	}
	if PreviewVisible(true, 120, 0.5) != false {
		t.Error("60 columns left for the table is too few; preview should close")
	}
	if PreviewVisible(false, 200, 0.5) != false {
		t.Error("preview: false in config must win")
	}
}
