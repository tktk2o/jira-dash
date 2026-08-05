package main

import (
	"context"
	"errors"

	jirapkg "jira-dash/internal/jira"
)

// fakeClient is a jiraClient that never touches the network - every method
// records that it was called and returns canned data, which is what lets
// --dry-run's "nothing was sent" guarantee be tested at all (per the
// migration plan's ban on tests touching the network).
type fakeClient struct {
	issue           jirapkg.Issue
	issueErr        error
	description     string
	descriptionErr  error
	searchResult    []jirapkg.Issue
	searchErr       error
	comments        []jirapkg.Comment
	commentsErr     error
	addedComment    jirapkg.Comment
	addCommentErr   error
	createdIssue    jirapkg.Issue
	createErr       error
	editErr         error
	transitions     []jirapkg.Transition
	transitionsErr  error
	transitionErr   error
	assignableUsers []jirapkg.User
	assignableErr   error

	// calls records every method invoked, in order, so a test can assert
	// none of the mutating ones ran (--dry-run) or exactly one did.
	calls []string

	lastSearchJQL     string
	lastSearchLimit   int
	lastAddCommentKey string
	lastAddCommentMD  string
	lastCreateInput   jirapkg.NewIssue
	lastEditKey       string
	lastEditInput     jirapkg.Edit
	lastTransitionTo  string
}

func (f *fakeClient) IssueWithDescription(
	ctx context.Context, key string,
) (jirapkg.Issue, string, error) {
	f.calls = append(f.calls, "IssueWithDescription")
	if f.issueErr != nil {
		return jirapkg.Issue{}, "", f.issueErr
	}
	return f.issue, f.description, f.descriptionErr
}

func (f *fakeClient) Search(ctx context.Context, jql string, limit int) ([]jirapkg.Issue, error) {
	f.calls = append(f.calls, "Search")
	f.lastSearchJQL, f.lastSearchLimit = jql, limit
	return f.searchResult, f.searchErr
}

func (f *fakeClient) Comments(ctx context.Context, key string, max int) ([]jirapkg.Comment, error) {
	f.calls = append(f.calls, "Comments")
	return f.comments, f.commentsErr
}

func (f *fakeClient) AddComment(ctx context.Context, key, bodyMarkdown string) (jirapkg.Comment, error) {
	f.calls = append(f.calls, "AddComment")
	f.lastAddCommentKey, f.lastAddCommentMD = key, bodyMarkdown
	return f.addedComment, f.addCommentErr
}

func (f *fakeClient) Create(ctx context.Context, n jirapkg.NewIssue) (jirapkg.Issue, error) {
	f.calls = append(f.calls, "Create")
	f.lastCreateInput = n
	return f.createdIssue, f.createErr
}

func (f *fakeClient) Edit(ctx context.Context, key string, e jirapkg.Edit) error {
	f.calls = append(f.calls, "Edit")
	f.lastEditKey, f.lastEditInput = key, e
	return f.editErr
}

func (f *fakeClient) Transitions(ctx context.Context, key string) ([]jirapkg.Transition, error) {
	f.calls = append(f.calls, "Transitions")
	return f.transitions, f.transitionsErr
}

func (f *fakeClient) Transition(ctx context.Context, key, statusName string) error {
	f.calls = append(f.calls, "Transition")
	f.lastTransitionTo = statusName
	return f.transitionErr
}

func (f *fakeClient) AssignableUsers(ctx context.Context, issueKey, query string) ([]jirapkg.User, error) {
	f.calls = append(f.calls, "AssignableUsers")
	return f.assignableUsers, f.assignableErr
}

// errNotConfigured is a sentinel a test can assign to make a call fail
// loudly instead of returning a zero value that might pass by accident.
var errNotConfigured = errors.New("fakeClient: this call was not configured for this test")
