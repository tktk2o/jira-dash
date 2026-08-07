package main

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// runJQLSuggest implements `jira jql suggest <fieldName> [fieldValue]`, a
// new subcommand per the migration plan: the old workflow had no way to
// discover a field's valid values short of guessing or reading Jira's own
// web UI.
func runJQLSuggest(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("jql suggest", flag.ContinueOnError)
	var format string
	fs.StringVar(&format, "f", "table", "output format")
	fs.StringVar(&format, "format", "table", "output format")
	flagArgs, positionals := splitArgs(args, map[string]bool{"f": true, "format": true})
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	f, err := parseFormat(format)
	if err != nil {
		return err
	}
	if len(positionals) < 1 || len(positionals) > 2 {
		return fmt.Errorf("usage: jira jql suggest <fieldName> [fieldValue]")
	}
	fieldName := positionals[0]
	var fieldValue string
	if len(positionals) == 2 {
		fieldValue = positionals[1]
	}

	suggestions, err := client.JQLSuggestions(ctx, fieldName, fieldValue)
	if err != nil {
		return err
	}
	return writeJQLSuggestOutput(stdout, f, suggestions)
}

// runJQL dispatches jira jql's one subcommand. A separate FlagSet-per-
// subcommand pattern still applies even with only one leaf, per
// runUsers' own note on why.
func runJQL(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jira jql suggest <fieldName> [fieldValue]")
	}
	switch args[0] {
	case "suggest":
		return runJQLSuggest(ctx, client, args[1:], stdout)
	default:
		return fmt.Errorf("unknown jql subcommand %q", args[0])
	}
}
