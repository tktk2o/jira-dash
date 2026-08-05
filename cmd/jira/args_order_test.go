package main

import (
	"bytes"
	"context"
	"testing"
)

// Go's flag package stops parsing at the first token that does not start
// with "-", so `get ABC-1 -f json` never even sees -f: the positional key
// silently swallows every flag after it. That order - key first, flags
// after - is the one used throughout this repo's own config, README, and
// keybindings, so every subcommand with a positional must accept it as
// readily as the flags-first order flag.Parse handles natively.
//
// This test drives each affected subcommand's handler both ways and checks
// the same fakeClient call happened either way, rather than checking
// output text, so it catches "silently produced no error but also did
// nothing" as well as an outright parse failure.
func TestSubcommandsAcceptFlagsBeforeOrAfterThePositional(t *testing.T) {
	tests := []struct {
		name       string
		beforeArgs []string
		key        string
		run        func(fc *fakeClient, args []string) error
		wantCall   string
	}{
		{
			name: "get", beforeArgs: []string{"-f", "json"}, key: "ABC-1",
			run: func(fc *fakeClient, args []string) error {
				return runGet(context.Background(), fc, args, &bytes.Buffer{})
			},
			wantCall: "IssueWithDescription",
		},
		{
			name: "search", beforeArgs: []string{"-l", "5"}, key: "payment bug",
			run: func(fc *fakeClient, args []string) error {
				return runSearch(context.Background(), fc, args, &bytes.Buffer{})
			},
			wantCall: "Search",
		},
		{
			name: "edit", beforeArgs: []string{"-s", "New title"}, key: "ABC-1",
			run: func(fc *fakeClient, args []string) error {
				return runEdit(context.Background(), fc, args, &bytes.Buffer{})
			},
			wantCall: "Edit",
		},
		{
			name: "comment add", beforeArgs: []string{"add", "-b", "hello"}, key: "ABC-1",
			run: func(fc *fakeClient, args []string) error {
				return runComment(context.Background(), fc, args, &bytes.Buffer{})
			},
			wantCall: "AddComment",
		},
		{
			name: "comment list", beforeArgs: []string{"list", "-m", "5"}, key: "ABC-1",
			run: func(fc *fakeClient, args []string) error {
				return runComment(context.Background(), fc, args, &bytes.Buffer{})
			},
			wantCall: "Comments",
		},
		{
			name: "transitions", beforeArgs: []string{"-f", "json"}, key: "ABC-1",
			run: func(fc *fakeClient, args []string) error {
				return runTransitions(context.Background(), fc, args, &bytes.Buffer{})
			},
			wantCall: "Transitions",
		},
		{
			name: "users assignable", beforeArgs: []string{"assignable", "-q", "ada"}, key: "ABC-1",
			run: func(fc *fakeClient, args []string) error {
				return runUsers(context.Background(), fc, args, &bytes.Buffer{})
			},
			wantCall: "AssignableUsers",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flagsFirst := append(append([]string{}, tc.beforeArgs...), tc.key)
			positionalFirst := append(append([]string{}, tc.beforeArgs...), tc.key)
			// Move the key to the front: beforeArgs may itself start with a
			// subcommand word ("add"/"list"/"assignable") that must stay
			// first, so the key is inserted right after that word rather
			// than at index 0.
			positionalFirst = reorderKeyFirst(tc.beforeArgs, tc.key)

			for _, variant := range []struct {
				label string
				args  []string
			}{
				{"flags before the positional", flagsFirst},
				{"the positional before flags", positionalFirst},
			} {
				fc := &fakeClient{}
				if err := tc.run(fc, variant.args); err != nil {
					t.Fatalf("%s: %s: %v (args=%v)", tc.name, variant.label, err, variant.args)
				}
				found := false
				for _, c := range fc.calls {
					if c == tc.wantCall {
						found = true
					}
				}
				if !found {
					t.Errorf("%s: %s: client calls = %v, want %q among them (args=%v)", tc.name, variant.label, fc.calls, tc.wantCall, variant.args)
				}
			}
		})
	}
}

// reorderKeyFirst rebuilds beforeArgs with key placed immediately after any
// leading subcommand word (a token that does not start with "-"), so the
// positional argument comes before the flags that follow it - the order
// this whole test exists to prove works.
func reorderKeyFirst(beforeArgs []string, key string) []string {
	i := 0
	for i < len(beforeArgs) && len(beforeArgs[i]) > 0 && beforeArgs[i][0] != '-' {
		i++
	}
	out := append([]string{}, beforeArgs[:i]...)
	out = append(out, key)
	out = append(out, beforeArgs[i:]...)
	return out
}
