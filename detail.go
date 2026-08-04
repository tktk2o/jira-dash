package main

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/mattn/go-runewidth"
)

// detailDebounce is how long the cursor must sit still before the body is
// fetched. Each jira call costs ~360ms of startup, so fetching per keystroke
// would make scrolling feel broken.
const detailDebounce = 150 * time.Millisecond

// selectionChanged arms a debounced load for whatever is now selected. The
// sequence number is what makes an earlier tick a no-op.
//
// Pointer receiver: it mutates detailSeq. Call sites inside Update hold an
// addressable copy of the model, so `m.selectionChanged()` updates the copy
// they return.
func (m *Model) selectionChanged() tea.Cmd {
	issue, ok := m.sections[m.active].selected()
	if !ok {
		m.detailKey = ""
		m.detailBody = ""
		m.detailComments = nil
		m.detail.SetContent("")
		return nil
	}

	m.detailSeq++
	m.detailKey = issue.Key
	// The body and comments belong to the row we just left; keeping them would
	// put another issue's text under this issue's header.
	m.detailBody = ""
	m.detailComments = nil
	// The header needs no call, so the pane is never blank while the two
	// fetches are in flight.
	m.refreshDetail(true)

	seq, key := m.detailSeq, issue.Key
	return tea.Tick(detailDebounce, func(time.Time) tea.Msg {
		return debounceMsg{seq: seq, key: key}
	})
}

// refreshDetail rebuilds the pane from whatever has arrived so far. Three
// sources land at different times - the header came with the search, the body
// from `jira get`, the comments from `jira comment list` - so every arrival
// re-renders the whole pane rather than appending to it.
func (m *Model) refreshDetail(reset bool) {
	issue, ok := m.sections[m.active].selected()
	if !ok {
		m.detail.SetContent("")
		return
	}

	width := m.detail.Width
	ps := newPreviewStyles(m.cfg.Theme)
	parts := []string{renderPreviewHeader(issue, m.now(), width, ps), rule(width, ps)}
	if m.detailBody == "" {
		parts = append(parts, ps.meta.Render("loading..."))
	} else {
		parts = append(parts, strings.TrimSpace(renderMarkdown(m.detailBody, width)))
	}
	parts = append(parts,
		rule(width, ps),
		ps.heading.Render("COMMENTS"),
		"",
		renderComments(m.detailComments, m.now(), width, ps),
	)
	m.detail.SetContent(strings.Join(parts, "\n"))
	if reset {
		// The viewport keeps its offset across a SetContent, so a new issue would
		// otherwise open scrolled to wherever the last one was left.
		m.detail.GotoTop()
	}
}

func rule(width int, ps previewStyles) string {
	if width <= 0 {
		return ""
	}
	return ps.rule.Render(strings.Repeat("─", width))
}

// loadComments is its own call because `jira comment list` is a separate
// subcommand - another ~360ms. It is not cached: a comment thread is the part of
// an issue most likely to have changed since you last looked.
func (m Model) loadComments(key string) tea.Cmd {
	searcher := m.searcher
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		comments, err := searcher.Comments(ctx, key)
		return commentsLoadedMsg{key: key, comments: comments, err: err}
	}
}

// loadIssue returns the body from cache when it is fresh, and otherwise asks
// the CLI. Writing the cache is best effort.
func (m Model) loadIssue(key string) tea.Cmd {
	cache, searcher, now := m.cache, m.searcher, m.now
	return func() tea.Msg {
		if md, ok := cache.ReadIssue(key, issueTTL, now()); ok {
			return issueLoadedMsg{key: key, markdown: md}
		}

		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		md, err := searcher.Issue(ctx, key)
		if err != nil {
			return issueLoadedMsg{key: key, err: err}
		}
		_ = cache.WriteIssue(key, md)
		return issueLoadedMsg{key: key, markdown: md}
	}
}

// renderPreviewHeader answers "what am I looking at" without waiting for the
// body: every field here came back with the search, so it costs no extra call
// and is on screen the moment the cursor lands.
//
// Laid out like gh-dash's preview: identity, title, a meta line of dot-separated
// facts, then labels as chips.
func renderPreviewHeader(i Issue, now time.Time, width int, ps previewStyles) string {
	lines := []string{
		ps.identity.Render(Truncate(i.Key+"  ·  "+orDefault(i.Project.Key, "-"), width)),
		"",
		ps.title.Render(Truncate(i.Summary, width)),
		"",
	}

	// Only facts Jira actually gave: an empty one would read as "unknown" where
	// the field is simply not part of this issue's workflow.
	meta := []string{TypeIcon(i.Type) + " " + orDefault(i.Type, "-"), orDefault(i.Status, "-")}
	if i.StoryPoints != nil {
		meta = append(meta, StoryPointText(i.StoryPoints)+" SP")
	}
	if i.Priority != "" {
		meta = append(meta, i.Priority)
	}
	meta = append(meta, i.AssigneeName())
	if sprint, ok := i.CurrentSprint(); ok {
		meta = append(meta, sprint.Name)
	}
	// The age is styled apart from the rest, the way gh-dash drops "1w ago" to
	// grey in a line that is otherwise dim: it is the one fact here that changes
	// on its own, without anyone editing the issue. Both halves are cut to fit
	// before either is styled - measuring a styled string counts its escape
	// sequences as cells.
	age := ""
	if !i.Updated.IsZero() {
		age = " ⋅ " + RelTime(now, i.Updated.Time) + " ago"
	}
	// The age is dropped rather than truncated when the pane is too narrow for
	// it. Truncating it left the meta line cut to nothing and the age hanging
	// past the edge of the pane, because only the meta half was being budgeted.
	if runewidth.StringWidth(age) > width {
		age = ""
	}
	line := ps.meta.Render(Truncate(strings.Join(meta, " ⋅ "),
		maxInt(0, width-runewidth.StringWidth(age))))
	if age != "" {
		line += ps.age.Render(age)
	}
	lines = append(lines, line)

	if len(i.Labels) > 0 {
		chips := make([]string, 0, len(i.Labels))
		for _, l := range i.Labels {
			chips = append(chips, "["+l+"]")
		}
		lines = append(lines, ps.label.Render(Truncate(strings.Join(chips, " "), width)))
	}

	return strings.Join(lines, "\n")
}

// renderComments is where an issue's history of decisions lives, which is
// usually the reason for opening it at all.
func renderComments(comments []Comment, now time.Time, width int, ps previewStyles) string {
	if len(comments) == 0 {
		return ps.meta.Render("no comments")
	}
	out := make([]string, 0, len(comments)*3)
	for _, c := range comments {
		age := " ⋅ -"
		if !c.Created.IsZero() {
			age = " ⋅ " + RelTime(now, c.Created.Time)
		}
		// Dropped, not truncated, when it cannot fit - the same reason as the
		// header's age: only the author half was being budgeted.
		if runewidth.StringWidth(age) > width {
			age = ""
		}
		author := Truncate(orDefault(c.Author, "-"), maxInt(0, width-runewidth.StringWidth(age)))
		out = append(out,
			ps.author.Render(author)+ps.age.Render(age),
			// The body is left to the markdown renderer downstream; wrapping it
			// here would fight with it.
			c.Body,
			"")
	}
	return strings.Join(out, "\n")
}

// renderMarkdown styles the body, falling back to the raw text when glamour
// cannot build a renderer - an unstyled description beats an empty pane.
func renderMarkdown(md string, width int) string {
	if width <= 0 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dracula"), glamour.WithWordWrap(width))
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return out
}
