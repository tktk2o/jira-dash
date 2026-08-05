// Package main is the `jira` CLI: a drop-in replacement for the old
// TypeScript tool, backed by internal/jira instead of a subprocess.
package main

import (
	"context"

	jirapkg "jira-dash/internal/jira"
)

// jiraClient is the subset of *jirapkg.Client every subcommand needs. Each
// subcommand takes this interface rather than the concrete type so its own
// test can hand in a fake and never touch the network, per the migration
// plan's ban on network calls from tests.
type jiraClient interface {
	Issue(ctx context.Context, key string) (jirapkg.Issue, error)
	IssueDescription(ctx context.Context, key string) (string, error)
	Search(ctx context.Context, jql string, limit int) ([]jirapkg.Issue, error)
	Comments(ctx context.Context, key string, max int) ([]jirapkg.Comment, error)
	AddComment(ctx context.Context, key, bodyMarkdown string) (jirapkg.Comment, error)
	Create(ctx context.Context, n jirapkg.NewIssue) (jirapkg.Issue, error)
	Edit(ctx context.Context, key string, e jirapkg.Edit) error
	Transitions(ctx context.Context, key string) ([]jirapkg.Transition, error)
	Transition(ctx context.Context, key, statusName string) error
	AssignableUsers(ctx context.Context, issueKey, query string) ([]jirapkg.User, error)
}

// newClient resolves credentials and builds the real client. Kept as a
// function value on app rather than called directly from main so a
// subcommand's test never has to load a real credentials file - only
// dispatch (untested here) calls this.
func newClient() (jiraClient, error) {
	creds, err := jirapkg.LoadCredentials()
	if err != nil {
		return nil, err
	}
	return jirapkg.NewClient(creds), nil
}
