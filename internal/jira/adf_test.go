package jira

import (
	"strings"
	"testing"
)

// A nil description is the common case - most issues have none - and must
// not error or produce a stray "null" in the rendered Markdown.
func TestRenderADFToMarkdownHandlesANilBody(t *testing.T) {
	got, err := renderADFToMarkdown(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}

	got, err = renderADFToMarkdown([]byte("null"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty for a JSON null body", got)
	}
}

// A single paragraph with strong/em marks is the shape most descriptions
// take. Getting the marker order wrong here (**/* vs */**) would corrupt
// almost every real description this program renders.
func TestRenderADFToMarkdownRendersAParagraphWithMarks(t *testing.T) {
	doc := `{"type":"doc","content":[{"type":"paragraph","content":[
		{"type":"text","text":"hello "},
		{"type":"text","text":"world","marks":[{"type":"strong"}]},
		{"type":"text","text":" and "},
		{"type":"text","text":"tilted","marks":[{"type":"em"}]}
	]}]}`
	got, err := renderADFToMarkdown([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := "hello **world** and *tilted*"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Headings carry a level attribute that maps directly onto the number of
// leading '#'s; getting the attrs decode wrong silently degrades every
// heading to level 1.
func TestRenderADFToMarkdownRendersHeadingLevel(t *testing.T) {
	doc := `{"type":"doc","content":[{"type":"heading","attrs":{"level":2},"content":[
		{"type":"text","text":"Section"}
	]}]}`
	got, err := renderADFToMarkdown([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got != "## Section" {
		t.Errorf("got %q, want %q", got, "## Section")
	}
}

// A bulletList's items must render one "- " per item in document order -
// the CLI this replaces rendered checklists this way, and losing the order
// would silently reorder acceptance criteria.
func TestRenderADFToMarkdownRendersBulletList(t *testing.T) {
	doc := `{"type":"doc","content":[{"type":"bulletList","content":[
		{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"first"}]}]},
		{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"second"}]}]}
	]}]}`
	got, err := renderADFToMarkdown([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "- first") || !strings.Contains(got, "- second") {
		t.Errorf("got %q, want both bullets present in order", got)
	}
	if strings.Index(got, "- first") > strings.Index(got, "- second") {
		t.Errorf("got %q, want first before second", got)
	}
}

// orderedList numbers items 1., 2., ... regardless of any attrs.order Jira
// might send, since renumbering from the doc order is what every Markdown
// renderer does with a mismatched start anyway.
func TestRenderADFToMarkdownRendersOrderedList(t *testing.T) {
	doc := `{"type":"doc","content":[{"type":"orderedList","content":[
		{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]}]},
		{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}
	]}]}`
	got, err := renderADFToMarkdown([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "1. one") || !strings.Contains(got, "2. two") {
		t.Errorf("got %q, want numbered items", got)
	}
}

// A codeBlock's language attr becomes the fence's info string; losing it
// would drop syntax highlighting the author explicitly chose.
func TestRenderADFToMarkdownRendersCodeBlockWithLanguage(t *testing.T) {
	doc := `{"type":"doc","content":[{"type":"codeBlock","attrs":{"language":"go"},"content":[
		{"type":"text","text":"fmt.Println(1)"}
	]}]}`
	got, err := renderADFToMarkdown([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "```go") || !strings.Contains(got, "fmt.Println(1)") {
		t.Errorf("got %q, want a go-tagged fence with the code", got)
	}
}

// A link mark must wrap the text in Markdown's [text](href), reading the
// href out of the mark's attrs rather than the node's own - hrefs live on
// the mark, and mixing this up would silently drop every link's target.
func TestRenderADFToMarkdownRendersLinks(t *testing.T) {
	doc := `{"type":"doc","content":[{"type":"paragraph","content":[
		{"type":"text","text":"see docs","marks":[{"type":"link","attrs":{"href":"https://example.com"}}]}
	]}]}`
	got, err := renderADFToMarkdown([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := "[see docs](https://example.com)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// hardBreak must become a newline mid-paragraph, not a space - collapsing it
// would join two lines of an address or a signature into one run-on line.
func TestRenderADFToMarkdownRendersHardBreak(t *testing.T) {
	doc := `{"type":"doc","content":[{"type":"paragraph","content":[
		{"type":"text","text":"line one"},
		{"type":"hardBreak"},
		{"type":"text","text":"line two"}
	]}]}`
	got, err := renderADFToMarkdown([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got != "line one\nline two" {
		t.Errorf("got %q, want two lines joined by a bare newline", got)
	}
}

// An unrecognised node type (a panel, a table, a media embed) must still
// surface its text rather than vanish - a description that used to be
// three paragraphs cannot silently become two because the middle one used a
// node type this program has not special-cased.
func TestRenderADFToMarkdownRecoversTextFromAnUnknownNodeType(t *testing.T) {
	doc := `{"type":"doc","content":[{"type":"panel","attrs":{"panelType":"info"},"content":[
		{"type":"paragraph","content":[{"type":"text","text":"heads up"}]}
	]}]}`
	got, err := renderADFToMarkdown([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "heads up") {
		t.Errorf("got %q, want the unknown node's text preserved", got)
	}
}
