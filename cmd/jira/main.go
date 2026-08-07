package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// version is the string `jira --version`/`jira -v` prints. It has no git
// tag or build-info wiring yet (out of this task's scope); the point of
// this defect fix is that the flag exists and exits 0, matching the old
// CLI, not that the number means anything yet.
const version = "jira-dash 0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, newClient))
}

// usageLines is what `jira` with no arguments prints: one line per
// subcommand this binary actually has. Written by hand rather than derived
// from the subcommands map (whose values are handler funcs with no
// description attached) - the old CLI's per-command Japanese blurbs are not
// reused verbatim, per the migration plan's ban on copying that text.
var usageLines = []string{
	"usage: jira <command> [options]",
	"",
	"commands:",
	"  get <key>              show one issue",
	"  search [query]         search issues by JQL or filter flags",
	"  create                 create an issue",
	"  edit <key>             edit an issue's fields, or transition its status",
	"  comment add <key>      add a comment",
	"  comment list <key>     list an issue's comments",
	"  transitions <key>      list the statuses an issue can move to",
	"  users assignable <key> list users who can be assigned an issue",
	"  jql suggest <field> [value] suggest JQL values for a field",
	"  auth login|status      manage credentials",
	"",
	"  -v, --version          print the version and exit",
}

// run dispatches one CLI invocation and returns the process exit code. It
// takes newClient as a parameter rather than calling the package function
// directly so main_test.go can drive every subcommand through a fake
// jiraClient without a real credentials file or any network - the
// migration plan's own ban on tests touching either.
func run(args []string, stdout, stderr io.Writer, newClient func() (jiraClient, error)) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, strings.Join(usageLines, "\n"))
		return 1
	}

	// A bare Background() context has no deadline, so a hung Jira request
	// would hang this process forever too; bound the whole CLI invocation
	// instead, generously enough for jira-dash's slowest command (a paged
	// search) plus the client's own retries.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd, rest := args[0], args[1:]

	// --version/-v must work with no credentials configured, same as auth
	// below: asking a freshly installed machine for the version should
	// never fail on a missing credentials file.
	if cmd == "--version" || cmd == "-v" {
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	}

	// auth is the one subcommand that must work before any credentials
	// exist, so it is the one exception to "every subcommand gets a
	// client" below.
	if cmd == "auth" {
		if err := runAuth(rest, os.Stdin, stdout); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}

	dispatch, ok := subcommands[cmd]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", cmd)
		return 1
	}

	client, err := newClient()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := dispatch(ctx, client, rest, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
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
	"jql":         runJQL,
}
