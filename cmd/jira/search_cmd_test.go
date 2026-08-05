package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestBuildSearchJQLAndsEveryFilterAndQuotesValues(t *testing.T) {
	got := buildSearchJQL("payment bug", "ABC", "In Progress", "", "Bug", "urgent", "")
	for _, want := range []string{
		`project = "ABC"`, `status = "In Progress"`, `issuetype = "Bug"`,
		`labels = "urgent"`, `text ~ "payment bug"`, "AND",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("jql = %q, want it to contain %q", got, want)
		}
	}
}

// currentUser() is a JQL function the old CLI's own --help gives as the
// example value for -a/--assignee; quoting it as a string literal would
// silently break every script that passes it.
func TestBuildSearchJQLLeavesCurrentUserFunctionUnquoted(t *testing.T) {
	got := buildSearchJQL("", "", "", "currentUser()", "", "", "")
	if !strings.Contains(got, "assignee = currentUser()") {
		t.Errorf("jql = %q", got)
	}
	if strings.Contains(got, `"currentUser()"`) {
		t.Errorf("currentUser() was quoted: %q", got)
	}
}

func TestBuildSearchJQLSprintAcceptsStateKeywordsAndNumericIDs(t *testing.T) {
	if got := buildSearchJQL("", "", "", "", "", "", "open"); !strings.Contains(got, "sprint in openSprints()") {
		t.Errorf("open: jql = %q", got)
	}
	if got := buildSearchJQL("", "", "", "", "", "", "42"); !strings.Contains(got, "sprint = 42") {
		t.Errorf("numeric: jql = %q", got)
	}
	if got := buildSearchJQL("", "", "", "", "", "", "Team Sprint 3"); !strings.Contains(got, `sprint = "Team Sprint 3"`) {
		t.Errorf("name: jql = %q", got)
	}
}

func TestSearchAcceptsBothShortAndLongLimitFlag(t *testing.T) {
	for _, flagSpelling := range []string{"-l", "--limit"} {
		fc := &fakeClient{}
		if err := runSearch(context.Background(), fc, []string{"--jql", `project = "ABC"`, flagSpelling, "5"}, &bytes.Buffer{}); err != nil {
			t.Fatalf("%s: %v", flagSpelling, err)
		}
		if fc.lastSearchLimit != 5 {
			t.Errorf("%s: limit = %d, want 5", flagSpelling, fc.lastSearchLimit)
		}
	}
}

// --jql overrides every other filter flag, matching the old CLI's own
// documented behaviour ("他のフィルタオプションは無視").
func TestSearchJQLFlagOverridesOtherFilters(t *testing.T) {
	fc := &fakeClient{}
	if err := runSearch(context.Background(), fc, []string{"--jql", "raw jql", "-p", "IGNORED"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if fc.lastSearchJQL != "raw jql" {
		t.Errorf("jql = %q, want the --jql value verbatim", fc.lastSearchJQL)
	}
}

func TestSearchWithNoFiltersAndNoJQLIsAnError(t *testing.T) {
	fc := &fakeClient{}
	if err := runSearch(context.Background(), fc, nil, &bytes.Buffer{}); err == nil {
		t.Error("want an error")
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called with no query at all: %v", fc.calls)
	}
}
