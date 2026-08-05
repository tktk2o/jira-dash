package jira

import (
	"encoding/json"
	"fmt"
	"strings"
)

// adfNode is Atlassian Document Format's one recursive shape. marks carries
// inline formatting (strong, em, links); attrs carries per-node parameters
// (heading level, code language, link href). Both are left as raw JSON and
// decoded lazily, because the vast majority of nodes need neither.
type adfNode struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Content []adfNode      `json:"content"`
	Marks   []adfMark      `json:"marks"`
	Attrs   map[string]any `json:"attrs"`
}

type adfMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

// renderADFToMarkdown converts one Jira description/comment body (ADF, as
// `fields.description` comes back) into Markdown - the same lossy conversion
// the old TypeScript CLI did, so a diff against it is expected to be "equally
// imperfect", not byte-identical (see the plan's risk #2).
func renderADFToMarkdown(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var doc adfNode
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("decoding ADF body: %w", err)
	}
	var b strings.Builder
	renderADFNodes(&b, doc.Content, "")
	return strings.TrimSpace(b.String()), nil
}

// renderADFNodes writes each node in order, separated by blank lines for the
// block-level ones. listPrefix carries the bullet/number marker one level of
// list nesting deep would use; renderADFNode adds indentation per level.
func renderADFNodes(b *strings.Builder, nodes []adfNode, indent string) {
	for _, n := range nodes {
		renderADFNode(b, n, indent)
	}
}

func renderADFNode(b *strings.Builder, n adfNode, indent string) {
	switch n.Type {
	case "paragraph":
		writeInline(b, n.Content)
		b.WriteString("\n\n")
	case "heading":
		level := 1
		if lv, ok := n.Attrs["level"].(float64); ok {
			level = int(lv)
		}
		b.WriteString(strings.Repeat("#", level))
		b.WriteString(" ")
		writeInline(b, n.Content)
		b.WriteString("\n\n")
	case "bulletList":
		renderADFList(b, n.Content, indent, "- ")
	case "orderedList":
		renderADFOrderedList(b, n.Content, indent)
	case "listItem":
		// Reached only when a list item's own children include a nested list
		// with no bullet/number prefix of its own to reuse; renderADFList and
		// renderADFOrderedList otherwise handle listItem directly so the
		// marker lands on the first line.
		renderADFNodes(b, n.Content, indent)
	case "codeBlock":
		lang, _ := n.Attrs["language"].(string)
		b.WriteString(indent)
		b.WriteString("```")
		b.WriteString(lang)
		b.WriteString("\n")
		for _, c := range n.Content {
			b.WriteString(indent)
			b.WriteString(c.Text)
			b.WriteString("\n")
		}
		b.WriteString(indent)
		b.WriteString("```\n\n")
	case "blockquote":
		var inner strings.Builder
		renderADFNodes(&inner, n.Content, "")
		for _, line := range strings.Split(strings.TrimRight(inner.String(), "\n"), "\n") {
			b.WriteString("> ")
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	case "rule":
		b.WriteString("---\n\n")
	case "hardBreak":
		b.WriteString("\n")
	case "text":
		writeMarkedText(b, n)
	default:
		// An unknown node (mediaSingle, panel, table, ...) is not one this
		// program knows how to shape, but its text is still worth keeping -
		// dropping it silently would read as a truncated description rather
		// than an unsupported one.
		writeInline(b, n.Content)
	}
}

func renderADFList(b *strings.Builder, items []adfNode, indent, marker string) {
	for _, item := range items {
		renderADFListItem(b, item, indent, marker)
	}
}

func renderADFOrderedList(b *strings.Builder, items []adfNode, indent string) {
	for i, item := range items {
		renderADFListItem(b, item, indent, fmt.Sprintf("%d. ", i+1))
	}
}

// renderADFListItem writes one <listItem>'s first paragraph inline after the
// marker, then recurses into anything after it (a nested list, a second
// paragraph) one indent level deeper - which is what lets a sub-list line up
// under its parent bullet instead of restarting at the margin.
func renderADFListItem(b *strings.Builder, item adfNode, indent, marker string) {
	b.WriteString(indent)
	b.WriteString(marker)
	rest := item.Content
	if len(rest) > 0 && rest[0].Type == "paragraph" {
		writeInline(b, rest[0].Content)
		rest = rest[1:]
	}
	b.WriteString("\n")
	renderADFNodes(b, rest, indent+"  ")
}

// writeInline renders a run of inline nodes (text, hardBreak, and anything
// unrecognised) with no trailing block separator - the caller owns spacing
// between block-level nodes.
func writeInline(b *strings.Builder, nodes []adfNode) {
	for _, n := range nodes {
		switch n.Type {
		case "text":
			writeMarkedText(b, n)
		case "hardBreak":
			b.WriteString("\n")
		default:
			writeInline(b, n.Content)
		}
	}
}

// writeMarkedText applies a text node's marks (strong, em, link) around its
// text. Order is fixed (link wraps last) rather than mirroring the mark
// slice's order, since Jira does not guarantee one and "**[x](y)**" and
// "[**x**](y)" render the same in every Markdown renderer this program
// targets.
func writeMarkedText(b *strings.Builder, n adfNode) {
	text := n.Text
	var strong, em, code bool
	var href string
	for _, m := range n.Marks {
		switch m.Type {
		case "strong":
			strong = true
		case "em":
			em = true
		case "code":
			code = true
		case "link":
			if h, ok := m.Attrs["href"].(string); ok {
				href = h
			}
		}
	}
	if code {
		text = "`" + text + "`"
	}
	if strong {
		text = "**" + text + "**"
	}
	if em {
		text = "*" + text + "*"
	}
	if href != "" {
		text = "[" + text + "](" + href + ")"
	}
	b.WriteString(text)
}
