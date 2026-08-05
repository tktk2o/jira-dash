package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Myself must map Jira's accountId/displayName/emailAddress/active onto
// User - this is the identity `jira auth status` reports, so a wrong field
// path here would silently show the wrong account as logged in.
func TestClientMyselfMapsJiraFieldsOntoUser(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"accountId":"acc-1","displayName":"Ada","emailAddress":"ada@example.com","active":true}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).Myself(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotPath, "/rest/api/3/myself") {
		t.Errorf("path = %q", gotPath)
	}
	want := User{AccountID: "acc-1", DisplayName: "Ada", Email: "ada@example.com", Active: true}
	if got != want {
		t.Errorf("got = %+v, want %+v", got, want)
	}
}

// emailAddress is absent from most accounts (privacy settings), and that
// absence must decode to an empty Email, not an error - a missing key in a
// JSON object is not malformed input.
func TestClientMyselfLeavesEmailEmptyWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"accountId":"acc-2","displayName":"Bea","active":true}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).Myself(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "" {
		t.Errorf("Email = %q, want empty", got.Email)
	}
}

// AssignableUsers must send issueKey and query as query parameters and map
// the returned array element-by-element onto User.
func TestClientAssignableUsersSendsIssueKeyAndQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[{"accountId":"acc-3","displayName":"Cid","emailAddress":"cid@example.com","active":true}]`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).AssignableUsers(context.Background(), "ABC-1", "cid")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQuery, "issueKey=ABC-1") || !strings.Contains(gotQuery, "query=cid") {
		t.Errorf("query = %q, want both issueKey and query", gotQuery)
	}
	if len(got) != 1 || got[0].AccountID != "acc-3" || got[0].Email != "cid@example.com" {
		t.Errorf("got = %+v", got)
	}
}

// An empty query must be left off the URL entirely, not sent as "query=" -
// the plan calls this out explicitly, and Jira's own behaviour for an empty
// filter parameter is not documented either way.
func TestClientAssignableUsersOmitsAnEmptyQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL).AssignableUsers(context.Background(), "ABC-1", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotQuery, "query") {
		t.Errorf("query = %q, want no query parameter at all", gotQuery)
	}
	if !strings.Contains(gotQuery, "issueKey=ABC-1") {
		t.Errorf("query = %q, want issueKey", gotQuery)
	}
}
