package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	jirapkg "jira-dash/internal/jira"
)

func TestTransitionsJSONOutput(t *testing.T) {
	fc := &fakeClient{transitions: []jirapkg.Transition{{ID: "11", Name: "Start progress"}}}
	var buf bytes.Buffer
	if err := runTransitions(context.Background(), fc, []string{"-f", "json", "ABC-1"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Start progress") {
		t.Errorf("output = %s", buf.String())
	}
}

func TestUsersAssignableAcceptsBothShortAndLongQueryFlag(t *testing.T) {
	for _, flagSpelling := range []string{"-q", "--query"} {
		fc := &fakeClient{assignableUsers: []jirapkg.User{{AccountID: "acc-1", DisplayName: "Ada"}}}
		var buf bytes.Buffer
		if err := runUsers(context.Background(), fc, []string{"assignable", flagSpelling, "ada", "-f", "json", "ABC-1"}, &buf); err != nil {
			t.Fatalf("%s: %v", flagSpelling, err)
		}
		if !strings.Contains(buf.String(), "acc-1") {
			t.Errorf("%s: output = %s", flagSpelling, buf.String())
		}
	}
}

func TestUnknownUsersSubcommandIsAnError(t *testing.T) {
	fc := &fakeClient{}
	if err := runUsers(context.Background(), fc, []string{"search", "ABC-1"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error")
	}
}
