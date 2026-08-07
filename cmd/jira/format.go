package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	jirapkg "jira-dash/internal/jira"
)

// outputFormat is the value -f/--format takes on every subcommand that has
// one.
type outputFormat string

const (
	formatTable    outputFormat = "table"
	formatJSON     outputFormat = "json"
	formatYAML     outputFormat = "yaml"
	formatMarkdown outputFormat = "markdown"
)

// parseFormat validates -f/--format's value, defaulting to table exactly
// like every old-CLI subcommand that has the flag. Only `get` also accepts
// yaml/markdown (see parseGetFormat) - every other subcommand's old-CLI
// --help lists table/json alone, and adding the other two there would be
// inventing a capability the tool never had rather than restoring one.
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

// parseGetFormat is `jira get -f`'s own validator: the old CLI's `get
// --help` lists four formats, not two, and rejecting an unfamiliar value
// must always name all of them rather than leaving the caller to guess
// which two this replacement kept.
func parseGetFormat(s string) (outputFormat, error) {
	switch outputFormat(s) {
	case "", formatTable:
		return formatTable, nil
	case formatJSON:
		return formatJSON, nil
	case formatYAML:
		return formatYAML, nil
	case formatMarkdown:
		return formatMarkdown, nil
	default:
		return "", fmt.Errorf("unknown format %q: want table, json, yaml, or markdown", s)
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
	switch format {
	case formatJSON:
		return writeJSON(w, getOutput{Issue: issue, Description: description})
	case formatYAML:
		return writeGetYAML(w, issue, description)
	case formatMarkdown:
		return writeGetMarkdown(w, issue, description)
	}
	_, _ = fmt.Fprintf(w, "%s  %s\n", issue.Key, issue.Summary)
	_, _ = fmt.Fprintf(w, "Type:     %s\n", issue.Type)
	_, _ = fmt.Fprintf(w, "Status:   %s\n", issue.Status)
	_, _ = fmt.Fprintf(w, "Priority: %s\n", issue.Priority)
	_, _ = fmt.Fprintf(w, "Assignee: %s\n", issue.AssigneeName())
	if len(issue.Labels) > 0 {
		_, _ = fmt.Fprintf(w, "Labels:   %s\n", strings.Join(issue.Labels, ", "))
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, description)
	return nil
}

// yamlScalar quotes s only when a bare YAML scalar would parse it as
// something other than the literal string - a leading/trailing space, a
// colon-space sequence, or an embedded quote all change meaning unquoted.
// Handling that subset (rather than pulling in a YAML library, which the
// migration plan's own constraints forbid) is enough for the values `get`
// ever prints: issue text, never nested structures.
func yamlScalar(s string) string {
	needsQuote := s == "" || strings.ContainsAny(s, ":#\"'\n") ||
		strings.TrimSpace(s) != s
	if !needsQuote {
		return s
	}
	return strconv.Quote(s)
}

// writeGetYAML is `jira get -f yaml`'s output: the same fields the table
// view shows, plus the description, since yaml (like markdown) is a human
// format the old CLI offered as an alternative to table, not a second
// machine format alongside json.
func writeGetYAML(w io.Writer, issue jirapkg.Issue, description string) error {
	_, _ = fmt.Fprintf(w, "key: %s\n", yamlScalar(issue.Key))
	_, _ = fmt.Fprintf(w, "summary: %s\n", yamlScalar(issue.Summary))
	_, _ = fmt.Fprintf(w, "type: %s\n", yamlScalar(issue.Type))
	_, _ = fmt.Fprintf(w, "status: %s\n", yamlScalar(issue.Status))
	_, _ = fmt.Fprintf(w, "priority: %s\n", yamlScalar(issue.Priority))
	_, _ = fmt.Fprintf(w, "assignee: %s\n", yamlScalar(issue.AssigneeName()))
	if len(issue.Labels) == 0 {
		_, _ = fmt.Fprintln(w, "labels: []")
	} else {
		_, _ = fmt.Fprintln(w, "labels:")
		for _, l := range issue.Labels {
			_, _ = fmt.Fprintf(w, "  - %s\n", yamlScalar(l))
		}
	}
	_, _ = fmt.Fprintln(w, "description: |")
	for _, line := range strings.Split(description, "\n") {
		_, _ = fmt.Fprintf(w, "  %s\n", line)
	}
	return nil
}

// writeGetMarkdown is `jira get -f markdown`'s output: a heading plus a
// bullet list of the same fields, the shape a person would paste into a
// PR description or a chat message.
func writeGetMarkdown(w io.Writer, issue jirapkg.Issue, description string) error {
	_, _ = fmt.Fprintf(w, "# %s: %s\n\n", issue.Key, issue.Summary)
	_, _ = fmt.Fprintf(w, "- **Type:** %s\n", issue.Type)
	_, _ = fmt.Fprintf(w, "- **Status:** %s\n", issue.Status)
	_, _ = fmt.Fprintf(w, "- **Priority:** %s\n", issue.Priority)
	_, _ = fmt.Fprintf(w, "- **Assignee:** %s\n", issue.AssigneeName())
	if len(issue.Labels) > 0 {
		_, _ = fmt.Fprintf(w, "- **Labels:** %s\n", strings.Join(issue.Labels, ", "))
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, description)
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
		_, _ = fmt.Fprintln(w, "No issues found")
		return nil
	}
	_, _ = fmt.Fprintf(w, "Found %d issue(s)\n\n", len(issues))
	_, _ = fmt.Fprintf(w, "%-12s %-10s %-14s %-18s %s\n", "Key", "Type", "Status", "Assignee", "Summary")
	for _, issue := range issues {
		_, _ = fmt.Fprintf(w, "%-12s %-10s %-14s %-18s %s\n", issue.Key, issue.Type, issue.Status, issue.AssigneeName(), issue.Summary)
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
	_, _ = fmt.Fprintf(w, "Comment %s added\n", c.ID)
	return nil
}

func writeCreateOutput(w io.Writer, format outputFormat, issue jirapkg.Issue) error {
	if format == formatJSON {
		return writeJSON(w, issue)
	}
	_, _ = fmt.Fprintf(w, "Created %s: %s\n", issue.Key, issue.Summary)
	return nil
}

func writeEditOutput(w io.Writer, format outputFormat, key string) error {
	if format == formatJSON {
		return writeJSON(w, map[string]string{"key": key, "status": "updated"})
	}
	_, _ = fmt.Fprintf(w, "Updated %s\n", key)
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
		_, _ = fmt.Fprintln(w, "No transitions available")
		return nil
	}
	for _, t := range transitions {
		_, _ = fmt.Fprintf(w, "%-6s %s\n", t.ID, t.Name)
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
		_, _ = fmt.Fprintln(w, "No assignable users found")
		return nil
	}
	for _, u := range users {
		_, _ = fmt.Fprintf(w, "%-24s %-28s %s\n", u.AccountID, u.DisplayName, u.Email)
	}
	return nil
}

// jqlSuggestOutput is `jira jql suggest`'s own shape - a new subcommand per
// the migration plan, with no prior CLI output to stay byte-compatible
// with.
type jqlSuggestOutput struct {
	Suggestions []jirapkg.Suggestion `json:"suggestions"`
}

func writeJQLSuggestOutput(w io.Writer, format outputFormat, suggestions []jirapkg.Suggestion) error {
	if format == formatJSON {
		return writeJSON(w, jqlSuggestOutput{Suggestions: suggestions})
	}
	if len(suggestions) == 0 {
		_, _ = fmt.Fprintln(w, "No suggestions found")
		return nil
	}
	for _, s := range suggestions {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", s.Value, s.DisplayName)
	}
	return nil
}
