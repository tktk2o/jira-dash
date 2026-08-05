package jira

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient builds a Client whose base URL points at an httptest.Server
// instead of api.atlassian.com, with a fixed email/token so assertions on
// the Authorization header do not depend on LoadCredentials. HOME is
// redirected to a fresh t.TempDir() so that FieldIDs' on-disk cache
// (~/.cache/jira-dash) never touches this machine's real cache or leaks
// between tests that all share the zero-value CloudID.
func newTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return &Client{
		creds:   Credentials{Email: "a@example.com", APIToken: "tok"},
		http:    &http.Client{},
		baseURL: serverURL,
	}
}

func TestClientSendsBasicAuthAndAcceptsJSON(t *testing.T) {
	var gotAuth, gotAccept, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccept, gotPath = r.Header.Get("Authorization"), r.Header.Get("Accept"), r.URL.Path
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	var out struct{ OK bool }
	if err := c.do(context.Background(), http.MethodGet, "/myself", nil, &out); err != nil {
		t.Fatal(err)
	}

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("a@example.com:tok"))
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if !strings.HasSuffix(gotPath, "/rest/api/3/myself") {
		t.Errorf("path = %q", gotPath)
	}
	if !out.OK {
		t.Error("the body was not decoded")
	}
}

// 401 は「壊れた」ではなく「トークンが切れた」。区別できないと直し方が分からない。
func TestClientTurnsAnUnauthorizedIntoAnActionableError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errorMessages":["Client must be authenticated"]}`))
	}))
	defer srv.Close()

	err := newTestClient(t, srv.URL).do(context.Background(), http.MethodGet, "/myself", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "jira auth login") {
		t.Fatalf("want an actionable 401, got %v", err)
	}
}

// Jira の errorMessages / errors をそのまま見せる。素の 400 は理由を言わない。
func TestClientReportsJiraErrorMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errorMessages":[],"errors":{"summary":"is required"}}`))
	}))
	defer srv.Close()

	err := newTestClient(t, srv.URL).do(context.Background(), http.MethodGet, "/issue/X", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "summary") {
		t.Fatalf("the error should carry Jira's own words: %v", err)
	}
}

// トークンがエラーやログに出たら、それは事故。
func TestClientNeverPutsTheTokenInAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := newTestClient(t, srv.URL).do(context.Background(), http.MethodGet, "/x", nil, nil)
	if err == nil || strings.Contains(err.Error(), "tok") {
		t.Fatalf("the token leaked into %v", err)
	}
}

// A request body must be sent as JSON with the matching Content-Type -
// otherwise Jira treats it as an unparseable request regardless of what the
// caller intended.
func TestClientSendsRequestBodyAsJSON(t *testing.T) {
	var gotContentType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	type payload struct {
		Name string `json:"name"`
	}
	err := newTestClient(t, srv.URL).do(context.Background(), http.MethodPost, "/thing", payload{Name: "x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if !strings.Contains(gotBody, `"name":"x"`) {
		t.Errorf("body = %q, want the encoded payload", gotBody)
	}
}

// BaseURL and AgileURL both build on the same test override, so a test that
// hits the agile root does not silently fall back to the real Atlassian host.
func TestAgileURLUsesTheSameOverrideAsBaseURL(t *testing.T) {
	c := newTestClient(t, "http://example.invalid")
	if got, want := c.AgileURL(), "http://example.invalid/rest/agile/1.0"; got != want {
		t.Errorf("AgileURL() = %q, want %q", got, want)
	}
}
