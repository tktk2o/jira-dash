package main

import (
	"strings"
	"testing"
)

func sampleIssue() Issue {
	name := "Alice Example"
	i := Issue{
		Key:      "ABC-1234",
		Summary:  "トークン更新で 500 が出る",
		Status:   "In Progress",
		Assignee: &name,
		URL:      "https://example.atlassian.net/browse/ABC-1234",
	}
	i.Project.Key = "ABC"
	return i
}

func TestRenderCommandExpandsEveryVariable(t *testing.T) {
	tmpl := "{{.IssueKey}} {{.IssueURL}} {{.Summary}} {{.Status}} {{.Assignee}} {{.ProjectKey}}"

	got, err := RenderCommand(tmpl, NewIssueVars(sampleIssue()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"ABC-1234",
		"https://example.atlassian.net/browse/ABC-1234",
		"トークン更新で 500 が出る",
		"In Progress",
		"Alice Example",
		"ABC",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered command missing %q: %s", want, got)
		}
	}
}

func TestRenderCommandRejectsUnknownVariable(t *testing.T) {
	if _, err := RenderCommand("open {{.Nope}}", NewIssueVars(sampleIssue())); err == nil {
		t.Fatal("an unknown variable must be an error, not an empty string in a shell command")
	}
}

func TestRenderCommandRejectsBrokenTemplate(t *testing.T) {
	if _, err := RenderCommand("open {{.IssueKey", NewIssueVars(sampleIssue())); err == nil {
		t.Fatal("want a parse error")
	}
}

func TestNewIssueVarsFallsBackForMissingAssignee(t *testing.T) {
	if got := NewIssueVars(Issue{Key: "ABC-1"}).Assignee; got != "-" {
		t.Errorf("assignee = %q, want -", got)
	}
}
