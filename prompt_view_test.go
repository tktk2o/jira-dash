package main

import (
	"strings"
	"testing"
)

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

// A parent create's title names the row it will attach to, not a project and
// sprint - the "in ABC / sprint" phrasing does not apply to a subtask, whose
// project and sprint both come from the parent, not the row.
func TestCreateBoxNamesTheParentWhenCreatingASubtask(t *testing.T) {
	var got []NewIssueRequest
	m := createTestModel(t, &got)
	m = press(m, "s")

	out := plain(m.View())
	if !strings.Contains(out, "サブタスク") || !strings.Contains(out, "ABC-1") {
		t.Errorf("the box should name the parent row: %q", out)
	}
	if !strings.Contains(out, "under") {
		t.Errorf("the box should say it is creating a child: %q", out)
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
