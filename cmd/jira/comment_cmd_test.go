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

func TestUnknownCommentSubcommandIsAnError(t *testing.T) {
	fc := &fakeClient{}
	if err := runComment(context.Background(), fc, []string{"edit", "ABC-1", "1"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error for the dropped comment-edit subcommand")
	}
}
