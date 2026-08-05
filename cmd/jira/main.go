package main

import (
	"context"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, newClient))
}

// run dispatches one CLI invocation and returns the process exit code. It
// takes newClient as a parameter rather than calling the package function
// directly so main_test.go can drive every subcommand through a fake
// jiraClient without a real credentials file or any network - the
// migration plan's own ban on tests touching either.
func run(args []string, stdout, stderr io.Writer, newClient func() (jiraClient, error)) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: jira <command> [options]")
		return 1
	}

	ctx := context.Background()
	cmd, rest := args[0], args[1:]

	// auth is the one subcommand that must work before any credentials
	// exist, so it is the one exception to "every subcommand gets a
	// client" below.
	if cmd == "auth" {
		if err := runAuth(rest); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	dispatch, ok := subcommands[cmd]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n", cmd)
		return 1
	}

	client, err := newClient()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := dispatch(ctx, client, rest, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// subcommands maps every command name this CLI accepts (besides auth) to
// its handler. A map literal rather than a switch inside run so a new
// subcommand is one line here plus its own file, never a change to run
// itself.
var subcommands = map[string]func(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error{
	"get":         runGet,
	"search":      runSearch,
	"create":      runCreate,
	"edit":        runEdit,
	"comment":     runComment,
	"transitions": runTransitions,
	"users":       runUsers,
}
