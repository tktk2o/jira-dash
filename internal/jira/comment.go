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
// paragraph/text/hardBreak nodes markdownToADF ever produces. omitempty on
// Content and Text keeps a hardBreak node ("type":"hardBreak") from growing
// a stray "content":null or "text":"" that Jira's schema does not expect.
type adfWriteNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text,omitempty"`
	Content []adfWriteNode `json:"content,omitempty"`
}

// addCommentRequest is the body POST /issue/{key}/comment requires: Jira
// stores a comment as ADF, not as a Markdown string - posting the raw
// Markdown text would store it literally, asterisks and all.
type addCommentRequest struct {
	Body adfDoc `json:"body"`
}

// AddComment posts bodyMarkdown to key, converting it to ADF first. The
// converter only handles paragraphs and hard line breaks (see
// markdownToADF) - a comment is prose, and nothing in this product posts a
// table or a heading into one.
func (c *Client) AddComment(ctx context.Context, key, bodyMarkdown string) (Comment, error) {
	req := addCommentRequest{Body: markdownToADF(bodyMarkdown)}
	var raw rawComment
	if err := c.do(ctx, http.MethodPost, "/issue/"+key+"/comment", req, &raw); err != nil {
		return Comment{}, err
	}
	return raw.toComment()
}

// markdownToADF converts plain prose to an ADF document: a blank line
// separates paragraphs, and a single newline within a paragraph becomes a
// hardBreak. This is deliberately not the inverse of renderADFToMarkdown for
// every construct ADF can express - only for the subset (paragraphs, hard
// breaks) a comment body ever needs, per the migration plan.
func markdownToADF(markdown string) adfDoc {
	doc := adfDoc{Type: "doc", Version: 1}
	for _, p := range strings.Split(markdown, "\n\n") {
		doc.Content = append(doc.Content, markdownParagraphToADF(p))
	}
	return doc
}

// markdownParagraphToADF turns one paragraph's text into a paragraph node,
// splitting on "\n" into text nodes joined by hardBreak - the ADF shape a
// single-line-broken paragraph takes.
func markdownParagraphToADF(paragraph string) adfWriteNode {
	lines := strings.Split(paragraph, "\n")
	para := adfWriteNode{Type: "paragraph"}
	for i, line := range lines {
		if i > 0 {
			para.Content = append(para.Content, adfWriteNode{Type: "hardBreak"})
		}
		para.Content = append(para.Content, adfWriteNode{Type: "text", Text: line})
	}
	return para
}
