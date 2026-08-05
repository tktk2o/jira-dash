package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// rawFields is the subset of `fields.*` this program reads off /issue/{key}
// and /search/jql. Every field is optional in Jira's own schema (an
// assignee, a priority scheme, a parent can all be absent), so everything
// here is a pointer or a slice rather than a bare struct - a bare struct
// would decode a missing object as its zero value, which for e.g. Status
// looks identical to "the status really is empty".
type rawFields struct {
	Summary     string          `json:"summary"`
	Status      *rawNamed       `json:"status"`
	IssueType   *rawNamed       `json:"issuetype"`
	Project     *rawProject     `json:"project"`
	Assignee    *rawUser        `json:"assignee"`
	Reporter    *rawUser        `json:"reporter"`
	Priority    *rawNamed       `json:"priority"`
	Updated     JiraTime        `json:"updated"`
	Parent      *rawIssueRef    `json:"parent"`
	Labels      []string        `json:"labels"`
	Description json.RawMessage `json:"description"`
}

type rawNamed struct {
	Name string `json:"name"`
}

type rawProject struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type rawUser struct {
	DisplayName string `json:"displayName"`
}

type rawIssueRef struct {
	Key string `json:"key"`
}

// rawIssue is one element of /search/jql's `issues` array, and the whole
// body of /issue/{key}.
type rawIssue struct {
	Key    string    `json:"key"`
	Fields rawFields `json:"fields"`
}

// toIssue maps Jira's own response shape onto the Issue this program has
// carried since the TypeScript CLI: same JSON tags, so the TUI's parser and
// `-f json` need not change to read a response built here instead of shelled
// out to `jira get`.
func (r rawIssue) toIssue() Issue {
	var issue Issue
	issue.Key = r.Key
	issue.Summary = r.Fields.Summary
	if r.Fields.Status != nil {
		issue.Status = r.Fields.Status.Name
	}
	if r.Fields.IssueType != nil {
		issue.Type = r.Fields.IssueType.Name
	}
	if r.Fields.Project != nil {
		issue.Project.Key = r.Fields.Project.Key
	}
	if r.Fields.Assignee != nil {
		name := r.Fields.Assignee.DisplayName
		issue.Assignee = &name
	}
	if r.Fields.Reporter != nil {
		name := r.Fields.Reporter.DisplayName
		issue.Reporter = &name
	}
	if r.Fields.Priority != nil {
		issue.Priority = r.Fields.Priority.Name
	}
	issue.Updated = r.Fields.Updated
	issue.Labels = r.Fields.Labels
	return issue
}

// issueDescriptionMarkdown renders one issue's ADF description to Markdown,
// the same lossy conversion `jira get`'s Markdown output performed. Kept
// separate from toIssue because Issue itself carries no description field -
// the TUI's preview pane reads it through a dedicated call, mirroring the
// old CLI's `jira get -f json` -> `.description`.
func (r rawIssue) issueDescriptionMarkdown() (string, error) {
	return renderADFToMarkdown(r.Fields.Description)
}

// issueFields is what /issue/{key} and /search/jql must both request - Jira
// returns nothing beyond `key` unless a field is named explicitly, on either
// endpoint. Kept as one list so an endpoint that gains a mapped field cannot
// forget to ask for it.
var issueFields = []string{
	"summary", "status", "issuetype", "project", "assignee", "reporter",
	"priority", "updated", "parent", "labels", "description",
}

// Issue fetches one issue by key. The description comes back rendered to
// Markdown (see IssueDescription) rather than as a second field on Issue,
// since Issue's JSON shape is the one the TUI and `-f json` both already
// depend on and this task does not touch it.
func (c *Client) Issue(ctx context.Context, key string) (Issue, error) {
	q := "?fields=" + strings.Join(issueFields, ",")
	var raw rawIssue
	if err := c.do(ctx, http.MethodGet, "/issue/"+key+q, nil, &raw); err != nil {
		return Issue{}, err
	}
	if raw.Key == "" {
		raw.Key = key
	}
	return raw.toIssue(), nil
}

// IssueDescription fetches one issue and returns only its description,
// rendered from ADF to Markdown. This is the endpoint the TUI's preview pane
// calls - it wants the body, not the fields the search results already
// carry (see the old CLI's own note on jira.go's Issue method).
func (c *Client) IssueDescription(ctx context.Context, key string) (string, error) {
	q := "?fields=description"
	var raw rawIssue
	if err := c.do(ctx, http.MethodGet, "/issue/"+key+q, nil, &raw); err != nil {
		return "", err
	}
	return raw.issueDescriptionMarkdown()
}

// searchJQLRequest is the body /search/jql requires. Unlike the deprecated
// /search endpoint, fields is mandatory here - an empty list returns keys
// only, silently, with no error to say so.
type searchJQLRequest struct {
	JQL           string   `json:"jql"`
	Fields        []string `json:"fields"`
	MaxResults    int      `json:"maxResults"`
	NextPageToken string   `json:"nextPageToken,omitempty"`
}

type searchJQLResponse struct {
	Issues        []rawIssue `json:"issues"`
	NextPageToken string     `json:"nextPageToken"`
}

// Search runs jql against /search/jql, the token-paginated endpoint that
// replaced the deprecated /search. It pages until limit issues have been
// collected or the token runs out, whichever comes first.
func (c *Client) Search(ctx context.Context, jql string, limit int) ([]Issue, error) {
	var issues []Issue
	pageToken := ""
	for {
		pageSize := limit - len(issues)
		if pageSize <= 0 {
			break
		}
		req := searchJQLRequest{
			JQL:           jql,
			Fields:        issueFields,
			MaxResults:    pageSize,
			NextPageToken: pageToken,
		}
		var resp searchJQLResponse
		if err := c.do(ctx, http.MethodPost, "/search/jql", req, &resp); err != nil {
			return nil, err
		}
		for _, raw := range resp.Issues {
			issues = append(issues, raw.toIssue())
			if len(issues) == limit {
				return issues, nil
			}
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return issues, nil
}
