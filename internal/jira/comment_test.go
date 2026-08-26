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

// A mark that takes no attrs must be encoded without an "attrs" key at all.
// Jira rejects the whole request with HTTP 400 INVALID_INPUT when it sees
// "attrs":null on a mark, so this is the difference between a body it stores
// and a body it refuses - not a cosmetic one.
func TestMarkdownToADFEncodesMarksWithoutANullAttrs(t *testing.T) {
	encoded, err := json.Marshal(markdownToADF("**bold** and `code` and [text](https://example.com)"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"attrs":null`) {
		t.Errorf("encoded body carries a null attrs: %s", encoded)
	}
	// The one mark that does take attrs must keep them.
	if !strings.Contains(string(encoded), `"href":"https://example.com"`) {
		t.Errorf("link mark lost its href: %s", encoded)
	}
}

// EditComment must PUT to the comment's own URL, not POST to the issue's
// comment collection - the latter would silently add a second comment
// instead of replacing the one asked for.
func TestEditCommentPutsToTheCommentsOwnURL(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody addCommentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"id":"10001","body":{"type":"doc","content":[]}}`))
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv.URL).EditComment(context.Background(), "ABC-1", "10001", "**new** body")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/rest/api/3/issue/ABC-1/comment/10001") {
		t.Errorf("path = %q", gotPath)
	}
	if got.ID != "10001" {
		t.Errorf("returned comment = %+v, want the edited one", got)
	}
	first := gotBody.Body.Content[0].Content[0]
	if len(first.Marks) != 1 || first.Marks[0].Type != "strong" {
		t.Errorf("body = %+v, want the Markdown converted to ADF", gotBody.Body)
	}
}

// **bold** must become a strong mark on the text node rather than reaching
// Jira as literal asterisks - the write side is what decides this, since
// Jira stores whatever shape it is handed.
func TestMarkdownToADFMarksBoldAsStrong(t *testing.T) {
	doc := markdownToADF("plain **bold** tail")

	content := doc.Content[0].Content
	if len(content) != 3 {
		t.Fatalf("content = %+v, want text, strong text, text", content)
	}
	if content[0].Text != "plain " || len(content[0].Marks) != 0 {
		t.Errorf("leading text = %+v, want unmarked %q", content[0], "plain ")
	}
	if content[1].Text != "bold" {
		t.Errorf("marked text = %q, want %q", content[1].Text, "bold")
	}
	if len(content[1].Marks) != 1 || content[1].Marks[0].Type != "strong" {
		t.Errorf("marks = %+v, want one strong mark", content[1].Marks)
	}
	if content[2].Text != " tail" {
		t.Errorf("trailing text = %q, want %q", content[2].Text, " tail")
	}
}

// A backtick span must become a code mark. This is the mark that matters
// most for this tool's own output: file paths and identifiers are most of
// what gets pasted into a comment, and backticks around them are noise once
// Jira has stored them literally.
func TestMarkdownToADFMarksBacktickSpanAsCode(t *testing.T) {
	content := markdownToADF("see `settings.py:494` first").Content[0].Content

	if len(content) != 3 {
		t.Fatalf("content = %+v, want text, code text, text", content)
	}
	if content[1].Text != "settings.py:494" {
		t.Errorf("marked text = %q", content[1].Text)
	}
	if len(content[1].Marks) != 1 || content[1].Marks[0].Type != "code" {
		t.Errorf("marks = %+v, want one code mark", content[1].Marks)
	}
}

// *italic* must become em, and must not be mistaken for half of a **bold**
// run - the two delimiters share a prefix, so the longer one has to win.
func TestMarkdownToADFMarksSingleAsterisksAsEmAndDoubleAsStrong(t *testing.T) {
	content := markdownToADF("*one* and **two**").Content[0].Content

	if len(content) != 3 {
		t.Fatalf("content = %+v, want em text, text, strong text", content)
	}
	if content[0].Text != "one" || len(content[0].Marks) != 1 || content[0].Marks[0].Type != "em" {
		t.Errorf("first run = %+v, want em on %q", content[0], "one")
	}
	if content[2].Text != "two" || len(content[2].Marks) != 1 || content[2].Marks[0].Type != "strong" {
		t.Errorf("last run = %+v, want strong on %q", content[2], "two")
	}
}

// [text](href) must become a link mark carrying href in attrs - the shape
// writeMarkedText reads back out, and the only mark here that needs attrs at
// all.
func TestMarkdownToADFMarksBracketRunAsLinkWithHref(t *testing.T) {
	content := markdownToADF("see [the docs](https://example.com/a)").Content[0].Content

	last := content[len(content)-1]
	if last.Text != "the docs" {
		t.Fatalf("link text = %q, want %q", last.Text, "the docs")
	}
	if len(last.Marks) != 1 || last.Marks[0].Type != "link" {
		t.Fatalf("marks = %+v, want one link mark", last.Marks)
	}
	if got := last.Marks[0].Attrs["href"]; got != "https://example.com/a" {
		t.Errorf("href = %v, want %q", got, "https://example.com/a")
	}
}

// "## text" must become a heading node carrying its level in attrs, the
// shape renderADFNode reads back out.
func TestMarkdownToADFBuildsHeadingWithLevel(t *testing.T) {
	doc := markdownToADF("## Findings")

	if len(doc.Content) != 1 {
		t.Fatalf("content = %+v, want one heading", doc.Content)
	}
	head := doc.Content[0]
	if head.Type != "heading" {
		t.Fatalf("type = %q, want heading", head.Type)
	}
	if got := head.Attrs["level"]; got != 2 {
		t.Errorf("level = %v, want 2", got)
	}
	if len(head.Content) != 1 || head.Content[0].Text != "Findings" {
		t.Errorf("content = %+v, want the heading text without its hashes", head.Content)
	}
}

// A run of "- " lines must become one bulletList holding a listItem per
// line, each wrapping its text in a paragraph - the nesting Jira requires,
// and the reason a flat list of text nodes would render as one run-on line.
func TestMarkdownToADFGroupsDashLinesIntoOneBulletList(t *testing.T) {
	doc := markdownToADF("- first\n- second")

	if len(doc.Content) != 1 || doc.Content[0].Type != "bulletList" {
		t.Fatalf("content = %+v, want one bulletList", doc.Content)
	}
	items := doc.Content[0].Content
	if len(items) != 2 {
		t.Fatalf("items = %+v, want two listItems", items)
	}
	for i, want := range []string{"first", "second"} {
		if items[i].Type != "listItem" {
			t.Errorf("item %d type = %q, want listItem", i, items[i].Type)
		}
		if len(items[i].Content) != 1 || items[i].Content[0].Type != "paragraph" {
			t.Fatalf("item %d = %+v, want a paragraph inside", i, items[i])
		}
		if got := items[i].Content[0].Content[0].Text; got != want {
			t.Errorf("item %d text = %q, want %q", i, got, want)
		}
	}
}

// An indented "- " line must nest inside the item above it rather than start
// a second top-level list, so a sub-point stays a sub-point.
func TestMarkdownToADFNestsIndentedBulletsUnderTheItemAbove(t *testing.T) {
	doc := markdownToADF("- parent\n  - child")

	if len(doc.Content) != 1 {
		t.Fatalf("content = %+v, want one top-level list", doc.Content)
	}
	items := doc.Content[0].Content
	if len(items) != 1 {
		t.Fatalf("items = %+v, want the child nested, not a second item", items)
	}
	inner := items[0].Content
	if len(inner) != 2 || inner[1].Type != "bulletList" {
		t.Fatalf("parent item = %+v, want paragraph then nested bulletList", inner)
	}
	child := inner[1].Content[0].Content[0].Content[0].Text
	if child != "child" {
		t.Errorf("nested text = %q, want %q", child, "child")
	}
}

// "1." lines must become an orderedList, not a bulletList - the numbering is
// the whole point of writing them that way.
func TestMarkdownToADFGroupsNumberedLinesIntoOrderedList(t *testing.T) {
	doc := markdownToADF("1. first\n2. second")

	if len(doc.Content) != 1 || doc.Content[0].Type != "orderedList" {
		t.Fatalf("content = %+v, want one orderedList", doc.Content)
	}
	if len(doc.Content[0].Content) != 2 {
		t.Errorf("items = %+v, want two", doc.Content[0].Content)
	}
}

// A fenced block must become a codeBlock carrying its language, and its body
// must stay literal - inline marks inside code would corrupt the very thing
// a fence is for.
func TestMarkdownToADFBuildsCodeBlockAndLeavesItsBodyLiteral(t *testing.T) {
	doc := markdownToADF("```go\nx := **y**\n```")

	if len(doc.Content) != 1 || doc.Content[0].Type != "codeBlock" {
		t.Fatalf("content = %+v, want one codeBlock", doc.Content)
	}
	block := doc.Content[0]
	if got := block.Attrs["language"]; got != "go" {
		t.Errorf("language = %v, want go", got)
	}
	if len(block.Content) != 1 || block.Content[0].Text != "x := **y**" {
		t.Errorf("body = %+v, want the source verbatim", block.Content)
	}
	if len(block.Content) == 1 && len(block.Content[0].Marks) != 0 {
		t.Errorf("marks = %+v, want none inside a code block", block.Content[0].Marks)
	}
}

// "> " lines must become a blockquote wrapping a paragraph, which is how a
// quoted reply survives the round trip.
func TestMarkdownToADFBuildsBlockquoteFromQuotedLines(t *testing.T) {
	doc := markdownToADF("> quoted")

	if len(doc.Content) != 1 || doc.Content[0].Type != "blockquote" {
		t.Fatalf("content = %+v, want one blockquote", doc.Content)
	}
	inner := doc.Content[0].Content
	if len(inner) != 1 || inner[0].Type != "paragraph" || inner[0].Content[0].Text != "quoted" {
		t.Errorf("blockquote content = %+v, want a paragraph holding the text", inner)
	}
}

// "---" on its own must become a rule, not a paragraph of three hyphens.
func TestMarkdownToADFBuildsRuleFromThreeHyphens(t *testing.T) {
	doc := markdownToADF("above\n\n---\n\nbelow")

	if len(doc.Content) != 3 {
		t.Fatalf("content = %+v, want paragraph, rule, paragraph", doc.Content)
	}
	if doc.Content[1].Type != "rule" {
		t.Errorf("middle node = %+v, want a rule", doc.Content[1])
	}
}

// A document using every construct the writer supports must survive a trip
// through the reader unchanged. This is the test that keeps the two sides
// describing the same subset of ADF as either one grows.
func TestMarkdownToADFRoundTripsEverySupportedConstruct(t *testing.T) {
	markdown := strings.Join([]string{
		"## Heading",
		"",
		"prose with **bold**, *italic*, `code` and [a link](https://example.com)",
		"",
		"- first",
		"- second",
		"  - nested",
		"",
		"1. one",
		"2. two",
		"",
		"```go",
		"x := 1",
		"```",
		"",
		"> quoted",
		"",
		"---",
		"",
		"tail",
	}, "\n")

	encoded, err := json.Marshal(markdownToADF(markdown))
	if err != nil {
		t.Fatal(err)
	}
	got, err := renderADFToMarkdown(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != markdown {
		t.Errorf("round trip =\n%s\n\nwant\n%s", got, markdown)
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
