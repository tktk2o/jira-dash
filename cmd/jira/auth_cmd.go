package main

import "fmt"

// runAuth stubs `jira auth login`/`jira auth status`. Task 9 of the
// migration plan owns writing the real thing (credential storage, the
// login prompt sequence); this task deliberately leaves it unimplemented
// rather than half-building it, per the instructions bounding this work to
// Task 8.
func runAuth(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jira auth <login|status>")
	}
	switch args[0] {
	case "login", "status":
		return fmt.Errorf("jira auth %s is not implemented yet in this CLI; use the old jira-cli, or edit ~/.config/jira-cli/credentials.json directly", args[0])
	default:
		return fmt.Errorf("unknown auth subcommand %q", args[0])
	}
}
