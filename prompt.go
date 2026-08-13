package main

import (
	"context"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// choicesLoadedMsg carries the reply from a choicesFrom: transitions/assignees
// fetch. It carries both the issue the fetch was for and the keybinding it was
// for, and the seq the request was issued under, because all three can go
// stale before the reply lands: the cursor can move to another issue, another
// press of a different choices key can start a second fetch, and only comparing
// seq (the debounceMsg pattern) would still open a picker for an issue the
// cursor has since left if the key were pressed twice in a row on two rows.
type choicesLoadedMsg struct {
	seq   int
	key   string
	kbKey string
	list  []Choice
	err   error
}

// openCreatePrompt starts the create flow for one issue type. It refuses on an
// empty section: the project and sprint are inherited from the row under the
// cursor, so with no row there is nothing to inherit and `jira create` would be
// handed an empty -p.
// openAskPrompt starts the ask flow for a keybinding that declared prompt: true.
// The instruction is typed here rather than baked into the config command,
// because what you want done with an issue is different every time - a fixed
// prompt is a different feature, and the keys without prompt: true still are one.
func (m Model) openAskPrompt(key string) (tea.Model, tea.Cmd) {
	if _, ok := m.sections[m.active].selected(); !ok {
		m.status = "nothing to ask about: this section has no rows"
		return m, nil
	}
	m.asking = true
	m.askKey = key
	m.openPrompt(askInputHeight)
	return m, nil
}

func (m Model) handleAskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlD:
		// An empty instruction would hand over the issue and nothing to do with
		// it, which is what the keys without prompt: true are for. The box stays
		// open rather than launching something pointless.
		instruction := strings.TrimSpace(m.prompt.Value())
		if instruction == "" {
			return m, nil
		}
		key := m.askKey
		m.closePrompt()
		return m.runUserKeybindingWith(key, instruction)

	case tea.KeyEsc, tea.KeyCtrlC:
		m.closePrompt()
		return m, nil
	}
	return m.updatePrompt(msg)
}

// openChoosePrompt opens the picker for a keybinding with choices, or starts
// loading one for a keybinding with choicesFrom: transitions/assignees.
//
// The two live sources do not open the box here: they hand back a command and
// let choicesLoadedMsg open it once the reply is in. The plan for this is
// explicit - an empty picker reads as a broken key, so the box has to wait for
// candidates rather than show them arriving.
func (m Model) openChoosePrompt(kb Keybinding) (tea.Model, tea.Cmd) {
	issue, ok := m.sections[m.active].selected()
	if !ok {
		m.status = "nothing to change: this section has no rows"
		return m, nil
	}

	if kb.ChoicesFrom == choicesFromTransitions || kb.ChoicesFrom == choicesFromAssignees {
		m.chooseSeq++
		m.loadingChoices = true
		m.status = "loading " + kb.ChoicesFrom + "..."
		return m, tea.Batch(
			fetchChoices(m.searcher, m.chooseSeq, kb.Key, kb.ChoicesFrom, issue.Key),
			m.spinner.Tick,
		)
	}

	// The remaining two sources need no call: choices is written in the config,
	// and statuses is derived from rows jhd already holds. The list is resolved
	// here, once, so that an empty one is reported as the config problem or the
	// empty tab it is, rather than as a box with nothing in it.
	list := kb.Choices
	if kb.ChoicesFrom == choicesFromStatuses {
		list = m.sectionStatuses()
	}
	if len(list) == 0 {
		m.status = kb.Key + ": no choices to pick from"
		return m, nil
	}
	m.choosing = true
	m.chooseKey = kb.Key
	m.chooseList = list
	m.chooseCursor = 0
	m.chooseFilter = ""
	return m, nil
}

// visibleChoices is chooseList narrowed and ranked by chooseFilter - the
// picker's equivalent of sectionState.visible(). Empty input returns the full
// list in its original order, same as the table's own filter.
func (m Model) visibleChoices() []Choice {
	if m.chooseFilter == "" {
		return m.chooseList
	}
	type ranked struct {
		choice Choice
		rank   int64
	}
	matches := make([]ranked, 0, len(m.chooseList))
	for _, c := range m.chooseList {
		if rank, ok := fuzzyMatch(c.Name(), m.chooseFilter); ok {
			matches = append(matches, ranked{c, rank})
		}
	}
	// Stable so choices that tie on rank keep chooseList's own order, the same
	// guarantee an untyped filter gives - without it every keystroke could
	// reshuffle entries that are equally good matches, which reads as the list
	// jittering rather than narrowing.
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].rank < matches[j].rank })
	out := make([]Choice, len(matches))
	for i, r := range matches {
		out[i] = r.choice
	}
	return out
}

// fuzzyMatch reports whether every rune of query appears in label in order
// (not necessarily adjacent), case-insensitive, and if so a rank where lower
// sorts first.
//
// Rank orders by the span from first to last matched rune, then by start
// position. This distinguishes a compact later match from a scattered early
// one; ties fall back to chooseList's order via visibleChoices' stable sort.
func fuzzyMatch(label, query string) (rank int64, ok bool) {
	if query == "" {
		return 0, true
	}
	l := []rune(strings.ToLower(label))
	q := []rune(strings.ToLower(query))
	qi, start, end := 0, -1, -1
	for i, r := range l {
		if r == q[qi] {
			if start == -1 {
				start = i
			}
			end = i
			qi++
			if qi == len(q) {
				break
			}
		}
	}
	if qi != len(q) {
		return 0, false
	}
	// Compact matches sort first, then earlier matches. Packing the two values
	// into one integer keeps visibleChoices' stable sort simple.
	return int64(end-start)<<32 | int64(start), true
}

// fetchChoices calls the API behind choicesFrom: transitions/assignees. seq and
// kbKey travel with the reply so Update can tell a stale answer from a current
// one without having to look the keybinding back up.
func fetchChoices(s Searcher, seq int, kbKey, source, issueKey string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		var list []Choice
		var err error
		switch source {
		case choicesFromTransitions:
			var transitions []Transition
			transitions, err = s.Transitions(ctx, issueKey)
			for _, t := range transitions {
				// Label-less: jira edit -S takes the transition's name, so the name is
				// both what the picker shows and what it sends, same as a status
				// under choicesFrom: statuses.
				list = append(list, Choice{Value: t.Name})
			}
		case choicesFromAssignees:
			var users []User
			users, err = s.AssignableUsers(ctx, issueKey, "")
			for _, u := range users {
				list = append(list, Choice{Label: u.DisplayName, Value: u.AccountID})
			}
		}
		return choicesLoadedMsg{seq: seq, key: issueKey, kbKey: kbKey, list: list, err: err}
	}
}

// sectionStatuses is the choicesFrom: statuses list - the status names the rows
// in view actually carry, in the order they first appear so the list is stable
// between openings rather than reordering itself as the cursor moves.
//
// The limit is real and worth knowing: a status no current row has is not
// offered, so on a tab where nothing is finished there is no "Done" to pick.
func (m Model) sectionStatuses() []Choice {
	seen := map[string]bool{}
	var out []Choice
	for _, i := range m.sections[m.active].visible() {
		if i.Status == "" || seen[i.Status] {
			continue
		}
		seen[i.Status] = true
		out = append(out, Choice{Value: i.Status})
	}
	return out
}

// handleChooseKey routes keys inside the picker. Movement is on the arrow
// keys only, deliberately not j/k: unlike the table's `/` filter, the picker
// has no separate mode to enter before typing takes over, so if a letter
// could mean either "move" or "narrow" a label like "Jane" would send the
// cursor flying every time its first character matched a motion key. gh-dash
// resolves the same clash the same way for its own filter-while-typing input -
// letters always fall through to the buffer, and the arrow keys keep
// movement reachable without reintroducing the ambiguity.
func (m Model) handleChooseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyDown:
		m.chooseCursor = min(m.chooseCursor+1, len(m.visibleChoices())-1)
		return m, nil
	case tea.KeyUp:
		m.chooseCursor = max(m.chooseCursor-1, 0)
		return m, nil
	case tea.KeyEnter:
		list := m.visibleChoices()
		if len(list) == 0 || m.chooseCursor < 0 || m.chooseCursor >= len(list) {
			return m, nil
		}
		key, value := m.chooseKey, list[m.chooseCursor].Value
		m.closeChoosePrompt()
		return m.runUserKeybindingWithChoice(key, value)
	case tea.KeyEsc, tea.KeyCtrlC:
		m.closeChoosePrompt()
		return m, nil
	case tea.KeyBackspace:
		if m.chooseFilter != "" {
			r := []rune(m.chooseFilter)
			m.chooseFilter = string(r[:len(r)-1])
		}
		m.clampChooseCursor()
		return m, nil
	// Space arrives as its own key type, not as a rune - the same reason
	// handleFilterKey has this case for the table's filter.
	case tea.KeySpace:
		m.chooseFilter += " "
		m.clampChooseCursor()
		return m, nil
	case tea.KeyRunes:
		m.chooseFilter += string(msg.Runes)
		m.clampChooseCursor()
		return m, nil
	}
	return m, nil
}

// clampChooseCursor keeps the cursor inside the narrowed list every time
// chooseFilter changes. Without this, filtering a list down while the cursor
// sat near its old end left it pointing past the new, shorter slice - an
// index that enter would then use to index into it.
func (m *Model) clampChooseCursor() {
	if n := len(m.visibleChoices()); m.chooseCursor >= n {
		m.chooseCursor = max(0, n-1)
	}
}

func (m *Model) closeChoosePrompt() {
	m.choosing = false
	m.chooseKey = ""
	m.chooseList = nil
	m.chooseCursor = 0
	m.chooseFilter = ""
}

// AskPrompt assembles what the configured command receives as {{.Prompt}}: the
// issue, then the instruction. The description is included because the preview
// already fetched it - without it the receiving end has to spend another
// 0.5-1.2s Jira REST round trip to learn what the issue says, and it may not
// have credentials at all. An empty body is simply left out rather than
// announced.
func AskPrompt(i Issue, body, instruction string) string {
	parts := []string{i.Key + " " + i.Summary}
	if b := strings.TrimSpace(body); b != "" && b != "*no description*" {
		parts = append(parts, b)
	}
	return strings.Join(append(parts, "---", instruction), "\n\n")
}

func (m Model) openCreatePrompt(ck CreateKey) (tea.Model, tea.Cmd) {
	if _, ok := m.sections[m.active].selected(); !ok {
		m.status = "nothing to create from: this section has no rows"
		return m, nil
	}
	m.creating = true
	m.createType = ck.Type
	m.createParent = ck.Parent
	m.openPrompt(createInputHeight)
	return m, nil
}

// handleCreateKey routes keys to the box's input, keeping only the two that
// leave it. Ctrl+d rather than enter submits, because enter now inserts a
// newline inside the box - the box states both on the line above its border.
func (m Model) handleCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlD:
		// Jira requires a summary to create an issue, so an empty one is refused
		// here rather than coming back as an API failure after a round trip. The
		// box stays open.
		summary := strings.TrimSpace(m.prompt.Value())
		if summary == "" {
			return m, nil
		}
		row, ok := m.sections[m.active].selected()
		if !ok {
			m.closePrompt()
			m.status = "the row this create was based on is gone"
			return m, nil
		}
		req := NewIssueRequest{
			Project: row.Project.Key,
			Type:    m.createType,
			Summary: summary,
		}
		if m.createParent {
			req.Parent = row.Key
			// Deliberately no Sprint here: a subtask inherits its parent's
			// sprint, and many Jira configurations reject an explicit sprint
			// on a subtask create outright. Setting it would either be
			// ignored or fail the whole create depending on the site.
		} else if sprint, ok := row.CurrentSprint(); ok {
			req.Sprint = sprint.Name
		}

		m.closePrompt()
		m.status = "creating " + req.Type + "..."
		return m, createIssue(m.searcher, m.active, req)

	case tea.KeyEsc, tea.KeyCtrlC:
		m.closePrompt()
		return m, nil
	}
	return m.updatePrompt(msg)
}

// updatePrompt hands a key to the box's input. Everything the box does not claim
// belongs to the textarea - which is what makes the cursor keys, word deletion
// and the rest work without this file knowing about any of them.
func (m Model) updatePrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(msg)
	return m, cmd
}

// closePrompt is a method rather than the same three assignments at each exit.
func (m *Model) closePrompt() {
	m.creating, m.createParent, m.asking, m.askKey = false, false, false, ""
	m.prompt.Reset()
	m.prompt.Blur()
}

// openPrompt sizes the box's input and focuses it. The height is the difference
// between the two boxes: a Jira summary is one line, an instruction is not.
func (m *Model) openPrompt(height int) {
	m.prompt.Reset()
	m.prompt.SetHeight(height)
	m.prompt.Focus()
	// The box is as wide as the table, less its border, its padding and the
	// column the line numbers take.
	m.prompt.SetWidth(max(1, m.tableWidth()-6))
}

// createdMsg reports the outcome. idx is the section the create was launched
// from, so the refresh lands there even if the cursor has moved on since.
type createdMsg struct {
	issue Issue
	idx   int
	err   error
}

func createIssue(s Searcher, idx int, req NewIssueRequest) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		created, err := s.Create(ctx, req)
		return createdMsg{issue: created, idx: idx, err: err}
	}
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.filtering = false
		m.sections[m.active].filter = m.filterDraft
		m.sections[m.active].cursor = 0
		return m, m.selectionChanged()
	case tea.KeyEsc:
		m.filtering = false
		m.filterDraft = ""
		return m, nil
	case tea.KeyBackspace:
		if m.filterDraft != "" {
			r := []rune(m.filterDraft)
			m.filterDraft = string(r[:len(r)-1])
		}
		return m, nil
	// Space arrives as its own key type, not as a rune, so without it here a
	// filter could never hold more than one word - and a summary filter is
	// exactly where you want two.
	case tea.KeyRunes:
		m.filterDraft += string(msg.Runes)
		return m, nil
	case tea.KeySpace:
		m.filterDraft += " "
		return m, nil
	}
	return m, nil
}
