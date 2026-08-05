package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	jirapkg "jira-dash/internal/jira"
)

// outputFormat is the value -f/--format takes on every subcommand that has
// one. The old CLI also accepted "yaml" and "markdown" on `get`; neither is
// wired to anything else in this codebase (the TUI only ever asked for
// json), so they are left out rather than half-supported.
type outputFormat string

const (
	formatTable outputFormat = "table"
	formatJSON  outputFormat = "json"
)

// parseFormat validates -f/--format's value, defaulting to table exactly
// like every old-CLI subcommand that has the flag.
func parseFormat(s string) (outputFormat, error) {
	switch outputFormat(s) {
	case "", formatTable:
		return formatTable, nil
	case formatJSON:
		return formatJSON, nil
	default:
		return "", fmt.Errorf("unknown format %q: want table or json", s)
	}
}

// writeJSON marshals v with indentation and a trailing newline, matching
// how a person reads `-f json` output at a terminal (the old CLI pretty-
// printed too; a script piping through jq does not care either way).
func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// getOutput is what `jira get -f json` prints: Issue's own fields (the
// shape Task 2 fixed, which the TUI's search path also depends on) plus the
// description that only `get` fetches. Parent, flagged and created - which
// the old TypeScript CLI's `get` also printed - are not here because Issue
// itself (internal/jira, out of this task's scope) carries none of the
// three; see this task's own report for why that line was not moved.
type getOutput struct {
	jirapkg.Issue
	Description string `json:"description"`
}

func writeGetOutput(w io.Writer, format outputFormat, issue jirapkg.Issue, description string) error {
	if format == formatJSON {
		return writeJSON(w, getOutput{Issue: issue, Description: description})
	}
	fmt.Fprintf(w, "%s  %s\n", issue.Key, issue.Summary)
	fmt.Fprintf(w, "Type:     %s\n", issue.Type)
	fmt.Fprintf(w, "Status:   %s\n", issue.Status)
	fmt.Fprintf(w, "Priority: %s\n", issue.Priority)
	fmt.Fprintf(w, "Assignee: %s\n", issue.AssigneeName())
	if len(issue.Labels) > 0 {
		fmt.Fprintf(w, "Labels:   %s\n", strings.Join(issue.Labels, ", "))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, description)
	return nil
}

// searchOutput is what `jira search -f json` prints. JQL is included
// because the old CLI echoed the resolved query - useful evidence when a
// filter combination produced fewer rows than expected - and it costs
// nothing extra to carry here since the caller already built it.
type searchOutput struct {
	JQL     string          `json:"jql"`
	Total   int             `json:"total"`
	Results []jirapkg.Issue `json:"results"`
}

func writeSearchOutput(w io.Writer, format outputFormat, jql string, issues []jirapkg.Issue) error {
	if format == formatJSON {
		return writeJSON(w, searchOutput{JQL: jql, Total: len(issues), Results: issues})
	}
	if len(issues) == 0 {
		fmt.Fprintln(w, "No issues found")
		return nil
	}
	fmt.Fprintf(w, "Found %d issue(s)\n\n", len(issues))
	fmt.Fprintf(w, "%-12s %-10s %-14s %-18s %s\n", "Key", "Type", "Status", "Assignee", "Summary")
	for _, issue := range issues {
		fmt.Fprintf(w, "%-12s %-10s %-14s %-18s %s\n", issue.Key, issue.Type, issue.Status, issue.AssigneeName(), issue.Summary)
	}
	return nil
}

// commentListOutput is what `jira comment list` always prints - that
// subcommand takes no -f flag in the old CLI, JSON is the only shape it
// ever had.
type commentListOutput struct {
	IssueKey string            `json:"issue_key"`
	Total    int               `json:"total"`
	Comments []jirapkg.Comment `json:"comments"`
}

func writeCommentListOutput(w io.Writer, key string, comments []jirapkg.Comment) error {
	return writeJSON(w, commentListOutput{IssueKey: key, Total: len(comments), Comments: comments})
}

func writeCommentAddOutput(w io.Writer, format outputFormat, c jirapkg.Comment) error {
	if format == formatJSON {
		return writeJSON(w, c)
	}
	fmt.Fprintf(w, "Comment %s added\n", c.ID)
	return nil
}

func writeCreateOutput(w io.Writer, format outputFormat, issue jirapkg.Issue) error {
	if format == formatJSON {
		return writeJSON(w, issue)
	}
	fmt.Fprintf(w, "Created %s: %s\n", issue.Key, issue.Summary)
	return nil
}

func writeEditOutput(w io.Writer, format outputFormat, key string) error {
	if format == formatJSON {
		return writeJSON(w, map[string]string{"key": key, "status": "updated"})
	}
	fmt.Fprintf(w, "Updated %s\n", key)
	return nil
}

// transitionsOutput is `jira transitions`'s own shape - a new subcommand
// per the migration plan, with no prior CLI output to stay byte-compatible
// with.
type transitionsOutput struct {
	Transitions []jirapkg.Transition `json:"transitions"`
}

func writeTransitionsOutput(w io.Writer, format outputFormat, transitions []jirapkg.Transition) error {
	if format == formatJSON {
		return writeJSON(w, transitionsOutput{Transitions: transitions})
	}
	if len(transitions) == 0 {
		fmt.Fprintln(w, "No transitions available")
		return nil
	}
	for _, t := range transitions {
		fmt.Fprintf(w, "%-6s %s\n", t.ID, t.Name)
	}
	return nil
}

// usersOutput is `jira users assignable`'s own shape - also new, see
// transitionsOutput's note.
type usersOutput struct {
	Users []jirapkg.User `json:"users"`
}

func writeUsersOutput(w io.Writer, format outputFormat, users []jirapkg.User) error {
	if format == formatJSON {
		return writeJSON(w, usersOutput{Users: users})
	}
	if len(users) == 0 {
		fmt.Fprintln(w, "No assignable users found")
		return nil
	}
	for _, u := range users {
		fmt.Fprintf(w, "%-24s %-28s %s\n", u.AccountID, u.DisplayName, u.Email)
	}
	return nil
}
