package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// Issue is the subset of `jira search -f json` this dashboard renders. URL is
// built from Credentials.SiteURL by the client (see read.go), so no site URL
// has to be configured here.
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

// Comment is one entry from `jira comment list`. It is a separate call from the
// issue itself - another 0.5-1.2s Jira REST round trip - which is why the
// preview draws its header and body first and folds comments in when they
// arrive.
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
