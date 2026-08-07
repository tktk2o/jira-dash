package jira

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// NewIssue is what `jira create` needs to open one issue. Sprint is a name,
// not an id - the CLI's `--sprint` flag has always taken a name, and
// resolving it to an id is ActiveSprint's job, not the caller's.
type NewIssue struct {
	ProjectKey  string
	Type        string
	Summary     string
	Description string
	Sprint      string
}

// rawProjectRef is the shape POST /issue expects to identify the project: a
// key, nothing else. Distinct from rawProject (issue.go's read-side shape,
// which also carries a name Jira never asks for on write).
type rawProjectRef struct {
	Key string `json:"key"`
}

type createRequest struct {
	Fields map[string]any `json:"fields"`
}

type createResponse struct {
	Key string `json:"key"`
}

// Create opens a new issue and returns it re-fetched through Issue, so the
// caller gets back the same shape `jira search`/`jira get` promise rather
// than the bare key POST /issue itself returns.
func (c *Client) Create(ctx context.Context, n NewIssue) (Issue, error) {
	fieldIDs, err := c.FieldIDs(ctx)
	if err != nil {
		return Issue{}, err
	}

	fields := map[string]any{
		"project":   rawProjectRef{Key: n.ProjectKey},
		"issuetype": rawNamed{Name: n.Type},
		"summary":   n.Summary,
	}
	if n.Description != "" {
		doc := markdownToADF(n.Description)
		fields["description"] = doc
	}

	if n.Sprint != "" {
		sprint, err := c.ActiveSprint(ctx, n.ProjectKey, n.Sprint)
		if err != nil {
			return Issue{}, err
		}
		if fieldIDs.Sprint == "" {
			// Silently dropping this would file the issue into the backlog
			// with no sprint set, which looks like Jira ignored the flag
			// rather than like this site simply has no Sprint field.
			return Issue{}, fmt.Errorf("this site has no Sprint field configured; cannot set --sprint %q", n.Sprint)
		}
		fields[fieldIDs.Sprint] = sprint.ID
	}

	var resp createResponse
	if err := c.do(ctx, http.MethodPost, "/issue", createRequest{Fields: fields}, &resp); err != nil {
		return Issue{}, err
	}
	return c.Issue(ctx, resp.Key)
}

// rawBoard is one element of GET /board's `values` array.
type rawBoard struct {
	ID int `json:"id"`
}

// boardsResponse is one page of GET /board. IsLast is the agile API's own
// signal that there is no next page - startAt/maxResults alone would leave
// callers guessing whether a short page meant "done" or "coincidentally
// exactly maxResults short of the truth".
type boardsResponse struct {
	Values []rawBoard `json:"values"`
	IsLast bool       `json:"isLast"`
}

// sprintsResponse is one page of GET /board/{id}/sprint, paginated the same
// way boardsResponse is.
type sprintsResponse struct {
	Values []Sprint `json:"values"`
	IsLast bool     `json:"isLast"`
}

// maxActiveSprintPages caps how many pages ActiveSprint will fetch per
// board/sprint listing. A real site's boards or sprints number in the tens
// to low hundreds; this cap exists so a site that never returns isLast=true
// (a misbehaving proxy, say) makes ActiveSprint fail loudly instead of
// looping forever.
const maxActiveSprintPages = 50

// pageSize is the maxResults this program asks the agile API for per page.
// Large enough that most sites finish in one round trip, without being so
// large a single response becomes unwieldy.
const pageSize = 50

// ActiveSprint resolves a sprint name to the Sprint a new issue should join:
// the board's active sprint if its name has prefix, otherwise a future one
// with that prefix, and never a closed sprint. This mirrors
// Issue.CurrentSprint's own preference (active over future, closed never) so
// that `jira create --sprint <name>` lands an issue in the same place
// `jira search` would already show it - the two must agree, or an issue
// created here could look misfiled the moment it is searched for.
func (c *Client) ActiveSprint(ctx context.Context, projectKey, prefix string) (Sprint, error) {
	wantedID, byIDErr := strconv.Atoi(prefix)
	boards, err := c.listBoards(ctx, projectKey)
	if err != nil {
		return Sprint{}, err
	}
	if len(boards) == 0 {
		return Sprint{}, fmt.Errorf("no board found for project %q", projectKey)
	}

	var future Sprint
	var haveFuture bool
	for _, board := range boards {
		sprints, err := c.listSprints(ctx, board.ID)
		if err != nil {
			return Sprint{}, err
		}
		for _, s := range sprints {
			matches := prefix == "" || (byIDErr == nil && s.ID == wantedID) ||
				(byIDErr != nil && strings.HasPrefix(s.Name, prefix))
			if !matches {
				continue
			}
			switch s.State {
			case "active":
				return s, nil
			case "future":
				if !haveFuture {
					future, haveFuture = s, true
				}
			}
		}
	}
	if haveFuture {
		return future, nil
	}
	return Sprint{}, fmt.Errorf("no active or future sprint named %q found for project %q", prefix, projectKey)
}

// listBoards walks every page of GET /board for a project, stopping at
// isLast (or maxActiveSprintPages, whichever comes first) so a site with
// more boards than fit on one page is not silently truncated to its first
// page - the bug this function exists to fix.
func (c *Client) listBoards(ctx context.Context, projectKey string) ([]rawBoard, error) {
	var boards []rawBoard
	for page := 0; page < maxActiveSprintPages; page++ {
		q := url.Values{
			"projectKeyOrId": {projectKey},
			"startAt":        {strconv.Itoa(page * pageSize)},
			"maxResults":     {strconv.Itoa(pageSize)},
		}
		var resp boardsResponse
		if err := c.doAgile(ctx, http.MethodGet, "/board?"+q.Encode(), nil, &resp); err != nil {
			return nil, err
		}
		boards = append(boards, resp.Values...)
		if resp.IsLast || len(resp.Values) == 0 {
			return boards, nil
		}
	}
	return boards, nil
}

// listSprints walks every page of GET /board/{id}/sprint the same way
// listBoards walks /board, so a board with more active/future sprints than
// fit on one page cannot hide the real active sprint on a later page.
func (c *Client) listSprints(ctx context.Context, boardID int) ([]Sprint, error) {
	var sprints []Sprint
	for page := 0; page < maxActiveSprintPages; page++ {
		q := url.Values{
			"state":      {"active,future"},
			"startAt":    {strconv.Itoa(page * pageSize)},
			"maxResults": {strconv.Itoa(pageSize)},
		}
		var resp sprintsResponse
		path := fmt.Sprintf("/board/%d/sprint?%s", boardID, q.Encode())
		if err := c.doAgile(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, err
		}
		sprints = append(sprints, resp.Values...)
		if resp.IsLast || len(resp.Values) == 0 {
			return sprints, nil
		}
	}
	return sprints, nil
}
