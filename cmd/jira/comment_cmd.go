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
		_, _ = fmt.Fprintf(stdout, "POST /issue/%s/comment\nbody (Markdown, sent as ADF):\n%s\n", key, resolvedBody)
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
	if max < 1 {
		return fmt.Errorf("comment list --max-results must be positive")
	}
	key := positionals[0]

	comments, err := client.Comments(ctx, key, max)
	if err != nil {
		return err
	}
	return writeCommentListOutput(stdout, key, comments)
}

// runCommentEdit implements `jira comment edit <key> <comment-id>`. It takes
// the same body flags as add, since the two differ only in whether Jira is
// asked to create a comment or overwrite one.
func runCommentEdit(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("comment edit", flag.ContinueOnError)
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
	if len(positionals) != 2 {
		return fmt.Errorf("usage: jira comment edit <key> <comment-id>")
	}
	key, commentID := positionals[0], positionals[1]

	resolvedBody, err := resolveCommentBody(body, bodyFile, editor)
	if err != nil {
		return err
	}
	if resolvedBody == "" {
		return fmt.Errorf("comment edit needs a body: pass -b, -B, or -e")
	}

	if dryRun {
		// An edit replaces the whole body - Jira has no partial update here -
		// so what this prints is the comment's entire new text, not a patch.
		_, _ = fmt.Fprintf(
			stdout,
			"PUT /issue/%s/comment/%s\nbody (Markdown, sent as ADF):\n%s\n",
			key, commentID, resolvedBody,
		)
		return nil
	}

	comment, err := client.EditComment(ctx, key, commentID, resolvedBody)
	if err != nil {
		return err
	}
	return writeCommentEditOutput(stdout, f, comment)
}

// runComment dispatches jira comment's subcommands. "delete" is still out of
// scope per the migration plan's own scope table - unlike an edit, there is
// nothing to review before the text is gone.
func runComment(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jira comment <add|edit|list>")
	}
	switch args[0] {
	case "add":
		return runCommentAdd(ctx, client, args[1:], stdout)
	case "edit":
		return runCommentEdit(ctx, client, args[1:], stdout)
	case "list":
		return runCommentList(ctx, client, args[1:], stdout)
	default:
		return fmt.Errorf("unknown comment subcommand %q", args[0])
	}
}
