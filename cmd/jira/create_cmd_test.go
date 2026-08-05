package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	jirapkg "jira-dash/internal/jira"
)

func TestCreateAcceptsBothShortAndLongFlags(t *testing.T) {
	fc := &fakeClient{createdIssue: jirapkg.Issue{Key: "ABC-9"}}
	err := runCreate(context.Background(), fc, []string{
		"-p", "ABC", "-t", "Bug", "-s", "Fix the thing", "-d", "It is broken",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if fc.lastCreateInput.ProjectKey != "ABC" || fc.lastCreateInput.Type != "Bug" ||
		fc.lastCreateInput.Summary != "Fix the thing" || fc.lastCreateInput.Description != "It is broken" {
		t.Errorf("create input = %+v", fc.lastCreateInput)
	}
}

// --dry-run must not call Create, Edit, or anything else - the whole point
// of the flag is that nothing goes out over the network.
func TestCreateDryRunSendsNothing(t *testing.T) {
	fc := &fakeClient{}
	var buf bytes.Buffer
	err := runCreate(context.Background(), fc, []string{
		"-p", "ABC", "-t", "Bug", "-s", "x", "--assignee", "acc-1", "--dry-run",
	}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called during --dry-run: %v", fc.calls)
	}
	if !strings.Contains(buf.String(), "ABC") || !strings.Contains(buf.String(), "assignee") {
		t.Errorf("dry-run output missing expected content: %s", buf.String())
	}
}

// Flags with no field on NewIssue (assignee, priority, labels, ...) are
// applied through a follow-up Edit call, since Edit's pointer fields cover
// almost exactly this same set - see postCreateEdit's own comment on why.
func TestCreateAppliesExtraFieldsThroughAFollowUpEdit(t *testing.T) {
	fc := &fakeClient{createdIssue: jirapkg.Issue{Key: "ABC-9"}, issue: jirapkg.Issue{Key: "ABC-9", Summary: "refreshed"}}
	err := runCreate(context.Background(), fc, []string{
		"-p", "ABC", "-t", "Bug", "-s", "x", "--priority", "High", "--labels", "urgent,backend",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if fc.lastEditKey != "ABC-9" {
		t.Errorf("edit key = %q", fc.lastEditKey)
	}
	if fc.lastEditInput.Priority == nil || *fc.lastEditInput.Priority != "High" {
		t.Errorf("edit priority = %v", fc.lastEditInput.Priority)
	}
	if fc.lastEditInput.Labels == nil || *fc.lastEditInput.Labels != "urgent,backend" {
		t.Errorf("edit labels = %v", fc.lastEditInput.Labels)
	}
}

func TestCreateRequiresProjectTypeAndSummary(t *testing.T) {
	fc := &fakeClient{}
	if err := runCreate(context.Background(), fc, []string{"-t", "Bug", "-s", "x"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error with no -p")
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called despite missing required flags: %v", fc.calls)
	}
}

// --fields-json is accepted and parsed, but internal/jira has no seam to
// send it for real - this documents that limitation as a loud error rather
// than a silently dropped field.
func TestCreateFieldsJSONIsRejectedOnActualSend(t *testing.T) {
	fc := &fakeClient{createdIssue: jirapkg.Issue{Key: "ABC-9"}}
	err := runCreate(context.Background(), fc, []string{
		"-p", "ABC", "-t", "Bug", "-s", "x", "--fields-json", `{"customfield_10001":"x"}`,
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want an error")
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called despite the unsendable field: %v", fc.calls)
	}
}

// --fields-json must be visible in --dry-run output even though it cannot
// be sent for real - dry-run's job is to show intent, not to validate that
// intent is currently achievable.
func TestCreateFieldsJSONShowsInDryRunOutput(t *testing.T) {
	fc := &fakeClient{}
	var buf bytes.Buffer
	err := runCreate(context.Background(), fc, []string{
		"-p", "ABC", "-t", "Bug", "-s", "x", "--fields-json", `{"customfield_10001":"x"}`, "--dry-run",
	}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "customfield_10001") {
		t.Errorf("dry-run output missing the fields-json content: %s", buf.String())
	}
}
