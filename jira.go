package main

import (
	"context"

	jirapkg "jira-dash/internal/jira"
)

// Issue, Sprint, Comment and JiraTime live in internal/jira: the CLI and the
// TUI both need the exact same JSON shape, and a duplicate type is how that
// shape drifts silently. Aliased rather than renamed at every call site so
// this move's diff does not bury the two behavioural changes (Task 2) under
// hundreds of mechanical ones.
type Issue = jirapkg.Issue
type Sprint = jirapkg.Sprint
type Comment = jirapkg.Comment
type JiraTime = jirapkg.JiraTime

// Searcher is the only door to the outside world. Keeping it one interface
// means the whole UI is testable without a network, and that the CLI can be
// swapped for direct REST calls later without touching the model.
//
// Create is the one write in the whole program. It is on this interface rather
// than routed through a user-configured command because the summary has to be
// typed into the dashboard, and a config command cannot ask for input.
type Searcher interface {
	Search(ctx context.Context, jql string, limit int) ([]Issue, error)
	Issue(ctx context.Context, key string) (string, error)
	Comments(ctx context.Context, key string) ([]Comment, error)
	Create(ctx context.Context, req NewIssueRequest) (Issue, error)
}

// NewIssueRequest is everything the create prompt collects. Project and sprint
// are not typed by hand: they are read off the row the cursor was on, which is
// what makes a new issue land beside the ones you were looking at.
type NewIssueRequest struct {
	Project string
	Type    string
	Summary string
	// Sprint is a name, not the row's Sprint.ID, because
	// internal/jira.NewIssue only resolves sprints by name (create.go's
	// ActiveSprint, matching the old CLI's --sprint flag). That reopens the
	// exact ambiguity Sprint.ID exists to avoid - two boards can share a
	// sprint name - but there is no id-based path into Client.Create to
	// route around it without extending internal/jira, which is a different
	// task.
	Sprint string
}

// commentsMax mirrors cmd/jira's own default (comment_cmd.go's
// -m/--max-results): the preview pane has no flag to ask for more, so it
// gets the same ceiling the compat CLI defaults to.
const commentsMax = 50

// Adapter satisfies Searcher on top of *internal/jira.Client. Searcher
// speaks in the TUI's vocabulary - Issue returns rendered Markdown for the
// preview pane - while Client speaks in the API's, returning a struct.
// Bending Client to the one caller that wants prose would tie the API layer
// to this program's UI, so the mapping lives here instead.
type Adapter struct {
	Client *jirapkg.Client
}

// Search runs a section's JQL.
func (a Adapter) Search(ctx context.Context, jql string, limit int) ([]Issue, error) {
	return a.Client.Search(ctx, jql, limit)
}

// Issue returns the body of the preview: the description, and nothing else.
// The header already states the type, status, project, assignee, reporter and
// priority, so the preview would otherwise print every one of them twice.
func (a Adapter) Issue(ctx context.Context, key string) (string, error) {
	return a.Client.IssueDescription(ctx, key)
}

// Comments reads an issue's thread.
func (a Adapter) Comments(ctx context.Context, key string) ([]Comment, error) {
	return a.Client.Comments(ctx, key, commentsMax)
}

// Create files a new issue. It is the one call in this program that writes.
func (a Adapter) Create(ctx context.Context, req NewIssueRequest) (Issue, error) {
	return a.Client.Create(ctx, jirapkg.NewIssue{
		ProjectKey: req.Project,
		Type:       req.Type,
		Summary:    req.Summary,
		Sprint:     req.Sprint,
	})
}
