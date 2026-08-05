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

	jirapkg "jira-dash/internal/jira"
)

// Issue, Sprint, Comment and JiraTime live in internal/jira: the CLI and the
// TUI both need the exact same JSON shape, and a duplicate type is how that
// shape drifts silently. Aliased rather than renamed at every call site so
// this move's diff does not bury the two behavioural changes (Task 2) under
// hundreds of mechanical ones.
type Issue = jirapkg.Issue
type Sprint = jirapkg.Sprint
type Comment = jirapkg.Comment
type JiraTime = jirapkg.JiraTime

// ParseSearchJSON reads what `jira search -f json` prints.
func ParseSearchJSON(b []byte) ([]Issue, error) {
	return jirapkg.ParseSearchJSON(b)
}

// ParseCommentsJSON reads `jira comment list`.
func ParseCommentsJSON(b []byte) ([]Comment, error) {
	return jirapkg.ParseCommentsJSON(b)
}

// ParseIssueJSON pulls the description out of `jira get -f json`.
func ParseIssueJSON(b []byte) (string, error) {
	return jirapkg.ParseIssueJSON(b)
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
