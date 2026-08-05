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

	// extra holds every field Jira sent, keyed by its raw JSON name. The
	// sprint field's key is a customfield_NNNNN id that differs per site
	// (see fields.go), so it cannot get a struct tag of its own; this is how
	// toIssue reaches it after FieldIDs has resolved which key that is.
	extra map[string]json.RawMessage
}

// UnmarshalJSON decodes the named fields above exactly as a plain struct tag
// would, and additionally keeps the whole object around in extra. A second
// json.Unmarshal pass into a map might look wasteful, but /issue and
// /search/jql responses are a few KB - the alternative, a hand-written
// decoder for a customfield_NNNNN key nobody can name in this source, is not
// worth it.
func (r *rawFields) UnmarshalJSON(b []byte) error {
	// A distinct named type: aliasing rawFields directly would recurse into
	// this same method forever.
	type plain rawFields
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*r = rawFields(p)
	return json.Unmarshal(b, &r.extra)
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
//
// fieldIDs.Sprint names which customfield_NNNNN key in r.Fields.extra holds
// the sprint field, since that id is resolved per site rather than known at
// compile time (see fields.go). An empty Sprint id - a site with no sprint
// field configured - leaves Issue.Sprint nil, which CurrentSprint and
// InActiveSprintPrefix already treat as "not in a sprint".
//
// siteURL fills Issue.URL, a plain browse link (not a secret - the old CLI
// printed the same one). Nothing filled it in until T11's diff against the
// old CLI showed every issue coming back with url:"": Task 10 removed the
// shelled-out `jira` process, which had built this link itself, and nothing
// took over the job on this path. An empty siteURL (any test, which never
// sets Credentials.SiteURL) leaves Issue.URL empty rather than a malformed
// "/browse/KEY".
func (r rawIssue) toIssue(fieldIDs FieldIDs, siteURL string) Issue {
	var issue Issue
	issue.Key = r.Key
	if siteURL != "" {
		// TrimSuffix because the credentials file is hand-editable and a site URL
		// written with a trailing slash would otherwise produce "…//browse/KEY",
		// which some browsers follow and some do not.
		issue.URL = strings.TrimSuffix(siteURL, "/") + "/browse/" + r.Key
	}
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
	if fieldIDs.Sprint != "" {
		if raw, ok := r.Fields.extra[fieldIDs.Sprint]; ok {
			// A malformed sprint value should not fail the whole issue -
			// the rest of Issue decoded fine, and a blank sprint column
			// beats losing the row entirely.
			_ = json.Unmarshal(raw, &issue.Sprint)
		}
	}
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

// requestedFields is what one /issue or /search/jql call must ask for: the
// fixed set every response needs, plus the sprint custom field when this
// site has one. Built fresh per call rather than once, because fieldIDs
// comes from a per-client cache that a test (or a re-login) can change
// between calls.
func requestedFields(fieldIDs FieldIDs) []string {
	fields := append([]string(nil), issueFields...)
	if fieldIDs.Sprint != "" {
		fields = append(fields, fieldIDs.Sprint)
	}
	return fields
}

// Issue fetches one issue by key. The description comes back rendered to
// Markdown (see IssueDescription) rather than as a second field on Issue,
// since Issue's JSON shape is the one the TUI and `-f json` both already
// depend on and this task does not touch it.
func (c *Client) Issue(ctx context.Context, key string) (Issue, error) {
	issue, _, err := c.IssueWithDescription(ctx, key)
	return issue, err
}

// IssueWithDescription fetches the fields and the description in ONE request.
// The two used to be separate calls, and `jira get` made both: measured at
// 2.2s against the old CLI's 0.8s for the same issue, because a second round
// trip costs more than the whole TypeScript startup this migration removed.
// Anything that wants both must come through here.
func (c *Client) IssueWithDescription(ctx context.Context, key string) (Issue, string, error) {
	fieldIDs, err := c.FieldIDs(ctx)
	if err != nil {
		return Issue{}, "", err
	}
	q := "?fields=" + strings.Join(append(requestedFields(fieldIDs), "description"), ",")
	var raw rawIssue
	if err := c.do(ctx, http.MethodGet, "/issue/"+key+q, nil, &raw); err != nil {
		return Issue{}, "", err
	}
	if raw.Key == "" {
		raw.Key = key
	}
	description, err := raw.issueDescriptionMarkdown()
	if err != nil {
		return Issue{}, "", err
	}
	return raw.toIssue(fieldIDs, c.creds.SiteURL), description, nil
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
	fieldIDs, err := c.FieldIDs(ctx)
	if err != nil {
		return nil, err
	}
	fields := requestedFields(fieldIDs)

	var issues []Issue
	pageToken := ""
	for {
		pageSize := limit - len(issues)
		if pageSize <= 0 {
			break
		}
		req := searchJQLRequest{
			JQL:           jql,
			Fields:        fields,
			MaxResults:    pageSize,
			NextPageToken: pageToken,
		}
		var resp searchJQLResponse
		if err := c.do(ctx, http.MethodPost, "/search/jql", req, &resp); err != nil {
			return nil, err
		}
		for _, raw := range resp.Issues {
			issues = append(issues, raw.toIssue(fieldIDs, c.creds.SiteURL))
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
