package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseSearchJSON(t *testing.T) {
	raw, err := os.ReadFile("testdata/search.json")
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ParseSearchJSON(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}

	first := issues[0]
	if first.Key != "ABC-1234" {
		t.Errorf("key = %q", first.Key)
	}
	if first.Type != "Bug" {
		t.Errorf("type = %q", first.Type)
	}
	if first.Status != "In Progress" {
		t.Errorf("status = %q", first.Status)
	}
	if first.Project.Key != "ABC" {
		t.Errorf("project key = %q", first.Project.Key)
	}
	if first.URL == "" {
		t.Error("url should come straight from the CLI, not be assembled")
	}
	if first.AssigneeName() != "Alice Example" {
		t.Errorf("assignee = %q", first.AssigneeName())
	}
	if issues[1].AssigneeName() != "-" {
		t.Errorf("a null assignee should render as -, got %q", issues[1].AssigneeName())
	}
}

// Jira returns "+0900" with no colon, which time.RFC3339 rejects.
func TestJiraTimeAcceptsJiraOffset(t *testing.T) {
	issues, err := ParseSearchJSON([]byte(
		`{"results":[{"key":"ABC-1","updated":"2026-08-04T10:15:00.000+0900"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := time.Date(2026, 8, 4, 10, 15, 0, 0, time.FixedZone("", 9*60*60))
	if !issues[0].Updated.Equal(want) {
		t.Errorf("updated = %v, want %v", issues[0].Updated.Time, want)
	}
}

func TestJiraTimeAcceptsNull(t *testing.T) {
	issues, err := ParseSearchJSON([]byte(`{"results":[{"key":"ABC-1","updated":null}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !issues[0].Updated.IsZero() {
		t.Error("a null updated should stay the zero time")
	}
}

func TestParseSearchJSONRejectsGarbage(t *testing.T) {
	if _, err := ParseSearchJSON([]byte("not json")); err == nil {
		t.Fatal("want an error")
	}
}

func TestCLISearchParsesStubOutput(t *testing.T) {
	cli := CLI{Bin: "./testdata/fake-jira-ok.sh"}

	issues, err := cli.Search(context.Background(), "assignee = currentUser()", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}
}

func TestCLIIssueReturnsMarkdown(t *testing.T) {
	cli := CLI{Bin: "./testdata/fake-jira-ok.sh"}

	md, err := cli.Issue(context.Background(), "ABC-1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(md, "ABC-1234") {
		t.Errorf("markdown should mention the key, got %q", md)
	}
}

// A failed call must surface one readable line, not a wall of stderr.
func TestCLISearchReportsFirstStderrLine(t *testing.T) {
	cli := CLI{Bin: "./testdata/fake-jira-fail.sh"}

	_, err := cli.Search(context.Background(), "project = ABC", 20)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("error should carry the first stderr line, got: %v", err)
	}
	if strings.Contains(err.Error(), "second line") {
		t.Errorf("error should stop at the first line, got: %v", err)
	}
}

func TestCLISearchIsCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (CLI{Bin: "./testdata/fake-jira-ok.sh"}).Search(ctx, "project = ABC", 1); err == nil {
		t.Fatal("a cancelled context must fail the call")
	}
}

// CLI must satisfy Searcher, so the model can take a fake in tests.
var _ Searcher = CLI{}

// A rotating sprint name ("Team 0803-0807") cannot be matched in JQL: the
// sprint field takes no LIKE operator, and `sprint ~ "Team"` was measured
// returning 2 of the sprint's 15 issues. The prefix match therefore happens
// here, which needs the sprint names and states out of the search JSON.
func TestParseSearchJSONKeepsSprintNamesAndStates(t *testing.T) {
	issues, err := ParseSearchJSON([]byte(`{"total":1,"results":[{
		"key":"ABC-1",
		"sprint":[
			{"name":"Team 0721-0724","state":"closed"},
			{"name":"Team 0803-0807","state":"active"}
		]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := issues[0].Sprint
	if len(got) != 2 {
		t.Fatalf("sprints = %d, want 2", len(got))
	}
	if got[1].Name != "Team 0803-0807" || got[1].State != "active" {
		t.Errorf("second sprint = %+v", got[1])
	}
}

// An issue with no sprint must parse, not fail: most projects do not use them.
func TestParseSearchJSONAcceptsAMissingSprint(t *testing.T) {
	issues, err := ParseSearchJSON([]byte(`{"total":1,"results":[{"key":"ABC-1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues[0].Sprint) != 0 {
		t.Errorf("sprints = %+v, want none", issues[0].Sprint)
	}
}

func TestInActiveSprintPrefix(t *testing.T) {
	inSprint := Issue{Sprint: []Sprint{
		{Name: "Team 0727-0731", State: "closed"},
		{Name: "Team 0803-0807", State: "active"},
	}}
	otherTeam := Issue{Sprint: []Sprint{{Name: "Other 0803-0807", State: "active"}}}
	// The prefix must not match a sprint the team has already finished, or a
	// closed sprint would keep an issue on the board forever.
	closedOnly := Issue{Sprint: []Sprint{{Name: "Team 0727-0731", State: "closed"}}}
	// A parked issue sits in a future sprint used as a named backlog; it is not
	// in the active sprint.
	parked := Issue{Sprint: []Sprint{{Name: "Team backlog", State: "future"}}}

	for name, tc := range map[string]struct {
		issue Issue
		want  bool
	}{
		"active sprint with the prefix": {inSprint, true},
		"another team's sprint":         {otherTeam, false},
		"only a closed sprint":          {closedOnly, false},
		"a future backlog sprint":       {parked, false},
		"no sprint at all":              {Issue{}, false},
	} {
		if got := tc.issue.InActiveSprintPrefix("Team"); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// An empty prefix means the section did not ask for one, so nothing is dropped.
func TestInActiveSprintPrefixIsOffWhenEmpty(t *testing.T) {
	if !(Issue{}).InActiveSprintPrefix("") {
		t.Error("an empty prefix should keep every issue")
	}
}

// Creating an issue "in the same place" means the sprint of the row you are
// standing on. Closed sprints are skipped: an issue keeps every sprint it has
// ever been in, so the most recent one is not necessarily the current one.
func TestCurrentSprintPrefersActiveThenFuture(t *testing.T) {
	active := Sprint{ID: 3, Name: "Team 0803-0807", State: "active"}
	future := Sprint{ID: 4, Name: "Team backlog", State: "future"}
	closed := Sprint{ID: 1, Name: "Team 0727-0731", State: "closed"}

	for name, tc := range map[string]struct {
		in   []Sprint
		want int
		ok   bool
	}{
		"active wins over a backlog": {[]Sprint{closed, future, active}, 3, true},
		"a backlog when none active": {[]Sprint{closed, future}, 4, true},
		"closed only is no sprint":   {[]Sprint{closed}, 0, false},
		"no sprint at all":           {nil, 0, false},
	} {
		got, ok := Issue{Sprint: tc.in}.CurrentSprint()
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", name, ok, tc.ok)
			continue
		}
		if got.ID != tc.want {
			t.Errorf("%s: sprint id = %d, want %d", name, got.ID, tc.want)
		}
	}
}

// The sprint id comes straight from the search results, so create never pays
// the CLI's name-to-id lookup.
func TestParseSearchJSONKeepsTheSprintID(t *testing.T) {
	issues, err := ParseSearchJSON([]byte(
		`{"results":[{"key":"ABC-1","sprint":[{"id":13126,"name":"Team 0803-0807","state":"active"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if issues[0].Sprint[0].ID != 13126 {
		t.Errorf("sprint id = %d, want 13126", issues[0].Sprint[0].ID)
	}
}

func TestCreateArgs(t *testing.T) {
	got := CreateArgs(NewIssueRequest{
		Project: "ABC", Type: "Task", Summary: "a title", SprintID: 13126,
	})
	want := []string{"create", "-p", "ABC", "-t", "Task", "-s", "a title", "-S", "13126", "-f", "json"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

// A section with no sprint (say, "assigned to me") must not pass -S at all:
// the CLI would take an empty value as a sprint named "".
func TestCreateArgsOmitsAnAbsentSprint(t *testing.T) {
	got := CreateArgs(NewIssueRequest{Project: "ABC", Type: "Task", Summary: "a title"})
	for _, a := range got {
		if a == "-S" {
			t.Fatalf("args should carry no sprint flag: %v", got)
		}
	}
}

// The args are handed to exec.Command as a slice, never to a shell, so a
// summary full of shell syntax is data. This pins that: the summary must
// arrive as exactly one argument, unquoted and unescaped.
func TestCreateArgsPassesAHostileSummaryVerbatim(t *testing.T) {
	summary := `'; rm -rf ~ #`
	got := CreateArgs(NewIssueRequest{Project: "ABC", Type: "Task", Summary: summary})
	found := 0
	for _, a := range got {
		if a == summary {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("the summary should appear once, verbatim: %v", got)
	}
}

// `jira create -f json` prints {key, id, self, url}. Only the key and the URL
// are of any use here: the key goes in the footer, the URL makes the new issue
// openable before the section has refetched.
func TestParseCreateJSON(t *testing.T) {
	got, err := ParseCreateJSON([]byte(`{
		"key": "ABC-1234",
		"id": "10001",
		"self": "https://example.atlassian.net/rest/api/3/issue/10001",
		"url": "https://example.atlassian.net/browse/ABC-1234"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "ABC-1234" {
		t.Errorf("key = %q", got.Key)
	}
	if got.URL != "https://example.atlassian.net/browse/ABC-1234" {
		t.Errorf("url = %q", got.URL)
	}
}

// A create that prints something unparseable must be an error, not a silent
// success reporting an empty key.
func TestParseCreateJSONRejectsAKeylessResponse(t *testing.T) {
	if _, err := ParseCreateJSON([]byte(`{"id":"10001"}`)); err == nil {
		t.Error("a response with no key should be an error")
	}
}
