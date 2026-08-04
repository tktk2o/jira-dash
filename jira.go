package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// JiraTime accepts the offset format Jira returns ("+0900"), which
// time.RFC3339 rejects because it lacks the colon.
type JiraTime struct {
	time.Time
}

var jiraTimeLayouts = []string{
	"2006-01-02T15:04:05.000-0700",
	"2006-01-02T15:04:05-0700",
	time.RFC3339,
}

// UnmarshalJSON goes through the string decoder rather than trimming the quotes
// off itself: a JSON null is a valid absent time, and only the decoder can tell
// it from the four-character string "null".
func (t *JiraTime) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		// A null unmarshals into no string at all, which is the absent case; any
		// other shape is a field this type cannot hold.
		if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			return nil
		}
		return err
	}
	if s == "" {
		return nil
	}
	for _, layout := range jiraTimeLayouts {
		if parsed, err := time.Parse(layout, s); err == nil {
			t.Time = parsed
			return nil
		}
	}
	return fmt.Errorf("unrecognised time %q", s)
}

// MarshalJSON writes RFC 3339, not the format Jira sent: what is marshalled is
// the cache, and it is read back by this program alone.
func (t JiraTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Time)
}

// Issue is the subset of `jira search -f json` this dashboard renders. URL
// comes from the CLI, so no site URL has to be configured here.
type Issue struct {
	Key      string   `json:"key"`
	Summary  string   `json:"summary"`
	Type     string   `json:"type"`
	Status   string   `json:"status"`
	Assignee *string  `json:"assignee"`
	Reporter *string  `json:"reporter"`
	Updated  JiraTime `json:"updated"`
	URL      string   `json:"url"`

	Priority string   `json:"priority"`
	Labels   []string `json:"labels"`

	// Story points come back null on anything unestimated, which is most
	// issues, so a pointer keeps "unestimated" apart from a deliberate 0.
	StoryPoints *float64 `json:"story_points"`

	Project struct {
		Key string `json:"key"`
	} `json:"project"`

	// An issue carries every sprint it has ever been in, closed ones included,
	// which is why the state matters as much as the name.
	Sprint []Sprint `json:"sprint"`
}

// Sprint is the part of a sprint a section needs to match on. A board's active
// sprint is renamed every iteration ("Team 0803-0807"), and JQL cannot match a
// name by prefix: the sprint field takes no LIKE operator, and `sprint ~ "Team"`
// was measured returning 2 of that sprint's 15 issues. So the match happens
// here instead.
type Sprint struct {
	// ID is carried so that creating an issue can name the sprint numerically.
	// `jira create -S` takes a name too, but then the CLI has to resolve it, and
	// two boards can hold sprints with the same name.
	ID    int    `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// CurrentSprint is the sprint a new issue should join to land in the same place
// as this one: the active sprint if there is one, otherwise a future sprint,
// which is how a named backlog is modelled. Closed is never it - an issue keeps
// every sprint it has ever been in, so the last entry is not the current one.
func (i Issue) CurrentSprint() (Sprint, bool) {
	var future Sprint
	var haveFuture bool
	for _, s := range i.Sprint {
		switch s.State {
		case "active":
			return s, true
		case "future":
			if !haveFuture {
				future, haveFuture = s, true
			}
		}
	}
	return future, haveFuture
}

// InActiveSprintPrefix reports whether the issue sits in a currently active
// sprint whose name starts with prefix. The state check is the point: a closed
// sprint with the same prefix would otherwise pin an issue to the board
// forever, and a future sprint is a named backlog, not the current iteration.
//
// An empty prefix means the section never asked to be narrowed.
func (i Issue) InActiveSprintPrefix(prefix string) bool {
	if prefix == "" {
		return true
	}
	for _, s := range i.Sprint {
		if s.State == "active" && strings.HasPrefix(s.Name, prefix) {
			return true
		}
	}
	return false
}

// AssigneeName is the name to draw in a column, so an unassigned issue reads as
// a dash rather than as an empty cell that looks like a rendering fault.
func (i Issue) AssigneeName() string {
	if i.Assignee == nil || *i.Assignee == "" {
		return "-"
	}
	return *i.Assignee
}

type searchEnvelope struct {
	Total   int     `json:"total"`
	Results []Issue `json:"results"`
}

// ParseSearchJSON reads what `jira search -f json` prints: a total and the
// results. Only the results are kept; the tab states how many rows it holds,
// which is a fact this program can see for itself.
func ParseSearchJSON(b []byte) ([]Issue, error) {
	var env searchEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	return env.Results, nil
}

// Searcher is the only door to the outside world. Keeping it one interface
// means the whole UI is testable without a network, and that the CLI can be
// swapped for direct REST calls later without touching the model.
//
// Create is the one write in the whole program. It is on this interface rather
// than routed through a user-configured command because the summary has to be
// typed into the dashboard, and a config command cannot ask for input.
type Searcher interface {
	Search(ctx context.Context, jql string, limit int) ([]Issue, error)
	Issue(ctx context.Context, key string) (string, error)
	Comments(ctx context.Context, key string) ([]Comment, error)
	Create(ctx context.Context, req NewIssueRequest) (Issue, error)
}

// Comment is one entry from `jira comment list`. It is a separate call from the
// issue itself - another ~360ms - which is why the preview draws its header and
// body first and folds comments in when they arrive.
type Comment struct {
	ID      string   `json:"id"`
	Author  string   `json:"author"`
	Body    string   `json:"body"`
	Created JiraTime `json:"created"`
}

// ParseCommentsJSON reads `jira comment list`. That subcommand takes no -f flag:
// JSON is the only thing it prints.
func ParseCommentsJSON(b []byte) ([]Comment, error) {
	var env struct {
		Comments []Comment `json:"comments"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	return env.Comments, nil
}

// Comments reads an issue's thread. `comment list` is the subcommand, so the
// key goes last - `jira comment ABC-1 list` would ask about an issue named
// "list".
func (c CLI) Comments(ctx context.Context, key string) ([]Comment, error) {
	out, err := c.run(ctx, "comment", "list", key)
	if err != nil {
		return nil, err
	}
	return ParseCommentsJSON(out)
}

// NewIssueRequest is everything the create prompt collects. Project and sprint
// are not typed by hand: they are read off the row the cursor was on, which is
// what makes a new issue land beside the ones you were looking at.
type NewIssueRequest struct {
	Project  string
	Type     string
	Summary  string
	SprintID int
}

// CreateArgs builds the argv for `jira create`. It is separate from the call so
// the flag assembly can be tested without running anything, and it returns a
// slice - exec.Command takes argv directly, never a shell, so a summary like
// `'; rm -rf ~` is one argument of data.
func CreateArgs(req NewIssueRequest) []string {
	args := []string{"create", "-p", req.Project, "-t", req.Type, "-s", req.Summary}
	// An empty -S would be read as a sprint named "", so a section without a
	// sprint passes no flag at all.
	if req.SprintID != 0 {
		args = append(args, "-S", strconv.Itoa(req.SprintID))
	}
	return append(args, "-f", "json")
}

// CLI runs the `jira` command. Every invocation pays ~360ms of tsx startup
// before any network, which is why results are cached and rendered first.
type CLI struct {
	Bin string
}

// Search runs a section's JQL.
func (c CLI) Search(ctx context.Context, jql string, limit int) ([]Issue, error) {
	out, err := c.run(ctx, "search", "--jql", jql, "-l", strconv.Itoa(limit), "-f", "json")
	if err != nil {
		return nil, err
	}
	return ParseSearchJSON(out)
}

// Create files a new issue. It is the one call in this program that writes.
func (c CLI) Create(ctx context.Context, req NewIssueRequest) (Issue, error) {
	out, err := c.run(ctx, CreateArgs(req)...)
	if err != nil {
		return Issue{}, err
	}
	return ParseCreateJSON(out)
}

// ParseCreateJSON reads what `jira create -f json` prints: {key, id, self,
// url}. A response without a key is treated as a failure - the CLI exits 0 on
// some paths, and reporting "created " with no key would look like success.
func ParseCreateJSON(b []byte) (Issue, error) {
	var created struct {
		Key string `json:"key"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(b, &created); err != nil {
		return Issue{}, err
	}
	if created.Key == "" {
		return Issue{}, errors.New("create returned no issue key")
	}
	return Issue{Key: created.Key, URL: created.URL}, nil
}

// Issue returns the body of the preview: the description, and nothing else.
// The markdown form of `jira get` leads with a bullet list of the type, status,
// project, assignee, reporter and priority - every one of which the preview
// header already states from the search results - so it was printing all of
// them twice.
func (c CLI) Issue(ctx context.Context, key string) (string, error) {
	out, err := c.run(ctx, "get", key, "-f", "json")
	if err != nil {
		return "", err
	}
	return ParseIssueJSON(out)
}

// ParseIssueJSON pulls the description out of `jira get -f json`. Most issues
// have none, and saying so beats an empty pane that reads as a failed load.
func ParseIssueJSON(b []byte) (string, error) {
	var issue struct {
		Description *string `json:"description"`
	}
	if err := json.Unmarshal(b, &issue); err != nil {
		return "", err
	}
	if issue.Description == nil || strings.TrimSpace(*issue.Description) == "" {
		return "*no description*", nil
	}
	return *issue.Description, nil
}

func (c CLI) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// The deadline is checked first because exec cannot report it: a killed
		// process comes back as "signal: killed", which reads like a crash. Wrapped
		// rather than formatted so a caller can still tell the two apart with
		// errors.Is.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("%s %s: %w", c.Bin, args[0], ctxErr)
		}
		if msg := firstLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s %s: %s: %w", c.Bin, args[0], msg, err)
		}
		return nil, fmt.Errorf("%s %s: %w", c.Bin, args[0], err)
	}
	return stdout.Bytes(), nil
}

// firstLine keeps the footer to one line; a stack trace in a status bar is
// noise, and the useful part of a CLI failure is almost always line one. An
// empty result means stderr said nothing, and the caller falls back to the
// exec error.
func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
