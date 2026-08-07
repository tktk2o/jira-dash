package main

import (
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"
	"time"
)

func TestModelSeedsFromCacheBeforeAnyFetch(t *testing.T) {
	cache := NewCache(t.TempDir())
	key := SectionKey("assignee = currentUser()", 20)
	at := time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC)
	if err := cache.WriteSection(key, issues("ABC-1"), at); err != nil {
		t.Fatal(err)
	}

	m := NewModel(testConfig(), fakeSearcher{}, cache, fixedNow())

	if len(m.sections[0].issues) != 1 {
		t.Fatalf("want the cached row rendered immediately, got %d", len(m.sections[0].issues))
	}
	if !m.sections[0].fetchedAt.Equal(at) {
		t.Error("the cached timestamp should drive the age shown in the footer")
	}
}

func TestFetchedMsgReplacesRows(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})

	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "ABC-2"), at: time.Now()})
	m = next.(Model)

	if len(m.sections[0].issues) != 2 {
		t.Errorf("got %d issues, want 2", len(m.sections[0].issues))
	}
	if m.sections[0].loading {
		t.Error("loading should be cleared once results land")
	}
}

func TestOlderSectionFetchCannotOverwriteANewerResult(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	m.sections[0].fetchSeq = 2
	next, _ := m.Update(fetchedMsg{idx: 0, seq: 2, issues: issues("NEW-1"), at: time.Now()})
	m = next.(Model)
	next, _ = m.Update(fetchedMsg{idx: 0, seq: 1, issues: issues("OLD-1"), at: time.Now().Add(time.Second)})
	m = next.(Model)
	if got := m.sections[0].issues[0].Key; got != "NEW-1" {
		t.Fatalf("stale fetch replaced the latest rows: got %s", got)
	}
}

// A failed refresh must not blank the dashboard: stale rows beat no rows when
// Jira is down or you are off the VPN.
func TestFetchedMsgWithErrorKeepsStaleRows(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: time.Now()})
	m = next.(Model)

	next, _ = m.Update(fetchedMsg{idx: 0, err: errors.New("boom")})
	m = next.(Model)

	if len(m.sections[0].issues) != 1 {
		t.Error("rows should survive a failed refresh")
	}
	if m.sections[0].err == nil {
		t.Error("the error should be recorded for the footer")
	}
}

// A section landing fires a bulk prefetch for its first rows, as its own
// tea.Cmd rather than blocking the fetchedMsg case itself.
func TestFetchedMsgFiresBulkPrefetch(t *testing.T) {
	var calledWith [][]string
	fake := fakeSearcher{bulkCalledWith: &calledWith, bulkIssues: map[string]string{"ABC-1": "# ABC-1 body"}}
	m := newTestModel(t, fake)

	_, cmd := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1", "ABC-2"), at: time.Now()})
	if cmd == nil {
		t.Fatal("a successful fetch should schedule the prefetch (and the selection-changed) commands")
	}

	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("got %T, want a batch", cmd())
	}
	var sawPrefetch bool
	for _, c := range batch {
		if msg, ok := c().(prefetchLoadedMsg); ok {
			sawPrefetch = true
			if msg.descriptions["ABC-1"] != "# ABC-1 body" {
				t.Errorf("descriptions = %v", msg.descriptions)
			}
		}
	}
	if !sawPrefetch {
		t.Fatal("expected a prefetchLoadedMsg in the batch")
	}
	if len(calledWith) != 1 || len(calledWith[0]) != 2 {
		t.Fatalf("BulkIssues called with %v, want the section's two keys", calledWith)
	}
}

// A failed fetch must not warm the prefetch cache with garbage - and must not
// crash, since msg.issues is empty on an error reply.
func TestFetchedMsgWithErrorSkipsPrefetch(t *testing.T) {
	var calledWith [][]string
	fake := fakeSearcher{bulkCalledWith: &calledWith}
	m := newTestModel(t, fake)

	_, cmd := m.Update(fetchedMsg{idx: 0, err: errors.New("boom")})
	if cmd != nil {
		if _, ok := cmd().(tea.BatchMsg); ok {
			t.Error("an errored fetch should not schedule a prefetch")
		}
	}
	if len(calledWith) != 0 {
		t.Error("BulkIssues should not be called for a failed fetch")
	}
}

// A prefetchLoadedMsg merges into m.prefetch rather than replacing it, and a
// failed prefetch (nil descriptions) leaves it untouched - both silently, with
// no footer status set either way.
func TestPrefetchLoadedMsgMergesIntoTheCache(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(prefetchLoadedMsg{descriptions: map[string]string{"ABC-1": "one"}})
	m = next.(Model)
	next, _ = m.Update(prefetchLoadedMsg{descriptions: map[string]string{"ABC-2": "two"}})
	m = next.(Model)

	if m.prefetch["ABC-1"] != "one" || m.prefetch["ABC-2"] != "two" {
		t.Errorf("prefetch = %v, want both entries merged", m.prefetch)
	}

	next, _ = m.Update(prefetchLoadedMsg{})
	m = next.(Model)
	if m.prefetch["ABC-1"] != "one" {
		t.Error("a failed prefetch reply must not disturb what is already cached")
	}
	if m.status != "" {
		t.Error("a prefetch failure must be silent - no footer status")
	}
}

// mergePrefetch stops once the cache holds prefetchCacheCap entries, so a
// long session cannot grow it without limit.
func TestMergePrefetchIsBoundedByPrefetchCacheCap(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	full := make(map[string]string, prefetchCacheCap)
	for i := 0; i < prefetchCacheCap; i++ {
		full[fmt.Sprintf("ABC-%d", i)] = "body"
	}
	m.mergePrefetch(full)
	if len(m.prefetch) != prefetchCacheCap {
		t.Fatalf("prefetch has %d entries, want exactly the cap %d", len(m.prefetch), prefetchCacheCap)
	}

	m.mergePrefetch(map[string]string{"OVERFLOW-1": "body"})
	if len(m.prefetch) != prefetchCacheCap {
		t.Errorf("prefetch grew to %d past the cap", len(m.prefetch))
	}
	if _, ok := m.prefetch["OVERFLOW-1"]; ok {
		t.Error("an entry past the cap should not be added")
	}
}

// A refetch tick that lands while the client reports NearRateLimit skips the
// section fetches but still reschedules the next tick, so the relative-time
// redraw keeps happening. It also sets a subtle footer note.
func TestRefetchTickSkipsFetchesWhenNearRateLimit(t *testing.T) {
	fake := fakeSearcher{nearRateLimit: true}
	m := newTestModel(t, fake)
	for i := range m.sections {
		next, _ := m.Update(fetchedMsg{idx: i, issues: issues("ABC-1"), at: time.Now()})
		m = next.(Model)
	}

	next, cmd := m.Update(tickMsg{refetch: true})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("the tick should still reschedule itself")
	}
	// cmd is a tea.Tick that sleeps for the tick interval before returning its
	// message, so it is deliberately not invoked here - a non-nil command from
	// nextTick is enough to know the loop keeps going.
	for _, s := range m.sections {
		if s.loading {
			t.Error("a section must not be marked loading when the fetch was skipped")
		}
	}
	if !strings.Contains(m.status, "rate limit") {
		t.Errorf("status = %q, want a rate-limit note", m.status)
	}
}

// Once NearRateLimit clears, the next tick fetches normally again and the
// note is cleared.
func TestRefetchTickFetchesAgainOnceRateLimitClears(t *testing.T) {
	fake := &fakeSearcher{nearRateLimit: true}
	m := newTestModel(t, *fake)

	next, _ := m.Update(tickMsg{refetch: true})
	m = next.(Model)

	fake.nearRateLimit = false
	m.searcher = *fake
	next, cmd := m.Update(tickMsg{refetch: true})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("expected fetch commands")
	}
	if !m.sections[0].loading {
		t.Error("a section should be marked loading once the tick actually fetches")
	}
	if m.status != "" {
		t.Error("the rate-limit note should clear once fetching resumes")
	}
}

// A refetch tick has to behave like r pressed on every section at once: every
// section goes loading, and the reply is the same fetchedMsg r produces, so
// applyFetched's own seq guard is what protects a tick's reply exactly as it
// protects r's.
func TestRefetchTickRefreshesEverySection(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))

	next, cmd := m.Update(tickMsg{refetch: true})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("a refetch tick should return commands")
	}
	for i, s := range m.sections {
		if !s.loading {
			t.Errorf("section %d loading = false, want true after a refetch tick", i)
		}
	}
}

// Unlike r, the tick must not blank the rows: a background refresh firing every
// few minutes should not flash the screen empty while the new rows are still
// in flight.
func TestRefetchTickKeepsStaleRowsWhileLoading(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	next, _ := m.Update(fetchedMsg{idx: 0, issues: issues("ABC-1"), at: fixedNow()()})
	m = settled(next.(Model))

	next, _ = m.Update(tickMsg{refetch: true})
	m = next.(Model)
	if len(m.sections[0].issues) != 1 {
		t.Errorf("issues = %v, want the stale row kept while the refetch is in flight", m.sections[0].issues)
	}
}

// A tick's reply is a plain fetchedMsg, so a stale one - superseded by a second
// tick or a manual r before the first lands - must be discarded the same way
// any other stale fetchedMsg is: applyFetched compares seq, and nextFetch is
// what advances it.
func TestStaleRefetchTickReplyIsDiscarded(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))
	staleSeq := m.sections[0].fetchSeq

	next, _ := m.Update(tickMsg{refetch: true})
	m = next.(Model)
	current := m.sections[0].fetchSeq
	if current == staleSeq {
		t.Fatal("a refetch tick should advance fetchSeq")
	}

	next, _ = m.Update(fetchedMsg{idx: 0, seq: staleSeq, issues: issues("STALE-1"), at: fixedNow()()})
	m = next.(Model)
	for _, i := range m.sections[0].issues {
		if i.Key == "STALE-1" {
			t.Error("a reply carrying a superseded seq must not land")
		}
	}
}

// A non-refetch tick exists only to redraw the clock; it must reschedule
// itself but must not touch any section.
func TestUiTickDoesNotRefetch(t *testing.T) {
	m := settled(newTestModel(t, fakeSearcher{}))

	next, cmd := m.Update(tickMsg{refetch: false})
	m = next.(Model)
	if cmd == nil {
		t.Error("a ui tick should reschedule itself")
	}
	for i, s := range m.sections {
		if s.loading {
			t.Errorf("section %d loading = true, want a ui tick to leave sections alone", i)
		}
	}
}

// Init's choice of which clock to start has to follow the config: an interval
// schedules a refetch tick, and 0 falls back to the ui-only clock so ages
// still redraw with auto-refetch off.
func TestNextTickFollowsRefetchInterval(t *testing.T) {
	m := newTestModel(t, fakeSearcher{})
	if cmd := m.nextTick(); cmd == nil {
		t.Fatal("nextTick should always return a command")
	}

	minutes := 5
	m.cfg.Defaults.RefetchIntervalMinutes = &minutes
	// tea.Cmd is opaque, so this only asserts nextTick still returns something
	// when a positive interval is configured - the refetch-vs-ui branch is
	// exercised through tickMsg.refetch above.
	if cmd := m.nextTick(); cmd == nil {
		t.Fatal("nextTick should return a command for a positive interval too")
	}
}
