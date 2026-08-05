package main

import (
	"encoding/json"
	"testing"
	"time"
)

// Adapter must satisfy Searcher, so the model can take a fake in tests and
// the real thing at runtime without either knowing about the other.
var _ Searcher = Adapter{}

// Jira returns "+0900" with no colon, which time.RFC3339 rejects. internal/jira
// has no test of its own for this - JiraTime only gets exercised there through
// full REST response fixtures - so it stays here, against the alias.
func TestJiraTimeAcceptsJiraOffset(t *testing.T) {
	var issue Issue
	if err := json.Unmarshal([]byte(
		`{"key":"ABC-1","updated":"2026-08-04T10:15:00.000+0900"}`), &issue); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := time.Date(2026, 8, 4, 10, 15, 0, 0, time.FixedZone("", 9*60*60))
	if !issue.Updated.Equal(want) {
		t.Errorf("updated = %v, want %v", issue.Updated.Time, want)
	}
}

func TestJiraTimeAcceptsNull(t *testing.T) {
	var issue Issue
	if err := json.Unmarshal([]byte(`{"key":"ABC-1","updated":null}`), &issue); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !issue.Updated.IsZero() {
		t.Error("a null updated should stay the zero time")
	}
}

// Jira returns null for a time it does not have, and the four-character string
// "null" is a different thing - only the JSON decoder can tell them apart.
func TestJiraTimeAcceptsNullAndRejectsNonsense(t *testing.T) {
	var payload struct {
		Updated JiraTime `json:"updated"`
	}
	if err := json.Unmarshal([]byte(`{"updated":null}`), &payload); err != nil {
		t.Fatalf("a null time should unmarshal as absent: %v", err)
	}
	if !payload.Updated.IsZero() {
		t.Error("a null time should be the zero time")
	}
	if err := json.Unmarshal([]byte(`{"updated":"not a time"}`), &payload); err == nil {
		t.Error("an unparseable time should be an error, not the zero time")
	}
	if err := json.Unmarshal([]byte(`{"updated":12345}`), &payload); err == nil {
		t.Error("a number should be an error, not a time")
	}
}

// A rotating sprint name ("Team 0803-0807") cannot be matched in JQL: the
// sprint field takes no LIKE operator, and `sprint ~ "Team"` was measured
// returning 2 of the sprint's 15 issues. The prefix match therefore happens
// here, against the row's own sprint list.
func TestInActiveSprintPrefix(t *testing.T) {
	inSprint := Issue{Sprint: []Sprint{
		{Name: "Team 0727-0731", State: "closed"},
		{Name: "Team 0803-0807", State: "active"},
	}}
	otherTeam := Issue{Sprint: []Sprint{{Name: "Other 0803-0807", State: "active"}}}
	// The prefix must not match a sprint the team has already finished, or a
	// closed sprint would keep an issue on the board forever.
	closedOnly := Issue{Sprint: []Sprint{{Name: "Team 0727-0731", State: "closed"}}}
	// A parked issue sits in a future sprint used as a named backlog; it is not
	// in the active sprint.
	parked := Issue{Sprint: []Sprint{{Name: "Team backlog", State: "future"}}}

	for name, tc := range map[string]struct {
		issue Issue
		want  bool
	}{
		"active sprint with the prefix": {inSprint, true},
		"another team's sprint":         {otherTeam, false},
		"only a closed sprint":          {closedOnly, false},
		"a future backlog sprint":       {parked, false},
		"no sprint at all":              {Issue{}, false},
	} {
		if got := tc.issue.InActiveSprintPrefix("Team"); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// An empty prefix means the section did not ask for one, so nothing is dropped.
func TestInActiveSprintPrefixIsOffWhenEmpty(t *testing.T) {
	if !(Issue{}).InActiveSprintPrefix("") {
		t.Error("an empty prefix should keep every issue")
	}
}

// Creating an issue "in the same place" means the sprint of the row you are
// standing on. Closed sprints are skipped: an issue keeps every sprint it has
// ever been in, so the most recent one is not necessarily the current one.
func TestCurrentSprintPrefersActiveThenFuture(t *testing.T) {
	active := Sprint{ID: 3, Name: "Team 0803-0807", State: "active"}
	future := Sprint{ID: 4, Name: "Team backlog", State: "future"}
	closed := Sprint{ID: 1, Name: "Team 0727-0731", State: "closed"}

	for name, tc := range map[string]struct {
		in   []Sprint
		want int
		ok   bool
	}{
		"active wins over a backlog": {[]Sprint{closed, future, active}, 3, true},
		"a backlog when none active": {[]Sprint{closed, future}, 4, true},
		"closed only is no sprint":   {[]Sprint{closed}, 0, false},
		"no sprint at all":           {nil, 0, false},
	} {
		got, ok := Issue{Sprint: tc.in}.CurrentSprint()
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", name, ok, tc.ok)
			continue
		}
		if got.ID != tc.want {
			t.Errorf("%s: sprint id = %d, want %d", name, got.ID, tc.want)
		}
	}
}
