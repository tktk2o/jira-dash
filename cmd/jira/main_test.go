package main

import (
	"bytes"
	"strings"
	"testing"
)

// run must never call newClient for `jira auth ...`: that path exists
// precisely for a machine with no credentials yet, so it cannot depend on
// LoadCredentials succeeding.
func TestRunNeverBuildsAClientForAuth(t *testing.T) {
	newClientCalled := false
	newClient := func() (jiraClient, error) {
		newClientCalled = true
		return &fakeClient{}, nil
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"auth", "status"}, &stdout, &stderr, newClient)
	if newClientCalled {
		t.Error("newClient was called for jira auth")
	}
	if code == 0 {
		t.Error("want a non-zero exit: auth is stubbed as not implemented")
	}
	if !strings.Contains(stderr.String(), "not implemented") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// Every other subcommand must fail with a clear message and exit 1 when
// credentials cannot be resolved - not a panic, not exit 0.
func TestRunReportsACredentialFailureCleanly(t *testing.T) {
	newClient := func() (jiraClient, error) { return nil, errCredentialsFailure }
	var stdout, stderr bytes.Buffer
	code := run([]string{"get", "ABC-1"}, &stdout, &stderr, newClient)
	if code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), errCredentialsFailure.Error()) {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunRejectsAnUnknownCommand(t *testing.T) {
	newClient := func() (jiraClient, error) { return &fakeClient{}, nil }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"bogus"}, &stdout, &stderr, newClient); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
}

func TestRunWithNoArgumentsPrintsUsage(t *testing.T) {
	newClient := func() (jiraClient, error) { return &fakeClient{}, nil }
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr, newClient); code != 1 {
		t.Errorf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "usage") {
		t.Errorf("stderr = %q", stderr.String())
	}
	// A single "usage:" line leaves a person guessing at every subcommand
	// name; the old CLI listed them, and this replacement must too.
	for _, name := range []string{"get", "search", "create", "edit", "comment", "transitions", "users"} {
		if !strings.Contains(stderr.String(), name) {
			t.Errorf("stderr does not list subcommand %q: %q", name, stderr.String())
		}
	}
}

// --version and -v must both work, and without ever calling newClient - the
// old CLI answers this on a machine with no credentials configured at all.
func TestVersionFlagBothSpellingsNeverBuildAClient(t *testing.T) {
	for _, arg := range []string{"--version", "-v"} {
		newClientCalled := false
		newClient := func() (jiraClient, error) {
			newClientCalled = true
			return nil, errCredentialsFailure
		}
		var stdout, stderr bytes.Buffer
		code := run([]string{arg}, &stdout, &stderr, newClient)
		if code != 0 {
			t.Errorf("%s: code = %d, stderr = %q", arg, code, stderr.String())
		}
		if newClientCalled {
			t.Errorf("%s: newClient was called", arg)
		}
		if stdout.String() == "" {
			t.Errorf("%s: stdout was empty", arg)
		}
	}
}

// dispatch actually reaches a real subcommand end to end through run, not
// just through the per-command test helpers above.
func TestRunDispatchesGetThroughToTheClient(t *testing.T) {
	fc := &fakeClient{}
	newClient := func() (jiraClient, error) { return fc, nil }
	var stdout, stderr bytes.Buffer
	if code := run([]string{"get", "ABC-1"}, &stdout, &stderr, newClient); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if len(fc.calls) == 0 {
		t.Error("get never reached the client")
	}
}

var errCredentialsFailure = &staticError{"not logged in: run \"jira auth login\""}

type staticError struct{ msg string }

func (e *staticError) Error() string { return e.msg }
