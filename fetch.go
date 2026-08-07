package main

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// prefetchCount is how many rows of a freshly-landed section get their
// description warmed in the one BulkIssues call that follows. It is capped
// well under bulkFetchMaxKeys: a section's own Limit can be larger than this,
// and the point is to warm what a user is likely to look at first, not to
// turn every fetch into a second, bigger request.
const prefetchCount = 25

// prefetchCacheCap bounds Model.prefetch so a long session cannot grow it
// without limit. Replacing the map wholesale per section reply (rather than
// merging) would also bound it, but would throw away a still-valid entry from
// section A the moment section B's own prefetch lands - capping the total
// instead keeps every reply's entries around until the map is actually full.
const prefetchCacheCap = 500

// fetchConcurrency bounds how many section fetches run at once. The exec
// startup that used to make parallelism essential is gone, but Jira is a
// shared API on the other end of these calls, and a tab per core is enough -
// the dashboard should not look like a load test.
const fetchConcurrency = 4

// fetchTimeout stops a hung call from leaving a section spinning forever.
const fetchTimeout = 15 * time.Second

// uiTickInterval redraws the dashboard on a plain clock rather than only on
// input or a fetch landing. Its one job is to keep the relative ages in the
// Updated column and the preview header from freezing between keypresses -
// bubbletea only repaints on a message, and without this one an idle terminal
// otherwise sits on the frame from whenever something last happened. Used only
// when defaults.refetchIntervalMinutes is 0: the auto-refetch tick already
// forces exactly this redraw on its own schedule, so a second clock alongside
// it would be pure waste.
const uiTickInterval = time.Minute

// tickMsg drives both auto-refetch (gh-dash's defaults.refetchIntervalMinutes)
// and the plain redraw clock above; refetch tells Update which job this tick
// is for. Only Init ever starts this loop, and the handler below is the only
// place that reschedules it - unlike the spinner, which a keypress can restart
// mid-flight, nothing else here can start a second copy, so there is no
// pileup to tag a generation against.
type tickMsg struct{ refetch bool }

// tickCmd schedules one tick after d. Called both from Init to start the loop
// and from the tickMsg case to keep it going.
func tickCmd(d time.Duration, refetch bool) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{refetch: refetch} })
}

var fetchSem = make(chan struct{}, fetchConcurrency)

type fetchedMsg struct {
	idx    int
	seq    int
	issues []Issue
	at     time.Time
	err    error
}

// prefetchLoadedMsg carries a BulkIssues reply for Model.prefetch. It has no
// error field: a prefetch failure is silent by design (footer noise for a
// background optimization would be wrong, and the per-issue on-demand fetch
// still works as the fallback), so the tea.Cmd swallows the error itself and
// this message simply carries whatever it did get - nil/empty on failure.
type prefetchLoadedMsg struct {
	descriptions map[string]string
}

// nextTick schedules the one clock tick that is currently in flight: a
// refetch tick on defaults.refetchIntervalMinutes when that is nonzero, or
// else the lighter UI-only redraw tick. Read from cfg on every call rather
// than cached on the model, so the mode can never drift from what LoadConfig
// actually validated.
func (m Model) nextTick() tea.Cmd {
	if n := *m.cfg.Defaults.RefetchIntervalMinutes; n > 0 {
		return tickCmd(time.Duration(n)*time.Minute, true)
	}
	return tickCmd(uiTickInterval, false)
}

func fetchSection(s Searcher, idx, seq int, sec Section) tea.Cmd {
	return func() tea.Msg {
		fetchSem <- struct{}{}
		defer func() { <-fetchSem }()

		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		found, err := s.Search(ctx, sec.JQL, sec.Limit)
		return fetchedMsg{idx: idx, seq: seq, issues: found, at: time.Now(), err: err}
	}
}

// prefetchKeys is the first prefetchCount keys of a freshly-landed section -
// what a user is likely to look at first, not every row the section holds.
func prefetchKeys(issues []Issue) []string {
	n := min(prefetchCount, len(issues))
	keys := make([]string, 0, n)
	for _, i := range issues[:n] {
		keys = append(keys, i.Key)
	}
	return keys
}

// prefetchDescriptions warms Model.prefetch for keys in one BulkIssues call,
// run as its own tea.Cmd so the UI is never blocked on it. A failure here is
// swallowed rather than surfaced: this is a background optimization, and the
// per-issue on-demand fetch in detail.go's loadIssue still covers a miss.
func prefetchDescriptions(s Searcher, keys []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()

		descriptions, err := s.BulkIssues(ctx, keys)
		if err != nil {
			return prefetchLoadedMsg{}
		}
		return prefetchLoadedMsg{descriptions: descriptions}
	}
}

// mergePrefetch adds descriptions into m.prefetch, stopping once
// prefetchCacheCap is reached - see that constant's doc comment for why
// merging additively, bounded by a total cap, is the chosen tradeoff.
func (m *Model) mergePrefetch(descriptions map[string]string) {
	if len(descriptions) == 0 {
		return
	}
	if m.prefetch == nil {
		m.prefetch = make(map[string]string, len(descriptions))
	}
	for k, v := range descriptions {
		if len(m.prefetch) >= prefetchCacheCap {
			return
		}
		m.prefetch[k] = v
	}
}

func (m *Model) nextFetch(idx int) int {
	m.sections[idx].fetchSeq++
	return m.sections[idx].fetchSeq
}
