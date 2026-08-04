package main

import (
	"bytes"
	"context"
	"encoding/json"
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

func (t *JiraTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
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
	Updated  JiraTime `json:"updated"`
	URL      string   `json:"url"`
	Project  struct {
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
	Name  string `json:"name"`
	State string `json:"state"`
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
type Searcher interface {
	Search(ctx context.Context, jql string, limit int) ([]Issue, error)
	Issue(ctx context.Context, key string) (string, error)
}

// CLI runs the `jira` command. Every invocation pays ~360ms of tsx startup
// before any network, which is why results are cached and rendered first.
type CLI struct {
	Bin string
}

func (c CLI) Search(ctx context.Context, jql string, limit int) ([]Issue, error) {
	out, err := c.run(ctx, "search", "--jql", jql, "-l", strconv.Itoa(limit), "-f", "json")
	if err != nil {
		return nil, err
	}
	return ParseSearchJSON(out)
}

func (c CLI) Issue(ctx context.Context, key string) (string, error) {
	out, err := c.run(ctx, "get", key, "-f", "markdown")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (c CLI) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %s", c.Bin, args[0], firstLine(stderr.String(), err))
	}
	return stdout.Bytes(), nil
}

// firstLine keeps the footer to one line; a stack trace in a status bar is
// noise, and the useful part of a CLI failure is almost always line one.
func firstLine(s string, fallback error) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return fallback.Error()
}
