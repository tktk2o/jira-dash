package main

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// runGet implements `jira get <key>`.
func runGet(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	var format, fields string
	fs.StringVar(&format, "f", "table", "output format")
	fs.StringVar(&format, "format", "table", "output format")
	fs.StringVar(&fields, "fields", "", "additional fields (rejected - see the error)")
	flagArgs, positionals := splitArgs(args, map[string]bool{"f": true, "format": true, "fields": true})
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fields != "" {
		// requestedFields (internal/jira) asks for a fixed field set only;
		// there is no seam here to add an arbitrary customfield by name
		// without either guessing at its id or extending internal/jira,
		// both out of this task's scope. Failing loudly and naming the way
		// out beats silently ignoring a flag the caller explicitly set.
		return fmt.Errorf("get --fields is not implemented in this CLI; -f json already includes every field this command fetches")
	}
	f, err := parseGetFormat(format)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: jira get <key>")
	}
	key := positionals[0]

	issue, err := client.Issue(ctx, key)
	if err != nil {
		return err
	}
	description, err := client.IssueDescription(ctx, key)
	if err != nil {
		return err
	}
	return writeGetOutput(stdout, f, issue, description)
}
