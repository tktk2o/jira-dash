package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// pressRunes feeds each rune through Update as its own tea.KeyMsg, the way a
// real terminal delivers one keystroke at a time - important here because a
// single tea.KeyMsg carrying multiple runes is not how 日本語 input arrives.
func pressRunes(m Model, s string) Model {
	for _, r := range s {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	return m
}

// clearJQLDraft empties the box after e has seeded it with the section's
// current JQL - openJQLEdit always starts from that, by design, so a test
// that wants to type a fresh query from nothing clears it first.
func clearJQLDraft(m Model) Model {
	for len(m.jqlDraft.Value()) > 0 {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		m = next.(Model)
	}
	return m
}

func TestEOpensJQLEditingWithTheCurrentQuery(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	m = press(m, "e")

	if !m.editingJQL {
		t.Fatal("e should open the search box for editing")
	}
	if got := m.jqlDraft.Value(); got != "assignee = currentUser()" {
		t.Errorf("draft = %q, want the active section's current JQL", got)
	}
}

// Runes, including 日本語, must land in the draft rather than being read as
// motions - q above all, since it would otherwise quit the dashboard.
func TestTypingIntoTheJQLBoxDoesNotTriggerTableKeys(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	m = press(m, "e")
	m = clearJQLDraft(m)

	m = pressRunes(m, "project = ABC and status = q j k 日本語")

	if !m.editingJQL {
		t.Error("q typed into the box must not quit editing")
	}
	if got := m.jqlDraft.Value(); got != "project = ABC and status = q j k 日本語" {
		t.Errorf("draft = %q, want every typed rune", got)
	}
}

func TestEnterCommitsAndRefetchesWithTheEditedJQL(t *testing.T) {
	edited := "project = NEW"
	fake := fakeSearcher{issues: map[string][]Issue{
		"assignee = currentUser()": issues("OLD-1"),
		edited:                     issues("NEW-1"),
	}}
	m := settled(newTestModel(t, fake))

	m = press(m, "e")
	m = clearJQLDraft(m)
	m = pressRunes(m, edited)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if m.editingJQL {
		t.Error("enter should close the box")
	}
	if got := m.sections[0].jqlOverride; got != edited {
		t.Errorf("jqlOverride = %q, want %q", got, edited)
	}
	if !m.sections[0].loading {
		t.Error("committing should mark the section loading")
	}
	if cmd == nil {
		t.Fatal("committing should return the refetch command")
	}
	msg := runFetchCmd(t, cmd)
	if len(msg.issues) != 1 || msg.issues[0].Key != "NEW-1" {
		t.Errorf("the fetch that went out did not use the edited JQL: %+v", msg.issues)
	}

	next, _ = m.Update(msg)
	m = next.(Model)
	if len(m.sections[0].issues) != 1 || m.sections[0].issues[0].Key != "NEW-1" {
		t.Errorf("rows did not update from the edited JQL's fetch: %+v", m.sections[0].issues)
	}
}

// runFetchCmd runs a tea.Cmd batch and returns the fetchedMsg inside it - the
// Update loop's own fetchSection is opaque otherwise, and the point of this
// test is to see what JQL it was actually given.
func runFetchCmd(t *testing.T, cmd tea.Cmd) fetchedMsg {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if fm, ok := c().(fetchedMsg); ok {
				return fm
			}
		}
		t.Fatal("no fetchedMsg in the batch")
	}
	if fm, ok := msg.(fetchedMsg); ok {
		return fm
	}
	t.Fatalf("cmd did not produce a fetchedMsg: %T", msg)
	return fetchedMsg{}
}

// The override has to survive both r and the auto-refresh tick, since both
// reuse fetchSection/nextFetch the same way a manual edit's commit does - see
// sectionState.effective.
func TestOverrideSurvivesManualRefreshAndTick(t *testing.T) {
	edited := "project = NEW"
	fake := fakeSearcher{issues: map[string][]Issue{edited: issues("NEW-1")}}
	m := settled(newTestModel(t, fake))
	m.sections[0].jqlOverride = edited
	m.sections[0].cacheKey = SectionKey(edited, 20)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	msg := runFetchCmd(t, cmd)
	if len(msg.issues) != 1 || msg.issues[0].Key != "NEW-1" {
		t.Errorf("r refetched with the wrong JQL: %+v", msg.issues)
	}

	m2 := settled(newTestModel(t, fake))
	m2.sections[0].jqlOverride = edited
	m2.sections[1].jqlOverride = ""
	_, cmd = m2.Update(tickMsg{refetch: true})
	msg = runFetchCmd(t, cmd)
	if len(msg.issues) != 1 || msg.issues[0].Key != "NEW-1" {
		t.Errorf("the refetch tick used the wrong JQL: %+v", msg.issues)
	}
}

func TestEscLeavesTheOverrideUnchanged(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	m.sections[0].jqlOverride = "project = KEEP"

	m = press(m, "e")
	m = pressRunes(m, " and this should be discarded")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.editingJQL {
		t.Error("esc should close the box")
	}
	if got := m.sections[0].jqlOverride; got != "project = KEEP" {
		t.Errorf("jqlOverride = %q, esc must not change it", got)
	}
}

func TestEmptyEnterRevertsToTheConfigJQL(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	m.sections[0].jqlOverride = "project = OVERRIDDEN"

	m = press(m, "e")
	m = clearJQLDraft(m)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if got := m.sections[0].jqlOverride; got != "" {
		t.Errorf("jqlOverride = %q, want cleared", got)
	}
	if cmd == nil {
		t.Fatal("reverting should still refetch")
	}
	msg := runFetchCmd(t, cmd)
	if msg.idx != 0 {
		t.Errorf("refetch idx = %d, want 0", msg.idx)
	}
}

// An override on one tab must not leak into another, and switching tabs shows
// each section's own JQL.
func TestOverridesArePerSection(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	m = press(m, "e")
	m = pressRunes(m, "project = ONLY-SECTION-0")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	if m.sections[1].jqlOverride != "" {
		t.Error("editing section 0 must not touch section 1's override")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	if got := m.sections[m.active].effective().JQL; got != "sprint in openSprints()" {
		t.Errorf("switching tabs should show section 1's own JQL, got %q", got)
	}
}

// Switching tabs mid-edit is defined to cancel the edit first, rather than
// leave a half-typed draft attached to whichever section becomes active.
func TestTabWhileEditingCancelsTheEditFirst(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	m = press(m, "e")
	m = pressRunes(m, "half typed")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)

	if m.editingJQL {
		t.Error("tab should have closed the box")
	}
	if m.active != 1 {
		t.Errorf("active = %d, want tab to still switch sections", m.active)
	}
	if m.sections[0].jqlOverride != "" {
		t.Error("the half-typed draft must not have committed as an override")
	}
}

func TestDivergenceMarkerOnlyWhileOverridden(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	if got := m.sections[0].jqlOverride; got != "" {
		t.Fatalf("precondition: no override, got %q", got)
	}
	if box := renderSearchBox(m, 120); containsMarker(box) {
		t.Errorf("no override: the box should not carry the divergence marker: %q", box)
	}

	m.sections[0].jqlOverride = "project = DIFFERENT"
	if box := renderSearchBox(m, 120); !containsMarker(box) {
		t.Errorf("an override should mark the box: %q", box)
	}
}

func containsMarker(s string) bool {
	for _, r := range s {
		if r == '*' {
			return true
		}
	}
	return false
}

// The box is a fixed searchBoxChrome lines, and layout has to account for
// that rather than the old one-line query line - tableHeight subtracts the
// difference through fixedChromeLines.
func TestLayoutAccountsForTheThreeLineSearchBox(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	if got := searchBoxChrome; got != 3 {
		t.Fatalf("searchBoxChrome = %d, want 3", got)
	}
	// tableHeight + fixedChromeLines + bottomPreviewHeight (0 for a right
	// preview) must not exceed the terminal, and must leave room once the
	// fixed chrome - which now includes the 3-line box instead of 1 line -
	// is taken out.
	want := max(1, m.height-fixedChromeLines-m.chromeLines()-m.bottomPreviewHeight())
	if got := m.tableHeight(); got != want {
		t.Errorf("tableHeight() = %d, want %d", got, want)
	}
}
