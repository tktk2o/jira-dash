package main

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// searchBoxChrome is what renderSearchBox costs vertically: a top border, the
// one content line, a bottom border. Unlike the old one-line renderQueryLine,
// this is a real box - see renderSearchBox's own comment for why the design
// asked for one this time even though an earlier comment on this codebase
// argued against it.
const searchBoxChrome = 3

// newJQLInput is the search box's input. Styled like newPromptInput's textarea
// so a focused box reads the same primary/secondary weighting as the rest of
// the dashboard, but a textinput rather than a textarea: a JQL edit is always
// one line, and a textinput gives horizontal scroll and a cursor for a query
// that runs past the box's width for free.
func newJQLInput(t Theme) textinput.Model {
	primary := lipgloss.Color(orDefault(t.Colors.Text.Primary, "#f8f8f2"))

	ti := textinput.New()
	ti.Prompt = ""
	ti.TextStyle = lipgloss.NewStyle().Foreground(primary)
	return ti
}

// openJQLEdit focuses the search box on the active section's effective JQL -
// the override if one is set, otherwise the config's own query, so re-opening
// a box that already diverged continues from what is actually running rather
// than resetting to the file's JQL underneath it.
func (m *Model) openJQLEdit() {
	m.editingJQL = true
	m.jqlDraft.SetValue(m.sections[m.active].effective().JQL)
	m.jqlDraft.CursorEnd()
	m.jqlDraft.Focus()
	m.syncJQLWidth()
}

// closeJQLEdit leaves the box without touching any section's state - the
// shared exit for esc and for commitJQLEdit, which closes before it writes.
func (m *Model) closeJQLEdit() {
	m.editingJQL = false
	m.jqlDraft.Blur()
}

// syncJQLWidth keeps the box's input as wide as the box itself draws, the
// same width tracking openPrompt does for the create/ask textarea. Called on
// every WindowSizeMsg and whenever the box opens, so a resize while editing
// and a terminal resized before e is ever pressed both leave the cursor
// landing where the visible box actually ends.
func (m *Model) syncJQLWidth() {
	m.jqlDraft.Width = max(1, m.tableWidth()-6)
}

// handleJQLKey routes keys while the search box is focused. Only enter and
// esc leave it; tab/shift+tab switch sections but must cancel the edit first
// - an override is per-section, and a half-typed query has no obvious "commit
// to which tab" once the active section changes under it - so canceling
// before falling through to the normal section-switch handling is the
// simplest rule that still lets tab work while the box is open. Every other
// key, including q/j/k and the letters that would otherwise be motions, goes
// to the textinput: this is a modal box like the filter's, not a shortcut
// bar.
func (m Model) handleJQLKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		return m.commitJQLEdit()
	case tea.KeyEsc:
		m.closeJQLEdit()
		return m, nil
	case tea.KeyTab, tea.KeyShiftTab:
		m.closeJQLEdit()
		return m.handleKey(msg)
	}
	var cmd tea.Cmd
	m.jqlDraft, cmd = m.jqlDraft.Update(msg)
	return m, cmd
}

// commitJQLEdit applies the draft to the active section: an empty (or
// whitespace-only) draft clears the override and reverts to the config JQL,
// anything else - including a draft that happens to match the config JQL
// verbatim - becomes the override. Either way the cache key is recomputed
// from what will actually be asked for and a refetch goes out immediately,
// the same r does: rows blanked, loading set, the preview cleared, so there
// is no moment where stale rows look current for a query that is no longer
// running.
func (m Model) commitJQLEdit() (tea.Model, tea.Cmd) {
	draft := strings.TrimSpace(m.jqlDraft.Value())
	m.closeJQLEdit()

	idx := m.active
	s := &m.sections[idx]
	if draft == s.cfg.JQL {
		draft = ""
	}
	s.jqlOverride = draft
	eff := s.effective()
	s.cacheKey = SectionKey(eff.JQL, eff.Limit)
	s.loading = true
	s.issues = nil
	s.cursor = 0
	s.err = nil
	m.detailKey = ""
	m.detail.SetContent("")

	return m, tea.Batch(fetchSection(m.searcher, idx, m.nextFetch(idx), eff), m.spinner.Tick)
}
