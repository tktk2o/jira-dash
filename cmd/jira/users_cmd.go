package main

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// runUsersAssignable implements `jira users assignable <key>`, a new
// subcommand per the migration plan - the old workflow was pasting an
// accountId into config by hand.
func runUsersAssignable(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("users assignable", flag.ContinueOnError)
	var query, format string
	fs.StringVar(&query, "q", "", "narrow by name/email substring")
	fs.StringVar(&query, "query", "", "narrow by name/email substring")
	fs.StringVar(&format, "f", "table", "output format")
	fs.StringVar(&format, "format", "table", "output format")
	if err := fs.Parse(args); err != nil {
		return err
	}
	f, err := parseFormat(format)
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: jira users assignable <key>")
	}
	key := fs.Arg(0)

	users, err := client.AssignableUsers(ctx, key, query)
	if err != nil {
		return err
	}
	return writeUsersOutput(stdout, f, users)
}

// runUsers dispatches jira users' one subcommand. A separate FlagSet-per-
// subcommand pattern still applies even with only one leaf, since a second
// one (e.g. "search") is the kind of thing this command could plausibly
// grow.
func runUsers(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jira users assignable <key>")
	}
	switch args[0] {
	case "assignable":
		return runUsersAssignable(ctx, client, args[1:], stdout)
	default:
		return fmt.Errorf("unknown users subcommand %q", args[0])
	}
}
