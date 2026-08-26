package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// rawComment is one element of GET /issue/{key}/comment's `comments` array.
// Body is left as raw JSON rather than a struct because it is an ADF
// document, the same shape Issue's description carries.
type rawComment struct {
	ID      string          `json:"id"`
	Author  *rawUser        `json:"author"`
	Body    json.RawMessage `json:"body"`
	Created JiraTime        `json:"created"`
}

// toComment renders rawComment's ADF body to Markdown through the same
// renderer an issue's description uses, so Comment.Body ends up in the shape
// `-f json` has always promised.
func (r rawComment) toComment() (Comment, error) {
	body, err := renderADFToMarkdown(r.Body)
	if err != nil {
		return Comment{}, err
	}
	c := Comment{ID: r.ID, Body: body, Created: r.Created}
	if r.Author != nil {
		c.Author = r.Author.DisplayName
	}
	return c, nil
}

// commentsResponse is the body of GET /issue/{key}/comment.
type commentsResponse struct {
	Comments []rawComment `json:"comments"`
}

// Comments fetches up to max comments on key, oldest first (Jira's own
// default order), rendering each body from ADF to Markdown.
func (c *Client) Comments(ctx context.Context, key string, max int) ([]Comment, error) {
	q := "?maxResults=" + strconv.Itoa(max)
	var resp commentsResponse
	if err := c.do(ctx, http.MethodGet, "/issue/"+key+"/comment"+q, nil, &resp); err != nil {
		return nil, err
	}
	comments := make([]Comment, 0, len(resp.Comments))
	for _, raw := range resp.Comments {
		comment, err := raw.toComment()
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, nil
}

// adfDoc is the top-level shape Jira expects when a request body carries
// ADF (POST /issue/{key}/comment's `body`): "type":"doc" plus "version":1 at
// the top level, not nested under "attrs" the way adfNode's own Attrs field
// would encode it - which is why the request side gets its own type instead
// of reusing adfNode for the document root.
type adfDoc struct {
	Type    string         `json:"type"`
	Version int            `json:"version"`
	Content []adfWriteNode `json:"content"`
}

// adfWriteNode is the write-side counterpart to adfNode, restricted to the
// nodes markdownToADF produces. omitempty on every field but Type keeps a
// hardBreak node ("type":"hardBreak") from growing a stray "content":null or
// "text":"" that Jira's schema does not expect - and keeps an unmarked text
// node from carrying an empty "marks":[].
type adfWriteNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text,omitempty"`
	Content []adfWriteNode `json:"content,omitempty"`
	Marks   []adfMark      `json:"marks,omitempty"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// addCommentRequest is the body POST /issue/{key}/comment requires: Jira
// stores a comment as ADF, not as a Markdown string - posting the raw
// Markdown text would store it literally, asterisks and all.
type addCommentRequest struct {
	Body adfDoc `json:"body"`
}

// AddComment posts bodyMarkdown to key, converting it to ADF first (see
// markdownToADF for the subset of Markdown that survives).
func (c *Client) AddComment(ctx context.Context, key, bodyMarkdown string) (Comment, error) {
	req := addCommentRequest{Body: markdownToADF(bodyMarkdown)}
	var raw rawComment
	if err := c.do(ctx, http.MethodPost, "/issue/"+key+"/comment", req, &raw); err != nil {
		return Comment{}, err
	}
	return raw.toComment()
}

// EditComment replaces commentID's body on key, converting bodyMarkdown to
// ADF the same way AddComment does. Jira has no partial update here - the
// PUT carries the whole body - so a caller wanting to append has to read the
// comment first.
func (c *Client) EditComment(ctx context.Context, key, commentID, bodyMarkdown string) (Comment, error) {
	req := addCommentRequest{Body: markdownToADF(bodyMarkdown)}
	var raw rawComment
	if err := c.do(ctx, http.MethodPut, "/issue/"+key+"/comment/"+commentID, req, &raw); err != nil {
		return Comment{}, err
	}
	return raw.toComment()
}

// markdownToADF converts Markdown to an ADF document, covering the same
// subset renderADFToMarkdown reads back: headings, bullet and ordered lists
// (nested), fenced code blocks, blockquotes, rules, and paragraphs whose
// single newlines become hardBreaks. Keeping the two sides symmetric is the
// point - a body written here and read back by the TUI should describe the
// same document, and anything the writer cannot express would otherwise
// reach Jira as literal Markdown punctuation.
func markdownToADF(markdown string) adfDoc {
	return adfDoc{
		Type:    "doc",
		Version: 1,
		Content: blocksToADF(strings.Split(markdown, "\n")),
	}
}

// blocksToADF walks lines and emits one node per block. It is called
// recursively for a blockquote's contents, which is why it takes lines rather
// than the whole document.
func blocksToADF(lines []string) []adfWriteNode {
	var nodes []adfWriteNode
	for i := 0; i < len(lines); {
		node, next, ok := blockAt(lines, i)
		if ok {
			nodes = append(nodes, node)
		}
		i = next
	}
	return nodes
}

// blockAt reports the block starting at lines[i] and the index to continue
// from. ok is false for a blank line, which separates blocks and produces no
// node of its own.
func blockAt(lines []string, i int) (adfWriteNode, int, bool) {
	line := lines[i]
	if strings.TrimSpace(line) == "" {
		return adfWriteNode{}, i + 1, false
	}
	if lang, ok := fenceLanguage(line); ok {
		return codeBlockAt(lines, i, lang)
	}
	if isRule(line) {
		return adfWriteNode{Type: "rule"}, i + 1, true
	}
	if level, text, ok := headingParts(line); ok {
		return adfWriteNode{
			Type:    "heading",
			Attrs:   map[string]any{"level": level},
			Content: inlineToADF(text),
		}, i + 1, true
	}
	if strings.HasPrefix(strings.TrimSpace(line), ">") {
		return blockquoteAt(lines, i)
	}
	if _, ok := listItemParts(line); ok {
		return listAt(lines, i)
	}
	return paragraphAt(lines, i)
}

// fenceLanguage reports the language of a ``` fence line, "" for a bare
// fence. The language is whatever follows the backticks, matching how
// renderADFNode writes it back.
func fenceLanguage(line string) (string, bool) {
	if !strings.HasPrefix(line, "```") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "```")), true
}

func isRule(line string) bool {
	switch strings.TrimSpace(line) {
	case "---", "***", "___":
		return true
	}
	return false
}

// headingParts splits "### text" into its level and text. Six hashes is
// ADF's own limit, and a run without a following space ("#hashtag") is not a
// heading.
func headingParts(line string) (level int, text string, ok bool) {
	hashes := len(line) - len(strings.TrimLeft(line, "#"))
	if hashes < 1 || hashes > 6 || !strings.HasPrefix(line[hashes:], " ") {
		return 0, "", false
	}
	return hashes, strings.TrimSpace(line[hashes:]), true
}

// codeBlockAt consumes lines up to the closing fence, keeping the body
// verbatim - no inline parsing, since the delimiters a fence protects are
// exactly the ones inlineToADF would otherwise consume. An unclosed fence
// runs to the end of the body rather than erroring, which loses less than
// dropping the block would.
func codeBlockAt(lines []string, i int, lang string) (adfWriteNode, int, bool) {
	var body []string
	j := i + 1
	for ; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "```" {
			j++
			break
		}
		body = append(body, lines[j])
	}
	node := adfWriteNode{Type: "codeBlock", Attrs: map[string]any{}}
	if lang != "" {
		node.Attrs["language"] = lang
	}
	if len(body) > 0 {
		node.Content = []adfWriteNode{{Type: "text", Text: strings.Join(body, "\n")}}
	}
	return node, j, true
}

// blockquoteAt consumes the run of "> " lines and parses their contents as
// blocks, so a quote can hold a list or a second paragraph.
func blockquoteAt(lines []string, i int) (adfWriteNode, int, bool) {
	var inner []string
	j := i
	for ; j < len(lines); j++ {
		trimmed := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(trimmed, ">") {
			break
		}
		inner = append(inner, strings.TrimPrefix(strings.TrimPrefix(trimmed, ">"), " "))
	}
	return adfWriteNode{Type: "blockquote", Content: blocksToADF(inner)}, j, true
}

// paragraphAt consumes the run of plain lines up to a blank line or the start
// of another block, joining them with hardBreaks - the shape a
// single-line-broken paragraph takes in ADF.
func paragraphAt(lines []string, i int) (adfWriteNode, int, bool) {
	para := adfWriteNode{Type: "paragraph"}
	j := i
	for ; j < len(lines); j++ {
		if !isParagraphLine(lines, j) {
			break
		}
		if j > i {
			para.Content = append(para.Content, adfWriteNode{Type: "hardBreak"})
		}
		para.Content = append(para.Content, inlineToADF(lines[j])...)
	}
	return para, j, true
}

// isParagraphLine reports whether lines[j] continues a paragraph rather than
// starting a block of its own. The first line of a paragraph has already been
// classified by blockAt, so this only has to catch the boundaries.
func isParagraphLine(lines []string, j int) bool {
	line := lines[j]
	if strings.TrimSpace(line) == "" || isRule(line) {
		return false
	}
	if _, ok := fenceLanguage(line); ok {
		return false
	}
	if _, _, ok := headingParts(line); ok {
		return false
	}
	if strings.HasPrefix(strings.TrimSpace(line), ">") {
		return false
	}
	_, isItem := listItemParts(line)
	return !isItem
}

// listLine is one parsed "- x" / "1. x" line: its indent decides nesting and
// its marker decides whether the list around it is ordered.
type listLine struct {
	indent  int
	ordered bool
	text    string
}

// listItemParts splits a list line into indent, marker kind and text. A
// marker needs a following space, so "-5 degrees" stays prose.
func listItemParts(line string) (listLine, bool) {
	indent := len(line) - len(strings.TrimLeft(line, " \t"))
	rest := line[indent:]
	for _, marker := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(rest, marker) {
			return listLine{indent: indent, text: strings.TrimSpace(rest[len(marker):])}, true
		}
	}
	if digits := len(rest) - len(strings.TrimLeft(rest, "0123456789")); digits > 0 &&
		strings.HasPrefix(rest[digits:], ". ") {
		return listLine{
			indent:  indent,
			ordered: true,
			text:    strings.TrimSpace(rest[digits+2:]),
		}, true
	}
	return listLine{}, false
}

// listAt consumes the run of consecutive list lines and builds them into
// nested lists.
func listAt(lines []string, i int) (adfWriteNode, int, bool) {
	var items []listLine
	j := i
	for ; j < len(lines); j++ {
		item, ok := listItemParts(lines[j])
		if !ok {
			break
		}
		items = append(items, item)
	}
	node, _ := buildList(items, 0)
	return node, j, true
}

// buildList turns the flat run of list lines into one list node, recursing
// whenever the indent grows. A deeper line is attached to the item above it
// rather than starting a sibling list, which is what keeps a sub-point under
// its parent bullet.
func buildList(items []listLine, pos int) (adfWriteNode, int) {
	baseIndent := items[pos].indent
	listType := "bulletList"
	if items[pos].ordered {
		listType = "orderedList"
	}
	list := adfWriteNode{Type: listType}
	for pos < len(items) && items[pos].indent >= baseIndent {
		if items[pos].indent > baseIndent {
			child, next := buildList(items, pos)
			list.Content = appendToLastItem(list.Content, child)
			pos = next
			continue
		}
		list.Content = append(list.Content, adfWriteNode{
			Type:    "listItem",
			Content: []adfWriteNode{{Type: "paragraph", Content: inlineToADF(items[pos].text)}},
		})
		pos++
	}
	return list, pos
}

// appendToLastItem nests child inside the item a deeper line belongs to. A
// nested list with no item above it (a body that starts indented) is dropped
// rather than left dangling, since ADF has nowhere to put it.
func appendToLastItem(items []adfWriteNode, child adfWriteNode) []adfWriteNode {
	if len(items) == 0 {
		return items
	}
	last := items[len(items)-1]
	last.Content = append(last.Content, child)
	items[len(items)-1] = last
	return items
}

// inlineToADF splits one line into text nodes, attaching a mark to the runs
// that Markdown delimits. It is the inverse of writeMarkedText's mark
// handling, so what this program writes and what it reads back describe the
// same subset of ADF.
func inlineToADF(line string) []adfWriteNode {
	var nodes []adfWriteNode
	var plain strings.Builder
	flushPlain := func() {
		if plain.Len() > 0 {
			nodes = append(nodes, adfWriteNode{Type: "text", Text: plain.String()})
			plain.Reset()
		}
	}
	for i := 0; i < len(line); {
		marked, width, ok := matchInlineMark(line[i:])
		if !ok {
			plain.WriteByte(line[i])
			i++
			continue
		}
		flushPlain()
		nodes = append(nodes, marked)
		i += width
	}
	flushPlain()
	return nodes
}

// inlineDelimiters lists the wrapped-in-a-delimiter marks, longest delimiter
// first: "**" has to be tried before "*", or every bold run would be read as
// an empty em followed by literal text.
var inlineDelimiters = []struct {
	delim string
	mark  string
}{
	{"**", "strong"},
	{"*", "em"},
	{"`", "code"},
}

// matchInlineMark reports the marked text node at the start of s, plus how
// many bytes it consumed. Anything it does not recognise is left for the
// caller to keep as literal text, which is what makes a stray asterisk or an
// unclosed backtick survive untouched.
func matchInlineMark(s string) (adfWriteNode, int, bool) {
	for _, d := range inlineDelimiters {
		if text, width, ok := matchDelimited(s, d.delim); ok {
			node := adfWriteNode{Type: "text", Text: text, Marks: []adfMark{{Type: d.mark}}}
			return node, width, true
		}
	}
	if text, href, width, ok := matchLink(s); ok {
		node := adfWriteNode{
			Type:  "text",
			Text:  text,
			Marks: []adfMark{{Type: "link", Attrs: map[string]any{"href": href}}},
		}
		return node, width, true
	}
	return adfWriteNode{}, 0, false
}

// matchLink reports the [text](href) run at the start of s. The two halves
// have to be adjacent - "[a] (b)" is prose, not a link - and neither half may
// be empty, so a bare "[]" or "[x]()" stays literal.
func matchLink(s string) (text, href string, width int, ok bool) {
	if !strings.HasPrefix(s, "[") {
		return "", "", 0, false
	}
	closeText := strings.Index(s, "]")
	if closeText <= 1 || !strings.HasPrefix(s[closeText+1:], "(") {
		return "", "", 0, false
	}
	rest := s[closeText+2:]
	closeHref := strings.Index(rest, ")")
	if closeHref <= 0 {
		return "", "", 0, false
	}
	return s[1:closeText], rest[:closeHref], closeText + 3 + closeHref, true
}

// matchDelimited reports the text wrapped in delim at the start of s, plus
// how many bytes the whole run (both delimiters included) took. An unclosed
// delimiter is not a match, so a lone "**" stays literal text rather than
// swallowing the rest of the line.
func matchDelimited(s, delim string) (text string, width int, ok bool) {
	if !strings.HasPrefix(s, delim) {
		return "", 0, false
	}
	rest := s[len(delim):]
	end := strings.Index(rest, delim)
	if end <= 0 {
		return "", 0, false
	}
	return rest[:end], len(delim)*2 + end, true
}
