package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// JQLSuggestions must send both fieldName and fieldValue as query
// parameters and strip Jira's own <b> highlight markers from displayName -
// a CLI has no bold to render them into, and leaving them in would corrupt
// the table column and confuse -f json consumers alike.
func TestClientJQLSuggestionsStripsHighlightTagsFromDisplayName(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{"results":[
			{"value":"PROJ-1","displayName":"<b>Proj</b>ect One"},
			{"value":"PROJ-2","displayName":"Project Two"}
		]}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).JQLSuggestions(context.Background(), "project", "proj")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery.Get("fieldName") != "project" || gotQuery.Get("fieldValue") != "proj" {
		t.Errorf("query = %v", gotQuery)
	}
	want := []Suggestion{
		{Value: "PROJ-1", DisplayName: "Project One"},
		{Value: "PROJ-2", DisplayName: "Project Two"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got = %+v, want %+v", got, want)
	}
}

// An empty fieldValue is a valid request (Jira returns the field's most
// common values), and must still hit the endpoint rather than short-circuit
// locally.
func TestClientJQLSuggestionsAllowsEmptyFieldValue(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).JQLSuggestions(context.Background(), "status", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got = %+v, want empty", got)
	}
	if !strings.Contains(gotPath, "fieldValue=") {
		t.Errorf("path = %q, want fieldValue param present", gotPath)
	}
}
