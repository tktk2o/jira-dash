package main

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// runTransitions implements `jira transitions <key>`, a new subcommand per
// the migration plan (the old CLI had no equivalent - a transition could
// only be triggered blind, through `jira edit -S <status>`).
func runTransitions(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("transitions", flag.ContinueOnError)
	var format string
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
		return fmt.Errorf("usage: jira transitions <key>")
	}
	key := fs.Arg(0)

	transitions, err := client.Transitions(ctx, key)
	if err != nil {
		return err
	}
	return writeTransitionsOutput(stdout, f, transitions)
}
