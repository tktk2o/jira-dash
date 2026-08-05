package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	jirapkg "jira-dash/internal/jira"
)

// get must accept both -f and --format for the same flag, per the
// migration plan's requirement that flag has to register both spellings
// itself.
func TestGetAcceptsBothShortAndLongFormatFlag(t *testing.T) {
	for _, flagSpelling := range []string{"-f", "--format"} {
		fc := &fakeClient{issue: jirapkg.Issue{Key: "ABC-1", Summary: "x"}}
		var buf bytes.Buffer
		if err := runGet(context.Background(), fc, []string{flagSpelling, "json", "ABC-1"}, &buf); err != nil {
			t.Fatalf("%s: %v", flagSpelling, err)
		}
		if !strings.Contains(buf.String(), `"key": "ABC-1"`) {
			t.Errorf("%s: output = %s", flagSpelling, buf.String())
		}
	}
}

// get --fields has no seam in internal/jira's fixed request-field list, so
// it must fail loudly rather than silently returning an issue without the
// fields the caller asked for.
func TestGetFieldsFlagIsRejectedRatherThanSilentlyIgnored(t *testing.T) {
	fc := &fakeClient{}
	err := runGet(context.Background(), fc, []string{"--fields", "customfield_10001", "ABC-1"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want an error")
	}
	if len(fc.calls) != 0 {
		t.Errorf("client was called despite the unsupported flag: %v", fc.calls)
	}
}

func TestGetRequiresExactlyOneKeyArgument(t *testing.T) {
	fc := &fakeClient{}
	if err := runGet(context.Background(), fc, nil, &bytes.Buffer{}); err == nil {
		t.Error("want an error with no key")
	}
	if err := runGet(context.Background(), fc, []string{"ABC-1", "DEF-2"}, &bytes.Buffer{}); err == nil {
		t.Error("want an error with two keys")
	}
}
