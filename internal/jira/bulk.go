package jira

import (
	"context"
	"net/http"
)

// bulkFetchMaxKeys caps one /issue/bulkfetch request. Atlassian's own
// documented limit sits in this territory; sending more than this in one
// call is asking for a request Jira would reject anyway, so BulkIssues
// trims to the first bulkFetchMaxKeys rather than let the caller find that
// out from an HTTP error.
const bulkFetchMaxKeys = 100

// bulkFetchRequest is the body POST /issue/bulkfetch requires. Description
// is the only field this program's prefetch use case needs - the TUI wants
// to warm the preview pane's cache before the user has picked an issue, not
// the full field set /search/jql already carries.
type bulkFetchRequest struct {
	IssueIdsOrKeys []string `json:"issueIdsOrKeys"`
	Fields         []string `json:"fields"`
}

// bulkFetchResponse is /issue/bulkfetch's own shape: the same per-issue
// object /search/jql returns, plus a list of keys Jira could not resolve
// (deleted, or the caller has no permission). issueErrors is decoded only
// so nothing chokes on its presence - BulkIssues ignores it deliberately,
// per its own doc comment.
type bulkFetchResponse struct {
	Issues      []rawIssue `json:"issues"`
	IssueErrors []any      `json:"issueErrors"`
}

// BulkIssues prefetches descriptions for many issues in one round trip,
// rendered from ADF to Markdown exactly as IssueWithDescription's single-
// issue path does. Built for the TUI's picker: warming every visible row's
// preview cache with N separate GETs would be N round trips for the same
// data one POST can carry.
//
// A key Jira could not resolve (deleted, no permission, a typo) is simply
// missing from the returned map rather than an error - one bad key must
// never fail every other key's prefetch, which is the whole point of doing
// this as a batch instead of one call per issue.
//
// keys longer than bulkFetchMaxKeys is trimmed to its first
// bulkFetchMaxKeys entries; see that constant's own doc comment.
func (c *Client) BulkIssues(ctx context.Context, keys []string) (map[string]string, error) {
	if len(keys) > bulkFetchMaxKeys {
		keys = keys[:bulkFetchMaxKeys]
	}
	if len(keys) == 0 {
		return map[string]string{}, nil
	}

	req := bulkFetchRequest{IssueIdsOrKeys: keys, Fields: []string{"description"}}
	var resp bulkFetchResponse
	if err := c.do(ctx, http.MethodPost, "/issue/bulkfetch", req, &resp); err != nil {
		return nil, err
	}

	descriptions := make(map[string]string, len(resp.Issues))
	for _, raw := range resp.Issues {
		md, err := raw.issueDescriptionMarkdown()
		if err != nil {
			// A single issue's malformed ADF should not fail the whole
			// batch, same reasoning as an unresolved key above: skip it,
			// keep the rest.
			continue
		}
		descriptions[raw.Key] = md
	}
	return descriptions, nil
}
