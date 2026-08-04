package main

import (
	"os"
	"testing"
	"time"
)

func TestParseSearchJSON(t *testing.T) {
	raw, err := os.ReadFile("testdata/search.json")
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ParseSearchJSON(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}

	first := issues[0]
	if first.Key != "ABC-1234" {
		t.Errorf("key = %q", first.Key)
	}
	if first.Type != "Bug" {
		t.Errorf("type = %q", first.Type)
	}
	if first.Status != "In Progress" {
		t.Errorf("status = %q", first.Status)
	}
	if first.Project.Key != "ABC" {
		t.Errorf("project key = %q", first.Project.Key)
	}
	if first.URL == "" {
		t.Error("url should come straight from the CLI, not be assembled")
	}
	if first.AssigneeName() != "Alice Example" {
		t.Errorf("assignee = %q", first.AssigneeName())
	}
	if issues[1].AssigneeName() != "-" {
		t.Errorf("a null assignee should render as -, got %q", issues[1].AssigneeName())
	}
}

// Jira returns "+0900" with no colon, which time.RFC3339 rejects.
func TestJiraTimeAcceptsJiraOffset(t *testing.T) {
	issues, err := ParseSearchJSON([]byte(
		`{"results":[{"key":"ABC-1","updated":"2026-08-04T10:15:00.000+0900"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := time.Date(2026, 8, 4, 10, 15, 0, 0, time.FixedZone("", 9*60*60))
	if !issues[0].Updated.Equal(want) {
		t.Errorf("updated = %v, want %v", issues[0].Updated.Time, want)
	}
}

func TestJiraTimeAcceptsNull(t *testing.T) {
	issues, err := ParseSearchJSON([]byte(`{"results":[{"key":"ABC-1","updated":null}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !issues[0].Updated.IsZero() {
		t.Error("a null updated should stay the zero time")
	}
}

func TestParseSearchJSONRejectsGarbage(t *testing.T) {
	if _, err := ParseSearchJSON([]byte("not json")); err == nil {
		t.Fatal("want an error")
	}
}
