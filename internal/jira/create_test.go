package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fieldServer wires up the GET /field response newTestClient's Client needs
// before Create can resolve a customfield id, plus a caller-supplied issue
// handler. Kept as one helper because every Create test needs the fields
// endpoint but differs only in what /issue and /issue/{key} return.
func fieldServer(t *testing.T, sprintFieldName string, issueHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/field", func(w http.ResponseWriter, _ *http.Request) {
		fields := []rawField{{ID: "customfield_10001", Name: "Story Points", UntranslatedName: "Story Points"}}
		if sprintFieldName != "" {
			fields = append(fields, rawField{ID: "customfield_10099", Name: sprintFieldName, UntranslatedName: sprintFieldName})
		}
		_ = json.NewEncoder(w).Encode(fields)
	})
	mux.Handle("/rest/api/3/issue/", issueHandler)
	mux.HandleFunc("/rest/api/3/issue", issueHandler)
	return httptest.NewServer(mux)
}

// Create must send the project key, issue type, and summary Jira's own
// schema requires, and must never key the sprint under a literal
// customfield_NNNNN - that id is site-specific and this program does not
// know the test site's id until FieldIDs resolves it.
func TestCreateSendsTheResolvedSprintFieldID(t *testing.T) {
	var postedBody map[string]json.RawMessage
	srv := fieldServer(t, "Sprint", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var req createRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			raw, _ := json.Marshal(req.Fields)
			_ = json.Unmarshal(raw, &postedBody)
			_, _ = w.Write([]byte(`{"key":"PROJ-9"}`))
		default:
			_, _ = w.Write([]byte(`{"key":"PROJ-9","fields":{"summary":"s"}}`))
		}
	})
	defer srv.Close()

	// The agile side lives on the same test server: a second mux registers
	// /rest/agile/1.0 paths so ActiveSprint's board/sprint lookups resolve
	// without a second httptest.Server.
	mux := srv.Config.Handler.(*http.ServeMux)
	mux.HandleFunc("/rest/agile/1.0/board", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[{"id":1}]}`))
	})
	mux.HandleFunc("/rest/agile/1.0/board/1/sprint", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[{"id":42,"name":"Team 0803-0807","state":"active"}]}`))
	})

	c := newTestClient(t, srv.URL)
	_, err := c.Create(context.Background(), NewIssue{
		ProjectKey: "PROJ", Type: "Task", Summary: "s", Sprint: "Team",
	})
	if err != nil {
		t.Fatal(err)
	}

	if postedBody["project"] == nil || postedBody["issuetype"] == nil || postedBody["summary"] == nil {
		t.Fatalf("posted body missing required fields: %v", postedBody)
	}
	sprintRaw, ok := postedBody["customfield_10099"]
	if !ok {
		t.Fatalf("posted body did not carry the resolved sprint field id: %v", postedBody)
	}
	if string(sprintRaw) != "42" {
		t.Errorf("sprint field value = %s, want the sprint id 42", sprintRaw)
	}
	for key := range postedBody {
		if strings.Contains(key, "customfield") && key != "customfield_10099" {
			t.Errorf("unexpected customfield key in posted body: %s", key)
		}
	}
}

// Requesting a sprint on a site with no Sprint field must fail loudly - a
// silent drop would file the issue into the backlog and look like Jira, not
// this program, misplaced it.
func TestCreateErrorsWhenSprintRequestedButSiteHasNoSprintField(t *testing.T) {
	srv := fieldServer(t, "", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"key":"PROJ-1"}`))
	})
	mux := srv.Config.Handler.(*http.ServeMux)
	mux.HandleFunc("/rest/agile/1.0/board", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[{"id":1}]}`))
	})
	mux.HandleFunc("/rest/agile/1.0/board/1/sprint", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[{"id":42,"name":"Team 0803-0807","state":"active"}]}`))
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.Create(context.Background(), NewIssue{ProjectKey: "PROJ", Type: "Task", Summary: "s", Sprint: "Team"})
	if err == nil || !strings.Contains(err.Error(), "Sprint") {
		t.Fatalf("want an error naming the missing Sprint field, got %v", err)
	}
}

// ActiveSprint must prefer an active sprint over a future one, matching
// Issue.CurrentSprint's own rule (issue.go) - the two must agree, or an
// issue created here could land somewhere `jira search` would not show it as
// current.
func TestActiveSprintPrefersActiveOverFuture(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/agile/1.0/board", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[{"id":7}]}`))
	})
	mux.HandleFunc("/rest/agile/1.0/board/7/sprint", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[
			{"id":1,"name":"Team 0713-0717","state":"closed"},
			{"id":2,"name":"Team 0720-0724","state":"future"},
			{"id":3,"name":"Team 0803-0807","state":"active"}
		]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.ActiveSprint(context.Background(), "PROJ", "Team")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 3 || got.State != "active" {
		t.Errorf("got = %+v, want the active sprint (id 3)", got)
	}
}

func TestActiveSprintAcceptsANumericSprintID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/board"):
			_, _ = w.Write([]byte(`{"values":[{"id":1}]}`))
		case strings.HasSuffix(r.URL.Path, "/sprint"):
			_, _ = w.Write([]byte(`{"values":[{"id":42,"name":"Unrelated name","state":"active"}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	got, err := newTestClient(t, srv.URL).ActiveSprint(context.Background(), "PROJ", "42")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 42 {
		t.Fatalf("sprint ID = %d, want 42", got.ID)
	}
}

// A closed sprint sharing the prefix must never win, even with no active
// sprint present - CurrentSprint treats "closed" the same way, and picking
// one here would pin new issues to an iteration that already ended.
func TestActiveSprintNeverReturnsAClosedSprint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/agile/1.0/board", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[{"id":7}]}`))
	})
	mux.HandleFunc("/rest/agile/1.0/board/7/sprint", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[{"id":1,"name":"Team 0713-0717","state":"closed"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.ActiveSprint(context.Background(), "PROJ", "Team")
	if err == nil {
		t.Fatal("want an error, a closed sprint must not be returned")
	}
}

// ActiveSprint must not stop at the first page of GET /board: a site with
// enough boards to need a second page must still have that second board's
// active sprint found, not silently ignored because isLast was false on
// page one.
func TestActiveSprintPaginatesAcrossMultipleBoardPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/agile/1.0/board", func(w http.ResponseWriter, r *http.Request) {
		startAt := r.URL.Query().Get("startAt")
		switch startAt {
		case "0":
			_, _ = w.Write([]byte(`{"values":[{"id":1}],"isLast":false}`))
		case "50":
			_, _ = w.Write([]byte(`{"values":[{"id":2}],"isLast":true}`))
		default:
			t.Errorf("unexpected startAt %q", startAt)
			_, _ = w.Write([]byte(`{"values":[],"isLast":true}`))
		}
	})
	mux.HandleFunc("/rest/agile/1.0/board/1/sprint", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[],"isLast":true}`))
	})
	mux.HandleFunc("/rest/agile/1.0/board/2/sprint", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[{"id":99,"name":"Team 0803-0807","state":"active"}],"isLast":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.ActiveSprint(context.Background(), "PROJ", "Team")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 99 {
		t.Errorf("got = %+v, want the active sprint on the second board page (id 99)", got)
	}
}

// The same pagination must hold for GET /board/{id}/sprint: a board whose
// active sprint sits on a second sprints page must still be found.
func TestActiveSprintPaginatesAcrossMultipleSprintPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/agile/1.0/board", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[{"id":7}],"isLast":true}`))
	})
	mux.HandleFunc("/rest/agile/1.0/board/7/sprint", func(w http.ResponseWriter, r *http.Request) {
		startAt := r.URL.Query().Get("startAt")
		switch startAt {
		case "0":
			_, _ = w.Write([]byte(`{"values":[{"id":1,"name":"Other 0713-0717","state":"future"}],"isLast":false}`))
		case "50":
			_, _ = w.Write([]byte(`{"values":[{"id":3,"name":"Team 0803-0807","state":"active"}],"isLast":true}`))
		default:
			t.Errorf("unexpected startAt %q", startAt)
			_, _ = w.Write([]byte(`{"values":[],"isLast":true}`))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	got, err := c.ActiveSprint(context.Background(), "PROJ", "Team")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 3 || got.State != "active" {
		t.Errorf("got = %+v, want the active sprint on the second sprint page (id 3)", got)
	}
}

// A page that never sets isLast=true must not spin forever - the safety cap
// exists exactly for a misbehaving site like this one.
func TestActiveSprintStopsAtTheMaxPageCapWhenIsLastNeverArrives(t *testing.T) {
	hits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/agile/1.0/board", func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"values":[{"id":1}],"isLast":false}`))
	})
	mux.HandleFunc("/rest/agile/1.0/board/1/sprint", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[],"isLast":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.ActiveSprint(context.Background(), "PROJ", "Team")
	if err == nil {
		t.Fatal("want an error, no active/future sprint was ever returned")
	}
	if hits != maxActiveSprintPages {
		t.Errorf("board endpoint was hit %d times, want the cap of %d", hits, maxActiveSprintPages)
	}
}

// A project with no board is a project ActiveSprint cannot resolve anything
// for; the error must name the project rather than surface an empty result
// that looks like "found nothing to do", not "cannot do this".
func TestActiveSprintErrorsWhenProjectHasNoBoard(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/agile/1.0/board", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"values":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.ActiveSprint(context.Background(), "PROJ", "Team")
	if err == nil || !strings.Contains(err.Error(), "PROJ") {
		t.Fatalf("want an error naming the project, got %v", err)
	}
}

// Create must send the issue's description as ADF, matching AddComment's
// own reasoning (comment.go): Jira stores a Markdown string literally,
// asterisks and all, rather than rendering it.
func TestCreateSendsDescriptionAsADF(t *testing.T) {
	var postedBody map[string]json.RawMessage
	srv := fieldServer(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var req createRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			raw, _ := json.Marshal(req.Fields)
			_ = json.Unmarshal(raw, &postedBody)
			_, _ = w.Write([]byte(`{"key":"PROJ-2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"key":"PROJ-2","fields":{"summary":"s"}}`))
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if _, err := c.Create(context.Background(), NewIssue{
		ProjectKey: "PROJ", Type: "Task", Summary: "s", Description: "first line",
	}); err != nil {
		t.Fatal(err)
	}

	var doc adfDoc
	if err := json.Unmarshal(postedBody["description"], &doc); err != nil {
		t.Fatalf("description was not a decodable ADF doc: %v", err)
	}
	if doc.Type != "doc" || doc.Version != 1 {
		t.Errorf("description = %+v, want a versioned ADF doc", doc)
	}
}
