package jira

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// impedimentFlagValue is Jira's own vocabulary for the flag icon on an issue
// - not a value this program or its company invented, so it is safe to keep
// here even though nothing else in this file is allowed to name a
// site-specific value. Setting a custom field to this exact string is how
// the Jira UI itself represents "flagged"; clearing it means sending an
// empty array instead.
const impedimentFlagValue = "Impediment"

// Edit is a partial update to one issue: every field is a pointer so that
// "not sent" (nil - leave this field alone) is distinguishable from "sent as
// the string 'null'" (a pointer to that literal - clear the field). The old
// CLI expressed the same contract by taking the literal string "null" as a
// flag's value and mapping it to Jira's "clear this field" request shape; a
// later task's CLI layer does that same mapping onto these pointers, so Edit
// itself must not collapse the distinction by using bare strings.
type Edit struct {
	Summary     *string
	Description *string
	Assignee    *string
	Priority    *string
	Labels      *string
	Parent      *string
	Sprint      *string
	StoryPoints *string
	Team        *string
	StartDate   *string
	TargetStart *string
	Due         *string
	FixVersions *string
	Components  *string
	Flag        *bool
}

// clearField is the sentinel Edit's pointer fields take to mean "clear
// this", per Edit's own doc comment. Kept as a named constant rather than
// repeating the literal "null", so a caller building an Edit and a reader
// checking against it are visibly talking about the same contract.
const clearField = "null"

// isClear reports whether s asks to clear the field, per Edit's contract.
func isClear(s *string) bool { return s != nil && *s == clearField }

// setSimpleField writes value into fields under jiraKey, or nil to clear it,
// unless value itself is nil - in which case the field is left untouched by
// not being present in fields at all. Shared by every *string Edit field
// whose Jira representation is just the bare string (Summary, Assignee,
// Priority, Parent, Due): only the fields with a different wire shape
// (labels/versions/components as arrays, custom fields under a resolved id)
// need their own handling below.
func setSimpleField(fields map[string]any, jiraKey string, value *string) {
	if value == nil {
		return
	}
	if isClear(value) {
		fields[jiraKey] = nil
		return
	}
	fields[jiraKey] = *value
}

// setCommaListField writes value's comma-separated items as a JSON array
// under jiraKey, or an empty array to clear it. Labels, fix versions, and
// components all arrive from the CLI as one comma-separated flag value (CLI
// compatibility with the old tool) but must go up as arrays - Jira's schema
// for all three is `["a","b"]`, never a bare string.
func setCommaListField(fields map[string]any, jiraKey string, value *string, wrap func(string) any) {
	if value == nil {
		return
	}
	if isClear(value) {
		fields[jiraKey] = []any{}
		return
	}
	items := strings.Split(*value, ",")
	list := make([]any, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		list = append(list, wrap(item))
	}
	fields[jiraKey] = list
}

// setCustomField is setSimpleField's counterpart for a value that lives
// under a resolved customfield_NNNNN id rather than a fixed Jira key. jiraKey
// empty means this site has never configured the field; per the migration
// plan, silently dropping a value the caller explicitly set would hide a
// real failure (the update the caller asked for never happened) behind what
// looks like success, so this returns an error instead of skipping the
// field.
func setCustomField(fields map[string]any, fieldName, jiraKey string, value *string, encode func(string) (any, error)) error {
	if value == nil {
		return nil
	}
	if jiraKey == "" {
		return fmt.Errorf("this site has no %s field configured; cannot set --%s", fieldName, strings.ToLower(fieldName))
	}
	if isClear(value) {
		fields[jiraKey] = nil
		return nil
	}
	encoded, err := encode(*value)
	if err != nil {
		return fmt.Errorf("%s: %w", fieldName, err)
	}
	fields[jiraKey] = encoded
	return nil
}

type editRequest struct {
	Fields map[string]any `json:"fields"`
}

// Edit applies a partial update to key. Fields left nil in e are never
// mentioned in the request, so Jira's own "field not editable on this
// screen" rejections only fire for fields this call actually touches.
func (c *Client) Edit(ctx context.Context, key string, e Edit) error {
	fieldIDs, err := c.FieldIDs(ctx)
	if err != nil {
		return err
	}

	fields := map[string]any{}

	setSimpleField(fields, "summary", e.Summary)
	setSimpleField(fields, "due", e.Due)
	if e.Description != nil {
		if isClear(e.Description) {
			fields["description"] = nil
		} else {
			fields["description"] = markdownToADF(*e.Description)
		}
	}
	if e.Assignee != nil {
		if isClear(e.Assignee) {
			fields["assignee"] = nil
		} else {
			fields["assignee"] = map[string]string{"accountId": *e.Assignee}
		}
	}
	if e.Priority != nil {
		if isClear(e.Priority) {
			fields["priority"] = nil
		} else {
			fields["priority"] = rawNamed{Name: *e.Priority}
		}
	}
	if e.Parent != nil {
		if isClear(e.Parent) {
			fields["parent"] = nil
		} else {
			fields["parent"] = rawIssueRef{Key: *e.Parent}
		}
	}

	setCommaListField(fields, "labels", e.Labels, func(s string) any { return s })
	setCommaListField(fields, "fixVersions", e.FixVersions, func(s string) any { return map[string]string{"name": s} })
	setCommaListField(fields, "components", e.Components, func(s string) any { return map[string]string{"name": s} })

	if err := setCustomField(fields, "Team", fieldIDs.Team, e.Team, func(s string) (any, error) { return s, nil }); err != nil {
		return err
	}
	if err := setCustomField(fields, "Start date", fieldIDs.StartDate, e.StartDate, func(s string) (any, error) { return s, nil }); err != nil {
		return err
	}
	if err := setCustomField(fields, "Target start", fieldIDs.TargetStart, e.TargetStart, func(s string) (any, error) { return s, nil }); err != nil {
		return err
	}
	if err := setCustomField(fields, "Story Points", fieldIDs.StoryPoints, e.StoryPoints, func(s string) (any, error) {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("story points must be a number, got %q", s)
		}
		return f, nil
	}); err != nil {
		return err
	}
	if e.Sprint != nil {
		if fieldIDs.Sprint == "" {
			return fmt.Errorf("this site has no Sprint field configured; cannot set --sprint")
		}
		if isClear(e.Sprint) {
			fields[fieldIDs.Sprint] = nil
		} else {
			// A Jira issue key is always PROJECT-NUMBER; the project key is
			// what ActiveSprint needs to find key's board, and Edit is
			// never handed the project separately (unlike Create, which
			// gets it from NewIssue).
			projectKey, _, _ := strings.Cut(key, "-")
			sprint, err := c.ActiveSprint(ctx, projectKey, *e.Sprint)
			if err != nil {
				return err
			}
			fields[fieldIDs.Sprint] = sprint.ID
		}
	}

	if e.Flag != nil {
		if fieldIDs.Flagged == "" {
			return fmt.Errorf("this site has no Flagged field configured; cannot set --flag")
		}
		if *e.Flag {
			// Jira's Impediment marker is what the UI's flag icon actually
			// sets: an array holding one value with this exact name.
			fields[fieldIDs.Flagged] = []any{map[string]string{"value": impedimentFlagValue}}
		} else {
			fields[fieldIDs.Flagged] = []any{}
		}
	}

	if len(fields) == 0 {
		return nil
	}
	return c.do(ctx, http.MethodPut, "/issue/"+key, editRequest{Fields: fields}, nil)
}

// Transition is one status change an issue can make right now - a subset of
// all of a project's statuses, since Jira's workflow only allows moving
// along the edges defined for the issue's current status.
type Transition struct {
	ID   string
	Name string
}

// rawTransition is one element of GET /issue/{key}/transitions' `transitions`
// array.
type rawTransition struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	To   rawNamed `json:"to"`
}

type transitionsResponse struct {
	Transitions []rawTransition `json:"transitions"`
}

// Transitions lists the status changes key can make right now.
func (c *Client) Transitions(ctx context.Context, key string) ([]Transition, error) {
	var resp transitionsResponse
	if err := c.do(ctx, http.MethodGet, "/issue/"+key+"/transitions", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Transition, 0, len(resp.Transitions))
	for _, t := range resp.Transitions {
		// Name is the transition's own label ("Start progress"), which does
		// not have to match the status it lands on (To.Name, "In Progress")
		// - but every workflow this program has seen the two are close
		// enough that reporting To.Name here would be a confusing surprise
		// for a caller who typed the transition's label. Kept as the
		// transition's own name.
		out = append(out, Transition{ID: t.ID, Name: t.Name})
	}
	return out, nil
}

type transitionRequest struct {
	Transition struct {
		ID string `json:"id"`
	} `json:"transition"`
}

// Transition moves key through the transition named statusName, matched
// case-insensitively against Transitions' own list. An unmatched name is the
// single most common way this call fails - a typo, or a status this issue's
// current workflow state cannot reach - so the error lists every name that
// was available, which the old CLI never did.
func (c *Client) Transition(ctx context.Context, key, statusName string) error {
	transitions, err := c.Transitions(ctx, key)
	if err != nil {
		return err
	}
	for _, t := range transitions {
		if strings.EqualFold(t.Name, statusName) {
			var req transitionRequest
			req.Transition.ID = t.ID
			return c.do(ctx, http.MethodPost, "/issue/"+key+"/transitions", req, nil)
		}
	}

	names := make([]string, len(transitions))
	for i, t := range transitions {
		names[i] = t.Name
	}
	return fmt.Errorf("no transition named %q on %s; available: %s", statusName, key, strings.Join(names, ", "))
}
