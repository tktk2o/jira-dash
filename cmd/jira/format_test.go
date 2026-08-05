package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	jirapkg "jira-dash/internal/jira"
)

// parseFormat must accept both spellings the old CLI's --help documents and
// reject anything else, since a typo'd -f value silently falling back to
// table would hide the mistake from a script checking for JSON.
func TestParseFormatAcceptsTableAndJSONAndDefaultsToTable(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    outputFormat
		wantErr bool
	}{
		{"", formatTable, false},
		{"table", formatTable, false},
		{"json", formatJSON, false},
		{"yaml", "", true},
	} {
		got, err := parseFormat(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseFormat(%q): want an error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("parseFormat(%q) = (%q, %v), want (%q, nil)", tc.in, got, err, tc.want)
		}
	}
}

// parseGetFormat must accept the old CLI's full four-value set on `get`
// (table/json/yaml/markdown, per `jira get --help`) and, on anything else,
// name all four rather than leaving the caller to guess which ones this
// replacement kept - a bare usage line for a bad -f value gives no hint the
// format was the problem.
func TestParseGetFormatAcceptsAllFourOldCLIFormatsAndNamesThemOnError(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want outputFormat
	}{
		{"", formatTable},
		{"table", formatTable},
		{"json", formatJSON},
		{"yaml", formatYAML},
		{"markdown", formatMarkdown},
	} {
		got, err := parseGetFormat(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("parseGetFormat(%q) = (%q, %v), want (%q, nil)", tc.in, got, err, tc.want)
		}
	}

	_, err := parseGetFormat("nonsense")
	if err == nil {
		t.Fatal("want an error for an unknown format")
	}
	for _, want := range []string{"table", "json", "yaml", "markdown"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// `jira get -f yaml` and `-f markdown` must each carry the same fields the
// table view shows, not fall through to it silently - a format flag that
// parses but renders as something else is as misleading as one that errors
// on a typo.
func TestGetYAMLAndMarkdownCarryTheSameFieldsAsTable(t *testing.T) {
	assignee := "Ada Lovelace"
	issue := jirapkg.Issue{
		Key: "ABC-1", Summary: "Fix the thing", Type: "Bug", Status: "In Progress",
		Assignee: &assignee, Priority: "High", Labels: []string{"urgent"},
	}
	for _, format := range []outputFormat{formatYAML, formatMarkdown} {
		var buf bytes.Buffer
		if err := writeGetOutput(&buf, format, issue, "the description"); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		got := buf.String()
		for _, want := range []string{"ABC-1", "Fix the thing", "Bug", "In Progress", "Ada Lovelace", "High", "urgent", "the description"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: missing %q in %s", format, want, got)
			}
		}
		if strings.HasPrefix(strings.TrimSpace(got), "{") {
			t.Errorf("%s: output looks like JSON: %s", format, got)
		}
	}
}

// `jira get -f json` must carry every field Issue itself promises, plus
// description - the one field get fetches that search never does. This is
// the guarantee the whole task exists for: a script that ran `jq .summary`
// against the old CLI must see the same key here.
func TestGetJSONCarriesIssueFieldsPlusDescription(t *testing.T) {
	assignee := "Ada Lovelace"
	issue := jirapkg.Issue{
		Key: "ABC-1", Summary: "Fix the thing", Type: "Bug", Status: "In Progress",
		Assignee: &assignee, Priority: "High", Labels: []string{"urgent"},
	}
	var buf bytes.Buffer
	if err := writeGetOutput(&buf, formatJSON, issue, "*no description*"); err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output was not valid JSON: %v\n%s", err, buf.String())
	}
	for _, key := range []string{"key", "summary", "type", "status", "assignee", "priority", "labels", "description"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing key %q in %s", key, buf.String())
		}
	}
	if decoded["description"] != "*no description*" {
		t.Errorf("description = %v", decoded["description"])
	}
}

// `jira search -f json` must echo the jql it ran and wrap results in
// {jql, total, results} - the shape the old CLI's own output takes, and
// the one internal/jira.ParseSearchJSON already knows how to read (it
// looks only at total/results, so this envelope is a superset).
func TestSearchJSONWrapsResultsWithJQLAndTotal(t *testing.T) {
	issues := []jirapkg.Issue{{Key: "ABC-1"}, {Key: "ABC-2"}}
	var buf bytes.Buffer
	if err := writeSearchOutput(&buf, formatJSON, `project = "ABC"`, issues); err != nil {
		t.Fatal(err)
	}

	got, err := jirapkg.ParseSearchJSON(buf.Bytes())
	if err != nil {
		t.Fatalf("internal/jira's own parser rejected this output: %v", err)
	}
	if len(got) != 2 || got[0].Key != "ABC-1" {
		t.Errorf("results = %+v", got)
	}

	var decoded struct {
		JQL   string `json:"jql"`
		Total int    `json:"total"`
	}
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.JQL != `project = "ABC"` || decoded.Total != 2 {
		t.Errorf("jql/total = %q/%d", decoded.JQL, decoded.Total)
	}
}

// `jira comment list` must produce output internal/jira.ParseCommentsJSON
// can already read, since the TUI depends on that parser and this command
// is the other producer of the same shape.
func TestCommentListJSONIsReadableByTheExistingParser(t *testing.T) {
	comments := []jirapkg.Comment{{ID: "1", Author: "Ada", Body: "hi"}}
	var buf bytes.Buffer
	if err := writeCommentListOutput(&buf, "ABC-1", comments); err != nil {
		t.Fatal(err)
	}
	got, err := jirapkg.ParseCommentsJSON(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Author != "Ada" {
		t.Errorf("comments = %+v", got)
	}
	if !strings.Contains(buf.String(), `"issue_key"`) {
		t.Errorf("missing issue_key in %s", buf.String())
	}
}

// Table output is only promised to be "close enough for a human", per the
// migration plan, but it must never be JSON - a caller that got the format
// flag wrong should see something obviously not machine output.
func TestTableOutputIsNotJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSearchOutput(&buf, formatTable, "", []jirapkg.Issue{{Key: "ABC-1", Summary: "Fix it"}}); err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(buf.Bytes(), &v); err == nil {
		t.Errorf("table output parsed as JSON: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "ABC-1") {
		t.Errorf("table output missing the issue key: %s", buf.String())
	}
}
