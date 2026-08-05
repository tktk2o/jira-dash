package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fieldsTestClient builds a Client pointed at srv, with a distinct cloud id
// per test so the on-disk cache from one test can never be mistaken for
// another's.
func fieldsTestClient(t *testing.T, srv *httptest.Server, cloudID string) *Client {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return &Client{
		creds:   Credentials{Email: "a@example.com", APIToken: "tok", CloudID: cloudID},
		http:    &http.Client{},
		baseURL: srv.URL,
	}
}

// The response shape GET /field actually returns, per Atlassian's docs and
// the migration plan: an array, not an object keyed by id.
const fieldResponseBody = `[
	{"id":"customfield_10020","name":"Sprint","untranslatedName":"Sprint"},
	{"id":"customfield_10016","name":"Story Points","untranslatedName":"Story Points"},
	{"id":"customfield_10099","name":"Team","untranslatedName":"Team"},
	{"id":"customfield_10015","name":"Start date","untranslatedName":"Start date"},
	{"id":"customfield_10071","name":"Flagged","untranslatedName":"Flagged"},
	{"id":"summary","name":"Summary","untranslatedName":"Summary"}
]`

// The six ids resolve by name, and a field this fixture never configured
// ("Target start" is deliberately absent) comes back empty rather than
// failing the whole call - matters because a site missing one custom field
// must not break everyone else's request.
func TestFieldIDsResolvesByNameAndLeavesAMissingFieldEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/field" {
			t.Errorf("path = %q, want /rest/api/3/field", r.URL.Path)
		}
		_, _ = w.Write([]byte(fieldResponseBody))
	}))
	defer srv.Close()
	c := fieldsTestClient(t, srv, "cloud-a")

	got, err := c.FieldIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := FieldIDs{
		Sprint:      "customfield_10020",
		StoryPoints: "customfield_10016",
		Team:        "customfield_10099",
		StartDate:   "customfield_10015",
		Flagged:     "customfield_10071",
		// TargetStart intentionally absent from the fixture.
	}
	if got != want {
		t.Errorf("FieldIDs() = %+v, want %+v", got, want)
	}
}

// Matching falls back to name when untranslatedName is absent, and ignores
// case - a viewer's locale can translate name but untranslatedName is
// documented to survive it, and Jira's own casing is not guaranteed stable.
func TestFieldIDsFallsBackToNameAndIgnoresCase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":"customfield_20099","name":"team"}]`))
	}))
	defer srv.Close()
	c := fieldsTestClient(t, srv, "cloud-b")

	got, err := c.FieldIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Team != "customfield_20099" {
		t.Errorf("Team = %q, want customfield_20099", got.Team)
	}
}

// The second call must not hit the network at all: the whole point of the
// cache is to spend the /field round trip once per site, not once per call.
func TestFieldIDsIsServedFromCacheOnTheSecondCall(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(fieldResponseBody))
	}))
	defer srv.Close()
	c := fieldsTestClient(t, srv, "cloud-c")

	first, err := c.FieldIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.FieldIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if hits != 1 {
		t.Fatalf("handler was hit %d times, want 1 (second call should be served from cache)", hits)
	}
	if first != second {
		t.Errorf("cached result %+v differs from the fresh one %+v", second, first)
	}
}

// Two sites' caches must never collide: a machine that holds credentials for
// more than one Jira cloud (a personal + a work site, say) would otherwise
// serve one site's field ids to the other.
func TestFieldIDsCachesSeparatelyPerCloudID(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(fieldResponseBody))
	}))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	clientFor := func(cloudID string) *Client {
		return &Client{
			creds:   Credentials{Email: "a@example.com", APIToken: "tok", CloudID: cloudID},
			http:    &http.Client{},
			baseURL: srv.URL,
		}
	}

	if _, err := clientFor("cloud-x").FieldIDs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := clientFor("cloud-y").FieldIDs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2 (each cloud id must fetch on its own first call)", hits)
	}
}

// A corrupt cache file is pure derived data: it must cost one re-fetch, not
// an error surfaced to the user who never touched that file themselves.
func TestFieldIDsSilentlyRefetchesWhenTheCacheFileIsCorrupt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(fieldResponseBody))
	}))
	defer srv.Close()
	c := fieldsTestClient(t, srv, "cloud-corrupt")

	path := fieldsCachePath("cloud-corrupt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := c.FieldIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Sprint != "customfield_10020" {
		t.Errorf("Sprint = %q, want the freshly fetched value", got.Sprint)
	}
}
