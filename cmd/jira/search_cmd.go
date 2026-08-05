package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// jqlLiteral quotes s for use inside a JQL string literal. JQL uses double
// quotes and backslash escaping, the same as JSON string escaping for the
// characters this program's inputs ever contain (quote and backslash), so
// strconv.Quote's output - minus its own doubled quoting - is reused rather
// than writing a second escaper.
func jqlLiteral(s string) string {
	quoted := strconv.Quote(s)
	return quoted
}

// buildSearchJQL assembles a JQL query from search's filter flags, ANDing
// together whichever ones were set. Mirrors the old CLI's own filter-flag
// behavior: --jql, when given, overrides all of these (checked by the
// caller before this is reached).
func buildSearchJQL(query, project, status, assignee, issueType, label, sprint string) string {
	var clauses []string
	add := func(field, value string) {
		if value != "" {
			clauses = append(clauses, field+" = "+jqlLiteral(value))
		}
	}
	add("project", project)
	add("status", status)
	add("issuetype", issueType)
	if assignee != "" {
		// currentUser() is a JQL function, not a literal - quoting it would
		// ask Jira to match the literal string "currentUser()" and return
		// nothing, which is the one example the old CLI's own --help gives.
		if assignee == "currentUser()" {
			clauses = append(clauses, "assignee = currentUser()")
		} else {
			clauses = append(clauses, "assignee = "+jqlLiteral(assignee))
		}
	}
	add("labels", label)
	if sprint != "" {
		switch sprint {
		case "open", "closed", "future":
			clauses = append(clauses, "sprint in "+sprint+"Sprints()")
		default:
			if _, err := strconv.Atoi(sprint); err == nil {
				clauses = append(clauses, "sprint = "+sprint)
			} else {
				clauses = append(clauses, "sprint = "+jqlLiteral(sprint))
			}
		}
	}
	if query != "" {
		clauses = append(clauses, "text ~ "+jqlLiteral(query))
	}
	if len(clauses) == 0 {
		return ""
	}
	return strings.Join(clauses, " AND ") + " ORDER BY updated DESC"
}

// runSearch implements `jira search [query]`.
func runSearch(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	var project, status, assignee, issueType, label, sprint, jql, format, fields string
	var limit int
	fs.StringVar(&project, "p", "", "project key")
	fs.StringVar(&project, "project", "", "project key")
	fs.StringVar(&status, "s", "", "status")
	fs.StringVar(&status, "status", "", "status")
	fs.StringVar(&assignee, "a", "", "assignee")
	fs.StringVar(&assignee, "assignee", "", "assignee")
	fs.StringVar(&issueType, "t", "", "issue type")
	fs.StringVar(&issueType, "type", "", "issue type")
	fs.StringVar(&label, "L", "", "label")
	fs.StringVar(&label, "label", "", "label")
	fs.StringVar(&sprint, "S", "", "sprint")
	fs.StringVar(&sprint, "sprint", "", "sprint")
	fs.IntVar(&limit, "l", 20, "max results")
	fs.IntVar(&limit, "limit", 20, "max results")
	fs.StringVar(&format, "f", "table", "output format")
	fs.StringVar(&format, "format", "table", "output format")
	fs.StringVar(&jql, "jql", "", "raw JQL, overrides the other filters")
	fs.StringVar(&fields, "fields", "", "additional fields (not implemented, json only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fields != "" {
		return fmt.Errorf("search --fields is not implemented in this CLI yet")
	}
	f, err := parseFormat(format)
	if err != nil {
		return err
	}

	resolvedJQL := jql
	if resolvedJQL == "" {
		var query string
		if fs.NArg() > 0 {
			query = fs.Arg(0)
		}
		resolvedJQL = buildSearchJQL(query, project, status, assignee, issueType, label, sprint)
		if resolvedJQL == "" {
			return fmt.Errorf("search needs a query, --jql, or at least one filter flag")
		}
	}

	issues, err := client.Search(ctx, resolvedJQL, limit)
	if err != nil {
		return err
	}
	return writeSearchOutput(stdout, f, resolvedJQL, issues)
}
