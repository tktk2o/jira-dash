package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCommentAddAcceptsBothShortAndLongBodyFlag(t *testing.T) {
	for _, flagSpelling := range []string{"-b", "--body"} {
		fc := &fakeClient{}
		if err := runComment(context.Background(), fc, []string{"add", flagSpelling, "hello", "ABC-1"}, &bytes.Buffer{}); err != nil {
			t.Fatalf("%s: %v", flagSpelling, err)
		}
		if fc.lastAddCommentMD != "hello" {
			t.Errorf("%s: body = %q", flagSpelling, fc.lastAddCommentMD)
		}
	}
}

// --dry-run must never call AddComment - that is the one call in this
// subcommand that writes.
func TestCommentAddDryRunSendsNothing(t *testing.T) {
	fc := &fakeClient{}
	var buf bytes.Buffer
	if err := runComment(context.Background(), fc, []string{"add", "-b", "hello", "--dry-run", "ABC-1"}, &buf); err != nil {
		t.Fatal(err)
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called during --dry-run: %v", fc.calls)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("dry-run output does not show the body: %s", buf.String())
	}
}

func TestCommentAddWithNoBodyIsAnError(t *testing.T) {
	fc := &fakeClient{}
	if err := runComment(context.Background(), fc, []string{"add", "ABC-1"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error")
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called with no body: %v", fc.calls)
	}
}

func TestCommentListAcceptsBothShortAndLongMaxResultsFlag(t *testing.T) {
	for _, flagSpelling := range []string{"-m", "--max-results"} {
		fc := &fakeClient{comments: nil}
		var buf bytes.Buffer
		if err := runComment(context.Background(), fc, []string{"list", flagSpelling, "5", "ABC-1"}, &buf); err != nil {
			t.Fatalf("%s: %v", flagSpelling, err)
		}
		if !strings.Contains(buf.String(), `"issue_key"`) {
			t.Errorf("%s: output = %s", flagSpelling, buf.String())
		}
	}
}

func TestCommentEditPassesKeyCommentIDAndBodyThrough(t *testing.T) {
	for _, flagSpelling := range []string{"-b", "--body"} {
		fc := &fakeClient{}
		args := []string{"edit", flagSpelling, "rewritten", "ABC-1", "10001"}
		if err := runComment(context.Background(), fc, args, &bytes.Buffer{}); err != nil {
			t.Fatalf("%s: %v", flagSpelling, err)
		}
		if fc.lastEditCommentKey != "ABC-1" || fc.lastEditCommentID != "10001" {
			t.Errorf("%s: target = %q/%q", flagSpelling, fc.lastEditCommentKey, fc.lastEditCommentID)
		}
		if fc.lastEditCommentMD != "rewritten" {
			t.Errorf("%s: body = %q", flagSpelling, fc.lastEditCommentMD)
		}
	}
}

// --dry-run must never call EditComment. This matters more here than on add:
// an unwanted edit destroys the comment's previous text, where an unwanted
// add only leaves a stray comment behind.
func TestCommentEditDryRunSendsNothing(t *testing.T) {
	fc := &fakeClient{}
	var buf bytes.Buffer
	args := []string{"edit", "-b", "rewritten", "--dry-run", "ABC-1", "10001"}
	if err := runComment(context.Background(), fc, args, &buf); err != nil {
		t.Fatal(err)
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called during --dry-run: %v", fc.calls)
	}
	if !strings.Contains(buf.String(), "rewritten") {
		t.Errorf("dry-run output does not show the body: %s", buf.String())
	}
}

// The comment id is not optional: without it there is nothing to identify
// which comment to overwrite, and guessing (the newest? the caller's own?)
// would be a destructive guess.
func TestCommentEditWithoutACommentIDIsAnError(t *testing.T) {
	fc := &fakeClient{}
	if err := runComment(context.Background(), fc, []string{"edit", "-b", "x", "ABC-1"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error")
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called with no comment id: %v", fc.calls)
	}
}

func TestCommentEditWithNoBodyIsAnError(t *testing.T) {
	fc := &fakeClient{}
	if err := runComment(context.Background(), fc, []string{"edit", "ABC-1", "10001"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error")
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called with no body: %v", fc.calls)
	}
}

func TestUnknownCommentSubcommandIsAnError(t *testing.T) {
	fc := &fakeClient{}
	if err := runComment(context.Background(), fc, []string{"delete", "ABC-1", "1"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error for a subcommand this CLI does not have")
	}
}
