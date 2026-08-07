package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	jirapkg "jira-dash/internal/jira"
)

func TestJQLSuggestTableOutput(t *testing.T) {
	fc := &fakeClient{jqlSuggestions: []jirapkg.Suggestion{
		{Value: "PROJ-1", DisplayName: "Project One"},
	}}
	var buf bytes.Buffer
	if err := runJQL(context.Background(), fc, []string{"suggest", "project", "one"}, &buf); err != nil {
		t.Fatal(err)
	}
	if fc.lastJQLFieldName != "project" || fc.lastJQLFieldValue != "one" {
		t.Errorf("fieldName=%q fieldValue=%q", fc.lastJQLFieldName, fc.lastJQLFieldValue)
	}
	if !strings.Contains(buf.String(), "PROJ-1\tProject One") {
		t.Errorf("output = %s", buf.String())
	}
}

func TestJQLSuggestAllowsOmittingFieldValue(t *testing.T) {
	fc := &fakeClient{jqlSuggestions: []jirapkg.Suggestion{{Value: "PROJ-1", DisplayName: "Project One"}}}
	var buf bytes.Buffer
	if err := runJQL(context.Background(), fc, []string{"suggest", "project"}, &buf); err != nil {
		t.Fatal(err)
	}
	if fc.lastJQLFieldValue != "" {
		t.Errorf("fieldValue = %q, want empty", fc.lastJQLFieldValue)
	}
}

func TestJQLSuggestJSONOutput(t *testing.T) {
	fc := &fakeClient{jqlSuggestions: []jirapkg.Suggestion{{Value: "PROJ-1", DisplayName: "Project One"}}}
	var buf bytes.Buffer
	if err := runJQL(context.Background(), fc, []string{"suggest", "-f", "json", "project"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"value": "PROJ-1"`) {
		t.Errorf("output = %s", buf.String())
	}
}

func TestUnknownJQLSubcommandIsAnError(t *testing.T) {
	fc := &fakeClient{}
	if err := runJQL(context.Background(), fc, []string{"search"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error")
	}
}

func TestJQLSuggestRejectsMissingFieldName(t *testing.T) {
	fc := &fakeClient{}
	if err := runJQL(context.Background(), fc, []string{"suggest"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error")
	}
}
