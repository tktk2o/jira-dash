package jira

import (
	"context"
	"net/http"
	"net/url"
)

// User is one Jira account. JSON tags follow Jira's own REST field names
// (accountId, displayName, active) rather than this program's other structs'
// snake_case (see Issue.StoryPoints) - `jira users assignable -f json` is a
// new command with no prior output to stay compatible with, so it can match
// what a script talking to Jira's API directly would already expect. Email
// is spelled out rather than emailAddress because Jira's own key is absent
// on most accounts (privacy settings) and the shorter name reads better in
// a table column.
type User struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Active      bool   `json:"active"`
}

// rawUserDetail is one user object as GET /myself and GET
// /user/assignable/search both return it. A separate type from rawUser
// (issue.go's assignee/reporter shape) because those two endpoints carry
// only displayName - Jira does not send accountId or emailAddress on an
// issue's embedded assignee, and decoding both shapes into one struct would
// make "field absent from this response" and "field absent on this account"
// indistinguishable.
type rawUserDetail struct {
	AccountID    string `json:"accountId"`
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
	Active       bool   `json:"active"`
}

// toUser maps Jira's own field names onto User. emailAddress simply being
// absent from the response - most accounts hide it - unmarshals to the zero
// value already, so there is nothing here to special-case for that; it is
// only worth naming because the plan calls it out as a trap.
func (r rawUserDetail) toUser() User {
	return User{
		AccountID:   r.AccountID,
		DisplayName: r.DisplayName,
		Email:       r.EmailAddress,
		Active:      r.Active,
	}
}

// Myself identifies the credentials this Client is authenticated as - the
// account `jira auth status` reports and the one every write endpoint
// attributes its change to.
func (c *Client) Myself(ctx context.Context) (User, error) {
	var raw rawUserDetail
	if err := c.do(ctx, http.MethodGet, "/myself", nil, &raw); err != nil {
		return User{}, err
	}
	return raw.toUser(), nil
}

// AssignableUsers lists the accounts issueKey can be assigned to, optionally
// narrowed by query (a name/email substring Jira matches itself). This is
// the endpoint that replaces hand-copying an accountId into a config file to
// build a picker.
//
// An empty query is left off the URL rather than sent as "query=": Jira's
// own docs do not distinguish the two, but omitting it keeps the request
// from depending on undocumented behaviour for the common case of "no
// filter".
func (c *Client) AssignableUsers(ctx context.Context, issueKey, query string) ([]User, error) {
	q := url.Values{"issueKey": {issueKey}}
	if query != "" {
		q.Set("query", query)
	}
	var raws []rawUserDetail
	if err := c.do(ctx, http.MethodGet, "/user/assignable/search?"+q.Encode(), nil, &raws); err != nil {
		return nil, err
	}
	users := make([]User, 0, len(raws))
	for _, raw := range raws {
		users = append(users, raw.toUser())
	}
	return users, nil
}
