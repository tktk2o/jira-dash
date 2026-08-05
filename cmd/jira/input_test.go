package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileOrStdinReadsAFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.md")
	if err := os.WriteFile(path, []byte("hello from a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readFileOrStdin(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello from a file" {
		t.Errorf("got %q", got)
	}
}

// A path of "-" must read stdin, matching the old CLI's own "パイプ入力も可"
// convention on -D/-B.
func TestReadFileOrStdinReadsStdinWhenPathIsDash(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	go func() {
		_, _ = w.Write([]byte("piped body"))
		w.Close()
	}()

	got, err := readFileOrStdin("-")
	if err != nil {
		t.Fatal(err)
	}
	if got != "piped body" {
		t.Errorf("got %q", got)
	}
}

func TestParseFieldsJSONMergesFileAndInlineWithInlineWinning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fields.json")
	if err := os.WriteFile(path, []byte(`{"a":"from-file","b":"from-file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := parseFieldsJSON(`{"a":"from-inline"}`, path)
	if err != nil {
		t.Fatal(err)
	}
	if got["a"] != "from-inline" {
		t.Errorf("a = %v, want the inline flag to win", got["a"])
	}
	if got["b"] != "from-file" {
		t.Errorf("b = %v", got["b"])
	}
}

func TestParseFieldsJSONRejectsNonObjectInput(t *testing.T) {
	if _, err := parseFieldsJSON(`["not", "an", "object"]`, ""); err == nil {
		t.Error("want an error")
	}
}

// Nothing in cmd/jira ever reads Credentials.APIToken directly - only
// internal/jira's do() sees it, to set one Authorization header (client.go).
// Every dry-run preview and error message in this package is built from
// flag values and jiraClient's typed return values alone, so there is no
// code path here that could print a token even by accident. This test
// stands in for that structural fact by checking dry-run output built from
// a request that, if this program ever threaded credentials through by
// mistake, would be the most likely place for one to leak.
func TestDryRunOutputNeverMentionsCredentialFields(t *testing.T) {
	fc := &fakeClient{}
	var buf bytes.Buffer
	if err := runCreate(context.Background(), fc, []string{"-p", "ABC", "-t", "Bug", "-s", "x", "--assignee", "acc-1", "--dry-run"}, &buf); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"apiToken", "APIToken", "Authorization", "Basic "} {
		if strings.Contains(buf.String(), forbidden) {
			t.Errorf("dry-run output mentions %q: %s", forbidden, buf.String())
		}
	}
}
