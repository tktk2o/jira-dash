package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Issue must map Jira's nested fields.* shape onto the flat Issue this
// program has carried since the TypeScript CLI - a wrong field path here
// (e.g. reading name off the wrong nested object) would silently blank a
// column in the TUI rather than error.
func TestClientIssueMapsJiraFieldsOntoIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/rest/api/3/issue/ABC-1" {
			t.Errorf("path = %q", got)
		}
		_, _ = w.Write([]byte(`{
			"key": "ABC-1",
			"fields": {
				"summary": "Fix the thing",
				"status": {"name": "In Progress"},
				"issuetype": {"name": "Bug"},
				"project": {"key": "ABC", "name": "Alphabet"},
				"assignee": {"displayName": "Ada"},
				"reporter": {"displayName": "Bea"},
				"priority": {"name": "High"},
				"updated": "2024-01-02T03:04:05.000+0900",
				"labels": ["urgent", "backend"],
				"description": null
			}
		}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).Issue(context.Background(), "ABC-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "ABC-1" || got.Summary != "Fix the thing" || got.Status != "In Progress" ||
		got.Type != "Bug" || got.Project.Key != "ABC" || got.Priority != "High" {
		t.Fatalf("mapped issue = %+v", got)
	}
	if got.Assignee == nil || *got.Assignee != "Ada" {
		t.Errorf("assignee = %v, want Ada", got.Assignee)
	}
	if got.Reporter == nil || *got.Reporter != "Bea" {
		t.Errorf("reporter = %v, want Bea", got.Reporter)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "urgent" {
		t.Errorf("labels = %v", got.Labels)
	}
}

// An issue with no assignee must decode to a nil pointer, not a pointer to
// an empty string - Issue.AssigneeName() only reads "-" for a nil pointer,
// so the wrong shape here would render a blank cell instead of a dash.
func TestClientIssueLeavesAssigneeNilWhenUnassigned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"key":"ABC-2","fields":{"summary":"x","assignee":null}}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).Issue(context.Background(), "ABC-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Assignee != nil {
		t.Errorf("assignee = %v, want nil", got.Assignee)
	}
}

// IssueDescription must run the ADF body through the same renderer adf_test.go
// exercises directly, so a paragraph in the raw response becomes Markdown
// rather than being handed to the caller as a JSON blob.
func TestClientIssueDescriptionRendersADFToMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"key":"ABC-3","fields":{"description":{"type":"doc","content":[
			{"type":"paragraph","content":[{"type":"text","text":"hello","marks":[{"type":"strong"}]}]}
		]}}}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).IssueDescription(context.Background(), "ABC-3")
	if err != nil {
		t.Fatal(err)
	}
	if got != "**hello**" {
		t.Errorf("got %q, want %q", got, "**hello**")
	}
}

// Search must send an explicit fields list - /search/jql returns bare keys
// with no error if fields is omitted, which would look like every issue
// lost its summary and status rather than like a request bug.
func TestClientSearchSendsAnExplicitFieldsList(t *testing.T) {
	var gotFields []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body searchJQLRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotFields = body.Fields
		_, _ = w.Write([]byte(`{"issues":[{"key":"A-1","fields":{"summary":"s"}}]}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL).Search(context.Background(), "project = A", 10); err != nil {
		t.Fatal(err)
	}
	if len(gotFields) == 0 {
		t.Fatal("fields was empty - /search/jql would have returned keys only")
	}
}

// Search must follow nextPageToken to a second page, and must stop once
// limit issues have been collected even if the server still offers a token -
// both halves of the pagination contract the plan calls out as the risky part.
func TestClientSearchFollowsPaginationAndStopsAtLimit(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body searchJQLRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.NextPageToken == "" {
			_, _ = w.Write([]byte(`{
				"issues": [{"key":"A-1","fields":{"summary":"one"}}, {"key":"A-2","fields":{"summary":"two"}}],
				"nextPageToken": "page-2"
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"issues": [{"key":"A-3","fields":{"summary":"three"}}, {"key":"A-4","fields":{"summary":"four"}}]
		}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).Search(context.Background(), "project = A", 3)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 (the second page must be fetched)", requests)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3 (limit must stop the loop mid-page)", len(got))
	}
	if got[0].Key != "A-1" || got[2].Key != "A-3" {
		t.Errorf("got = %+v, want page order preserved", got)
	}
}

// A search with no nextPageToken at all in the first response must stop
// after one request - a client that always sends a second request when the
// first page fills the whole limit would waste a call on every small search.
func TestClientSearchStopsWhenNoTokenIsOffered(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"issues":[{"key":"A-1","fields":{"summary":"one"}}]}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).Search(context.Background(), "project = A", 5)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
}
