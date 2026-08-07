package jira

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Suggestion is one value/label pair from Jira's JQL autocomplete, e.g. a
// project key paired with its human-readable name - the raw Value is what
// gets spliced into a JQL clause, DisplayName is what a person picking from
// a list would want to read.
type Suggestion struct {
	Value       string `json:"value"`
	DisplayName string `json:"displayName"`
}

// rawSuggestion is one element of /jql/autocompletedata/suggestions'
// `results` array. DisplayName arrives wrapped in Jira's own <b>...</b>
// highlight tags around the substring that matched fieldValue; Suggestion's
// DisplayName strips them, since a CLI has no bold to render them into.
type rawSuggestion struct {
	Value       string `json:"value"`
	DisplayName string `json:"displayName"`
}

func (r rawSuggestion) toSuggestion() Suggestion {
	return Suggestion{
		Value:       r.Value,
		DisplayName: stripHighlightTags(r.DisplayName),
	}
}

// stripHighlightTags removes Jira's <b>/</b> highlight markers from an
// autocomplete displayName. A plain strings.ReplaceAll rather than a regexp
// or HTML parser - these are the only two tags this endpoint ever emits,
// always as a matched pair around a substring, never nested or attributed.
func stripHighlightTags(s string) string {
	s = strings.ReplaceAll(s, "<b>", "")
	s = strings.ReplaceAll(s, "</b>", "")
	return s
}

type jqlSuggestionsResponse struct {
	Results []rawSuggestion `json:"results"`
}

// JQLSuggestions asks Jira's own JQL autocomplete for values fieldName can
// take, narrowed by the partial fieldValue a user has typed so far - the
// same data the web UI's JQL editor autocompletes from. An empty fieldValue
// is valid and returns the field's most common values, so it is included in
// the query string, unlike AssignableUsers' optional query.
func (c *Client) JQLSuggestions(ctx context.Context, fieldName, fieldValue string) ([]Suggestion, error) {
	q := url.Values{"fieldName": {fieldName}, "fieldValue": {fieldValue}}
	var resp jqlSuggestionsResponse
	if err := c.do(ctx, http.MethodGet, "/jql/autocompletedata/suggestions?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	suggestions := make([]Suggestion, 0, len(resp.Results))
	for _, raw := range resp.Results {
		suggestions = append(suggestions, raw.toSuggestion())
	}
	return suggestions, nil
}
