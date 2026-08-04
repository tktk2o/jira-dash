package main

import (
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
