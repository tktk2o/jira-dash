package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// editFieldServer wires up GET /field (so Edit can resolve customfield ids
// before building its request) and captures whatever PUT /issue/{key} sends,
// mirroring create_test.go's fieldServer for the write side edit.go covers.
func editFieldServer(t *testing.T, teamID, sprintID, storyPointsID string, gotFields *map[string]json.RawMessage) *httptest.Server {
	return editFieldServerWithFlag(t, teamID, sprintID, storyPointsID, "", gotFields)
}

// editFieldServerWithFlag is editFieldServer plus an optional Flagged field
// id - split out rather than adding a second GET /field registration per
// test, since http.ServeMux panics on a pattern registered twice.
func editFieldServerWithFlag(t *testing.T, teamID, sprintID, storyPointsID, flaggedID string, gotFields *map[string]json.RawMessage) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/field", func(w http.ResponseWriter, _ *http.Request) {
		var fields []rawField
		if teamID != "" {
			fields = append(fields, rawField{ID: teamID, Name: "Team", UntranslatedName: "Team"})
		}
		if sprintID != "" {
			fields = append(fields, rawField{ID: sprintID, Name: "Sprint", UntranslatedName: "Sprint"})
		}
		if storyPointsID != "" {
			fields = append(fields, rawField{ID: storyPointsID, Name: "Story Points", UntranslatedName: "Story Points"})
		}
		if flaggedID != "" {
			fields = append(fields, rawField{ID: flaggedID, Name: "Flagged", UntranslatedName: "Flagged"})
		}
		_ = json.NewEncoder(w).Encode(fields)
	})
	mux.HandleFunc("/rest/api/3/issue/ABC-1", func(w http.ResponseWriter, r *http.Request) {
		var req editRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		raw, _ := json.Marshal(req.Fields)
		_ = json.Unmarshal(raw, gotFields)
		w.WriteHeader(http.StatusNoContent)
	})
	return httptest.NewServer(mux)
}

// A field left nil in Edit must not appear in the request at all - Jira's
// own semantics treat a present-but-omitted field as "leave it alone", and
// this is the only way Edit can ask for that on fields it never mentions.
func TestEditOmitsFieldsLeftNil(t *testing.T) {
	var got map[string]json.RawMessage
	srv := editFieldServer(t, "", "", "", &got)
	defer srv.Close()

	summary := "new summary"
	if err := newTestClient(t, srv.URL).Edit(context.Background(), "ABC-1", Edit{Summary: &summary}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got fields = %v, want only summary", got)
	}
	var s string
	_ = json.Unmarshal(got["summary"], &s)
	if s != summary {
		t.Errorf("summary = %q, want %q", s, summary)
	}
}

// A field set to the "null" sentinel must clear it (an explicit JSON null),
// distinct from being left nil (omitted). This is the contract Edit's own
// doc comment names, and it is the whole reason every field is a pointer.
func TestEditClearsAFieldSetToTheNullSentinel(t *testing.T) {
	var got map[string]json.RawMessage
	srv := editFieldServer(t, "", "", "", &got)
	defer srv.Close()

	clear := "null"
	if err := newTestClient(t, srv.URL).Edit(context.Background(), "ABC-1", Edit{Assignee: &clear}); err != nil {
		t.Fatal(err)
	}
	raw, ok := got["assignee"]
	if !ok {
		t.Fatal("assignee should be present in the request to clear it")
	}
	if string(raw) != "null" {
		t.Errorf("assignee = %s, want a JSON null", raw)
	}
}

// Labels arrive from the CLI as one comma-separated string but must go up as
// a JSON array - Jira's schema never accepts a bare string for this field.
func TestEditSendsLabelsAsAnArray(t *testing.T) {
	var got map[string]json.RawMessage
	srv := editFieldServer(t, "", "", "", &got)
	defer srv.Close()

	labels := "bug, needs-triage"
	if err := newTestClient(t, srv.URL).Edit(context.Background(), "ABC-1", Edit{Labels: &labels}); err != nil {
		t.Fatal(err)
	}
	var list []string
	if err := json.Unmarshal(got["labels"], &list); err != nil {
		t.Fatalf("labels was not a JSON array: %v", err)
	}
	if len(list) != 2 || list[0] != "bug" || list[1] != "needs-triage" {
		t.Errorf("labels = %v, want [bug needs-triage] (whitespace trimmed)", list)
	}
}

// Clearing the comma-list fields must send an empty array, not a null or an
// omitted key - Jira's schema for labels/fixVersions/components has no other
// way to say "none".
func TestEditClearsFixVersionsAsAnEmptyArray(t *testing.T) {
	var got map[string]json.RawMessage
	srv := editFieldServer(t, "", "", "", &got)
	defer srv.Close()

	clear := "null"
	if err := newTestClient(t, srv.URL).Edit(context.Background(), "ABC-1", Edit{FixVersions: &clear}); err != nil {
		t.Fatal(err)
	}
	var list []any
	if err := json.Unmarshal(got["fixVersions"], &list); err != nil {
		t.Fatalf("fixVersions was not a JSON array: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("fixVersions = %v, want an empty array", list)
	}
}

// A custom field the caller set on a site that has no such field configured
// must fail with an error naming the field - the migration plan calls out
// that silently dropping it would hide a real failure behind what looks like
// success.
func TestEditErrorsWhenTeamFieldRequestedButSiteHasNone(t *testing.T) {
	var got map[string]json.RawMessage
	srv := editFieldServer(t, "", "", "", &got)
	defer srv.Close()

	team := "Platform"
	err := newTestClient(t, srv.URL).Edit(context.Background(), "ABC-1", Edit{Team: &team})
	if err == nil || !strings.Contains(err.Error(), "Team") {
		t.Fatalf("want an error naming the missing Team field, got %v", err)
	}
}

// Edit must key the Team value under the resolved customfield id, never a
// literal customfield_NNNNN - the id is site-specific, exactly the constraint
// fields.go exists to enforce.
func TestEditSendsTeamUnderTheResolvedCustomFieldID(t *testing.T) {
	var got map[string]json.RawMessage
	srv := editFieldServer(t, "customfield_10077", "", "", &got)
	defer srv.Close()

	team := "Platform"
	if err := newTestClient(t, srv.URL).Edit(context.Background(), "ABC-1", Edit{Team: &team}); err != nil {
		t.Fatal(err)
	}
	var v string
	if err := json.Unmarshal(got["customfield_10077"], &v); err != nil {
		t.Fatalf("customfield_10077 missing or not a string: %v (fields = %v)", err, got)
	}
	if v != team {
		t.Errorf("customfield_10077 = %q, want %q", v, team)
	}
}

// Story points must be sent as a number - Jira's schema rejects a numeric
// custom field sent as a string - and a caller-supplied value that does not
// parse must fail rather than silently sending garbage.
func TestEditRejectsAStoryPointsValueThatIsNotANumber(t *testing.T) {
	var got map[string]json.RawMessage
	srv := editFieldServer(t, "", "", "customfield_10020", &got)
	defer srv.Close()

	bad := "three"
	err := newTestClient(t, srv.URL).Edit(context.Background(), "ABC-1", Edit{StoryPoints: &bad})
	if err == nil || !strings.Contains(err.Error(), "three") {
		t.Fatalf("want an error naming the bad value, got %v", err)
	}
}

// Flag=true must set Jira's own Impediment marker, and Flag=false must clear
// it - this file documents that string as Jira's vocabulary, not this
// program's, and the test pins the exact value the doc comment names.
func TestEditFlagTrueSetsTheImpedimentMarker(t *testing.T) {
	var got map[string]json.RawMessage
	srv := editFieldServerWithFlag(t, "", "", "", "customfield_10030", &got)
	defer srv.Close()

	flag := true
	if err := newTestClient(t, srv.URL).Edit(context.Background(), "ABC-1", Edit{Flag: &flag}); err != nil {
		t.Fatal(err)
	}
	var values []map[string]string
	if err := json.Unmarshal(got["customfield_10030"], &values); err != nil {
		t.Fatalf("flagged field was not a JSON array: %v", err)
	}
	if len(values) != 1 || values[0]["value"] != "Impediment" {
		t.Errorf("flagged = %v, want [{value: Impediment}]", values)
	}
}

// Flag=false clears the marker with an empty array, matching how the
// comma-list fields clear (see TestEditClearsFixVersionsAsAnEmptyArray) -
// Jira has no other shape for "no impediment flags".
func TestEditFlagFalseClearsTheImpedimentMarker(t *testing.T) {
	var got map[string]json.RawMessage
	srv := editFieldServerWithFlag(t, "", "", "", "customfield_10030", &got)
	defer srv.Close()

	flag := false
	if err := newTestClient(t, srv.URL).Edit(context.Background(), "ABC-1", Edit{Flag: &flag}); err != nil {
		t.Fatal(err)
	}
	var values []any
	if err := json.Unmarshal(got["customfield_10030"], &values); err != nil {
		t.Fatalf("flagged field was not a JSON array: %v", err)
	}
	if len(values) != 0 {
		t.Errorf("flagged = %v, want an empty array", values)
	}
}

// Edit must send nothing at all when every field is left nil - a caller with
// no changes should not generate a request Jira could reject for an empty
// fields object.
func TestEditSendsNothingWhenNoFieldsAreSet(t *testing.T) {
	called := false
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/3/field", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]rawField{})
	})
	mux.HandleFunc("/rest/api/3/issue/ABC-1", func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := newTestClient(t, srv.URL).Edit(context.Background(), "ABC-1", Edit{}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("PUT /issue was called with nothing to change")
	}
}

// Transitions must map the id and name Jira sends, so Transition can match
// against Name and POST the corresponding ID.
func TestTransitionsListsWhatTheGETReturns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"transitions":[{"id":"11","name":"In Progress","to":{"name":"In Progress"}},{"id":"21","name":"Done","to":{"name":"Done"}}]}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).Transitions(context.Background(), "ABC-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "11" || got[0].Name != "In Progress" {
		t.Errorf("got = %+v", got)
	}
}

// Transition must match case-insensitively - a caller typing "done" or
// "Done" should not depend on Jira's own casing of the status name.
func TestTransitionMatchesCaseInsensitively(t *testing.T) {
	var postedID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"transitions":[{"id":"21","name":"Done","to":{"name":"Done"}}]}`))
		case http.MethodPost:
			var req transitionRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			postedID = req.Transition.ID
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	if err := newTestClient(t, srv.URL).Transition(context.Background(), "ABC-1", "done"); err != nil {
		t.Fatal(err)
	}
	if postedID != "21" {
		t.Errorf("posted transition id = %q, want 21", postedID)
	}
}

// An unmatched status name is the single most common way this call fails,
// and the error must list what was actually available - the old CLI did
// not, which the plan calls out as the least useful error in the tool.
func TestTransitionListsCandidatesWhenNameDoesNotMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"transitions":[{"id":"11","name":"In Progress","to":{"name":"In Progress"}},{"id":"21","name":"Done","to":{"name":"Done"}}]}`))
	}))
	defer srv.Close()

	err := newTestClient(t, srv.URL).Transition(context.Background(), "ABC-1", "Cancelled")
	if err == nil {
		t.Fatal("want an error for an unmatched status name")
	}
	if !strings.Contains(err.Error(), "In Progress") || !strings.Contains(err.Error(), "Done") {
		t.Errorf("error should list the available candidates, got %v", err)
	}
}
