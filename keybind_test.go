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
		"'ABC-1234'",
		"'https://example.atlassian.net/browse/ABC-1234'",
		"'トークン更新で 500 が出る'",
		"'In Progress'",
		"'Alice Example'",
		"'ABC'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered command missing %q: %s", want, got)
		}
	}
}

// A Jira title is free text written by whoever filed the issue, and the
// rendered command goes to `sh -c`. Quoting has to make the whole title one
// inert argument.
func TestRenderCommandNeutralisesShellMetacharactersInASummary(t *testing.T) {
	issue := sampleIssue()
	issue.Summary = `x; rm -rf ~ && echo $(whoami) ` + "`id`" + ` 'quoted'`

	got, err := RenderCommand("echo {{.Summary}}", NewIssueVars(issue))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Everything after `echo ` must be a single-quoted run, so the only way to
	// leave that quoting is the escaped-quote sequence.
	body := strings.TrimPrefix(got, "echo ")
	if !strings.HasPrefix(body, "'") || !strings.HasSuffix(body, "'") {
		t.Fatalf("summary was not wrapped in single quotes: %s", got)
	}
	if strings.Contains(strings.ReplaceAll(body, `'\''`, ""), "'"+" ") {
		t.Errorf("quoting was broken out of: %s", got)
	}
	if !strings.Contains(got, `'\''quoted'\''`) {
		t.Errorf("a single quote inside the value should be escaped, got: %s", got)
	}
}

func TestShellQuoteWrapsAndEscapes(t *testing.T) {
	if got := shellQuote("plain"); got != "'plain'" {
		t.Errorf("shellQuote(plain) = %q", got)
	}
	if got := shellQuote("it's"); got != `'it'\''s'` {
		t.Errorf("shellQuote(it's) = %q", got)
	}
	if got := shellQuote(""); got != "''" {
		t.Errorf("shellQuote(empty) = %q, want '' so the argument still exists", got)
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
	if got := NewIssueVars(Issue{Key: "ABC-1"}).Assignee; got != "'-'" {
		t.Errorf("assignee = %q, want the quoted '-'", got)
	}
}
