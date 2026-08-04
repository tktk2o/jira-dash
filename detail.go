package main

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
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
		m.detail.SetContent("")
		return nil
	}

	m.detailSeq++
	m.detailKey = issue.Key
	seq, key := m.detailSeq, issue.Key
	return tea.Tick(detailDebounce, func(time.Time) tea.Msg {
		return debounceMsg{seq: seq, key: key}
	})
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
