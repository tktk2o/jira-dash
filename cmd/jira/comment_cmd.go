package main

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// runCommentAdd implements `jira comment add <key>`.
func runCommentAdd(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("comment add", flag.ContinueOnError)
	var body, bodyFile, editor, format string
	var dryRun bool
	fs.StringVar(&body, "b", "", "comment body (Markdown)")
	fs.StringVar(&body, "body", "", "comment body (Markdown)")
	fs.StringVar(&bodyFile, "B", "", "read the body from a file (- for stdin)")
	fs.StringVar(&bodyFile, "body-file", "", "read the body from a file (- for stdin)")
	fs.StringVar(&editor, "e", "", "open $EDITOR (or this command) to write the body")
	fs.StringVar(&editor, "editor", "", "open $EDITOR (or this command) to write the body")
	fs.BoolVar(&dryRun, "dry-run", false, "print the request instead of sending it")
	fs.StringVar(&format, "f", "table", "output format")
	fs.StringVar(&format, "format", "table", "output format")
	flagArgs, positionals := splitArgs(args, map[string]bool{
		"b": true, "body": true, "B": true, "body-file": true,
		"e": true, "editor": true, "f": true, "format": true,
		// "dry-run" takes no value.
	})
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	f, err := parseFormat(format)
	if err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: jira comment add <key>")
	}
	key := positionals[0]

	resolvedBody, err := resolveCommentBody(body, bodyFile, editor)
	if err != nil {
		return err
	}
	if resolvedBody == "" {
		return fmt.Errorf("comment add needs a body: pass -b, -B, or -e")
	}

	if dryRun {
		// Markdown is shown as-is rather than run through the ADF converter
		// AddComment uses internally (markdownToADF is unexported in
		// internal/jira) - this is the text that would be sent, in the form
		// the caller wrote it, which is enough to review before sending for
		// real.
		fmt.Fprintf(stdout, "POST /issue/%s/comment\nbody (markdown, sent as ADF):\n%s\n", key, resolvedBody)
		return nil
	}

	comment, err := client.AddComment(ctx, key, resolvedBody)
	if err != nil {
		return err
	}
	return writeCommentAddOutput(stdout, f, comment)
}

// resolveCommentBody picks one of -b/-B/-e, in that priority order, matching
// the old CLI: an explicit -b wins over a file, and -e (open an editor) is
// only used once both are absent since it is the slowest of the three.
func resolveCommentBody(body, bodyFile, editor string) (string, error) {
	if body != "" {
		return body, nil
	}
	if bodyFile != "" {
		return readFileOrStdin(bodyFile)
	}
	if editor != "" || editorCommand(editor) != "" {
		return openInEditor(editorCommand(editor), "")
	}
	return "", nil
}

// runCommentList implements `jira comment list <key>`. The old CLI never
// gave this subcommand a -f flag - its --help lists only -m/--max-results -
// so JSON is the only output this prints, matching that.
func runCommentList(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("comment list", flag.ContinueOnError)
	var max int
	fs.IntVar(&max, "m", 50, "max results")
	fs.IntVar(&max, "max-results", 50, "max results")
	flagArgs, positionals := splitArgs(args, map[string]bool{"m": true, "max-results": true})
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if len(positionals) != 1 {
		return fmt.Errorf("usage: jira comment list <key>")
	}
	key := positionals[0]

	comments, err := client.Comments(ctx, key, max)
	if err != nil {
		return err
	}
	return writeCommentListOutput(stdout, key, comments)
}

// runComment dispatches jira comment's two subcommands ("edit"/"delete" are
// out of scope per the migration plan's own scope table).
func runComment(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jira comment <add|list> ...")
	}
	switch args[0] {
	case "add":
		return runCommentAdd(ctx, client, args[1:], stdout)
	case "list":
		return runCommentList(ctx, client, args[1:], stdout)
	default:
		return fmt.Errorf("unknown comment subcommand %q", args[0])
	}
}
