package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BulkIssues must map each returned issue's key to its rendered
// description - this is the batch prefetch a picker warms its cache with,
// so a wrong key would silently show one issue's description under
// another's.
func TestClientBulkIssuesMapsEachKeyToItsDescription(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"issues":[
			{"key":"ABC-1","fields":{"description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]}]}}},
			{"key":"ABC-2","fields":{"description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}}}
		]}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).BulkIssues(context.Background(), []string{"ABC-1", "ABC-2"})
	if err != nil {
		t.Fatal(err)
	}
	if got["ABC-1"] != "one" || got["ABC-2"] != "two" {
		t.Errorf("got = %+v", got)
	}
	if !strings.Contains(gotBody, `"issueIdsOrKeys":["ABC-1","ABC-2"]`) {
		t.Errorf("request body = %s", gotBody)
	}
}

// A key Jira could not resolve must never fail the whole batch - one bad
// issue among many is the normal case this endpoint exists for, not an
// error condition.
func TestClientBulkIssuesReturnsRestWhenOneKeyIsMissingFromResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"issues":[
			{"key":"ABC-1","fields":{"description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]}]}}}
		],"issueErrors":[{"issueIdOrKey":"ABC-2"}]}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).BulkIssues(context.Background(), []string{"ABC-1", "ABC-2"})
	if err != nil {
		t.Fatal(err)
	}
	if got["ABC-1"] != "one" {
		t.Errorf("got = %+v", got)
	}
	if _, ok := got["ABC-2"]; ok {
		t.Errorf("ABC-2 should be absent, got %+v", got)
	}
}

// A batch larger than the API's own limit must be trimmed, not sent whole
// and left to fail on the server side.
func TestClientBulkIssuesCapsBatchAtHundredKeys(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"issues":[]}`))
	}))
	defer srv.Close()

	keys := make([]string, 150)
	for i := range keys {
		keys[i] = "ABC-" + string(rune('0'+i%10))
	}
	if _, err := newTestClient(t, srv.URL).BulkIssues(context.Background(), keys); err != nil {
		t.Fatal(err)
	}
	if strings.Count(gotBody, "ABC-") != 100 {
		t.Errorf("request sent %d keys, want 100: %s", strings.Count(gotBody, "ABC-"), gotBody)
	}
}
