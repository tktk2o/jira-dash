package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Comments must map author.displayName, created, and the ADF body onto
// Comment's existing shape - the TUI parses that exact shape today and the
// CLI's -f json promises it, so a wrong field path here would silently blank
// a column rather than error.
func TestClientCommentsMapsJiraFieldsOntoComment(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		_, _ = w.Write([]byte(`{"comments":[
			{
				"id": "10001",
				"author": {"displayName": "Ada"},
				"created": "2024-01-02T03:04:05.000+0900",
				"body": {"type":"doc","content":[
					{"type":"paragraph","content":[{"type":"text","text":"looks good"}]}
				]}
			}
		]}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).Comments(context.Background(), "ABC-1", 25)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotPath, "/rest/api/3/issue/ABC-1/comment?maxResults=25") {
		t.Errorf("path = %q", gotPath)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].ID != "10001" || got[0].Author != "Ada" || got[0].Body != "looks good" {
		t.Errorf("got = %+v", got[0])
	}
	if got[0].Created.IsZero() {
		t.Error("Created should be parsed, not zero")
	}
}

// A comment with no author (a deleted account, or a system comment) must
// decode to an empty Author rather than error - the rest of the comment is
// still worth showing.
func TestClientCommentsLeavesAuthorEmptyWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"comments":[{"id":"1","body":{"type":"doc","content":[]}}]}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).Comments(context.Background(), "ABC-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Author != "" {
		t.Errorf("got = %+v, want empty Author", got)
	}
}

// AddComment must send the body as ADF, not as the raw Markdown string -
// Jira stores whatever it is handed literally, so a Markdown string here
// would show up in the UI complete with asterisks and blank lines.
func TestAddCommentSendsBodyAsADFNotMarkdown(t *testing.T) {
	var gotBody addCommentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"id":"1","body":{"type":"doc","content":[]}}`))
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL).AddComment(context.Background(), "ABC-1", "first paragraph\n\nsecond paragraph with a\nline break"); err != nil {
		t.Fatal(err)
	}

	if gotBody.Body.Type != "doc" || gotBody.Body.Version != 1 {
		t.Fatalf("body = %+v, want a versioned ADF doc", gotBody.Body)
	}
	if len(gotBody.Body.Content) != 2 {
		t.Fatalf("len(content) = %d, want 2 paragraphs", len(gotBody.Body.Content))
	}

	first := gotBody.Body.Content[0]
	if first.Type != "paragraph" || len(first.Content) != 1 || first.Content[0].Text != "first paragraph" {
		t.Errorf("first paragraph = %+v", first)
	}

	second := gotBody.Body.Content[1]
	if second.Type != "paragraph" || len(second.Content) != 3 {
		t.Fatalf("second paragraph = %+v, want text, hardBreak, text", second)
	}
	if second.Content[0].Text != "second paragraph with a" || second.Content[1].Type != "hardBreak" || second.Content[2].Text != "line break" {
		t.Errorf("second paragraph content = %+v", second.Content)
	}
}

// The two-paragraph, one-line-break body markdownToADF produces must round
// trip through renderADFToMarkdown back to readable text - the same
// guarantee the plan asks for, checked against the decoder rather than by
// re-deriving the ADF shape by hand.
func TestMarkdownToADFRoundTripsThroughRenderADFToMarkdown(t *testing.T) {
	markdown := "first paragraph\n\nsecond paragraph with a\nline break"
	doc := markdownToADF(markdown)

	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := renderADFToMarkdown(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := "first paragraph\n\nsecond paragraph with a\nline break"
	if got != want {
		t.Errorf("round trip = %q, want %q", got, want)
	}
}
