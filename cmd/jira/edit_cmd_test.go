package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestEditAcceptsBothShortAndLongSummaryFlag(t *testing.T) {
	for _, flagSpelling := range []string{"-s", "--summary"} {
		fc := &fakeClient{}
		if err := runEdit(context.Background(), fc, []string{flagSpelling, "New title", "ABC-1"}, &bytes.Buffer{}); err != nil {
			t.Fatalf("%s: %v", flagSpelling, err)
		}
		if fc.lastEditInput.Summary == nil || *fc.lastEditInput.Summary != "New title" {
			t.Errorf("%s: summary = %v", flagSpelling, fc.lastEditInput.Summary)
		}
	}
}

// The literal string "null" must reach Edit unchanged - Edit.isClear (a
// prior task) is what turns it into "clear this field"; the CLI layer's
// only job is to pass the flag value straight through.
func TestEditPassesTheLiteralNullThroughForClearing(t *testing.T) {
	fc := &fakeClient{}
	if err := runEdit(context.Background(), fc, []string{"-a", "null", "ABC-1"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if fc.lastEditInput.Assignee == nil || *fc.lastEditInput.Assignee != "null" {
		t.Errorf("assignee = %v, want a pointer to the literal \"null\"", fc.lastEditInput.Assignee)
	}
}

func TestEditFlagAndNoFlagAreMutuallyExclusive(t *testing.T) {
	fc := &fakeClient{}
	if err := runEdit(context.Background(), fc, []string{"--flag", "--no-flag", "ABC-1"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error")
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called despite the flag conflict: %v", fc.calls)
	}
}

// --status is a separate Transition call, never merged into the Edit PUT -
// the two are genuinely different Jira endpoints.
func TestEditStatusCallsTransitionSeparatelyFromEdit(t *testing.T) {
	fc := &fakeClient{}
	if err := runEdit(context.Background(), fc, []string{"-S", "Done", "-s", "New title", "ABC-1"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"Edit", "Transition"}
	if len(fc.calls) != len(wantCalls) || fc.calls[0] != wantCalls[0] || fc.calls[1] != wantCalls[1] {
		t.Errorf("calls = %v, want %v", fc.calls, wantCalls)
	}
	if fc.lastTransitionTo != "Done" {
		t.Errorf("transition target = %q", fc.lastTransitionTo)
	}
}

// --dry-run must call neither Edit nor Transition.
func TestEditDryRunSendsNothing(t *testing.T) {
	fc := &fakeClient{}
	var buf bytes.Buffer
	if err := runEdit(context.Background(), fc, []string{"-S", "Done", "-s", "New title", "--dry-run", "ABC-1"}, &buf); err != nil {
		t.Fatal(err)
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called during --dry-run: %v", fc.calls)
	}
	if !strings.Contains(buf.String(), "New title") || !strings.Contains(buf.String(), "Done") {
		t.Errorf("dry-run output missing expected content: %s", buf.String())
	}
}

func TestEditWithNoFieldsAndNoStatusIsAnError(t *testing.T) {
	fc := &fakeClient{}
	if err := runEdit(context.Background(), fc, []string{"ABC-1"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error")
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called with nothing to do: %v", fc.calls)
	}
}
