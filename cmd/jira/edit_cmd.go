package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	jirapkg "jira-dash/internal/jira"
)

// editFlags holds every flag `jira edit` parses, mirroring createFlags.
type editFlags struct {
	summary, description, descriptionFile, editor              string
	assignee, priority, labels, parent, sprint, status         string
	storyPoints, team, components, startDate, targetStart, due string
	fixVersions                                                string
	flag, noFlag                                               bool
	fieldsJSON, fieldsJSONFile                                 string
	dryRun                                                     bool
	format                                                     string
}

func parseEditFlags(args []string) (editFlags, string, error) {
	var f editFlags
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	fs.StringVar(&f.summary, "s", "", "new summary")
	fs.StringVar(&f.summary, "summary", "", "new summary")
	fs.StringVar(&f.description, "d", "", "new description (Markdown)")
	fs.StringVar(&f.description, "description", "", "new description (Markdown)")
	fs.StringVar(&f.descriptionFile, "D", "", "read the description from a file (- for stdin)")
	fs.StringVar(&f.descriptionFile, "description-file", "", "read the description from a file (- for stdin)")
	fs.StringVar(&f.assignee, "a", "", `assignee account id ("null" to clear)`)
	fs.StringVar(&f.assignee, "assignee", "", `assignee account id ("null" to clear)`)
	fs.StringVar(&f.priority, "priority", "", `priority ("null" to clear)`)
	fs.StringVar(&f.labels, "l", "", "labels, comma-separated")
	fs.StringVar(&f.labels, "labels", "", "labels, comma-separated")
	fs.StringVar(&f.parent, "parent", "", `parent issue key ("null" to clear)`)
	fs.StringVar(&f.sprint, "sprint", "", `sprint name/id ("null" to clear)`)
	fs.StringVar(&f.status, "S", "", "status to transition to")
	fs.StringVar(&f.status, "status", "", "status to transition to")
	fs.StringVar(&f.editor, "e", "", "open $EDITOR (or this command) to write the description")
	fs.StringVar(&f.editor, "editor", "", "open $EDITOR (or this command) to write the description")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print the request instead of sending it")
	fs.StringVar(&f.storyPoints, "story-points", "", `story points ("null" to clear)`)
	fs.StringVar(&f.team, "team", "", `team id ("null" to clear)`)
	fs.StringVar(&f.components, "components", "", `components, comma-separated ("null" to clear)`)
	fs.StringVar(&f.startDate, "start-date", "", `start date ("null" to clear)`)
	fs.StringVar(&f.targetStart, "target-start", "", `target start date ("null" to clear)`)
	fs.StringVar(&f.due, "due", "", `due date ("null" to clear)`)
	fs.StringVar(&f.fixVersions, "fix-versions", "", `fix versions, comma-separated ("null" to clear)`)
	fs.BoolVar(&f.flag, "flag", false, "set the flag (Impediment)")
	fs.BoolVar(&f.noFlag, "no-flag", false, "clear the flag")
	fs.StringVar(&f.fieldsJSON, "fields-json", "", "extra fields as a JSON object")
	fs.StringVar(&f.fieldsJSONFile, "fields-json-file", "", "extra fields as a JSON file")
	fs.StringVar(&f.format, "f", "table", "output format")
	fs.StringVar(&f.format, "format", "table", "output format")
	flagArgs, positionals := splitArgs(args, map[string]bool{
		"s": true, "summary": true, "d": true, "description": true,
		"D": true, "description-file": true, "a": true, "assignee": true,
		"priority": true, "l": true, "labels": true, "parent": true,
		"sprint": true, "S": true, "status": true, "e": true, "editor": true,
		"story-points": true, "team": true, "components": true,
		"start-date": true, "target-start": true, "due": true,
		"fix-versions": true, "fields-json": true, "fields-json-file": true,
		"f": true, "format": true,
		// "dry-run", "flag", "no-flag" take no value.
	})
	if err := fs.Parse(flagArgs); err != nil {
		return editFlags{}, "", err
	}
	if len(positionals) != 1 {
		return editFlags{}, "", fmt.Errorf("usage: jira edit <key>")
	}
	return f, positionals[0], nil
}

// resolveEditDescription mirrors resolveDescription but returns nil when
// nothing was given at all, versus a pointer to "" - Edit needs to tell
// "untouched" apart from "clear it", and only the caller (not this
// function) knows whether the caller ever typed anything.
func resolveEditDescription(f editFlags) (*string, error) {
	switch {
	case f.description != "":
		d := f.description
		return &d, nil
	case f.descriptionFile != "":
		d, err := readFileOrStdin(f.descriptionFile)
		if err != nil {
			return nil, err
		}
		return &d, nil
	case f.editor != "" || editorCommand(f.editor) != "":
		d, err := openInEditor(editorCommand(f.editor), "")
		if err != nil {
			return nil, err
		}
		return &d, nil
	default:
		return nil, nil
	}
}

// buildEdit turns editFlags into an internal/jira.Edit. Every *string field
// here takes the flag's raw value straight through, including the literal
// "null": Edit.isClear already treats that string as "clear this field",
// which is exactly the contract the old CLI's own "null"-as-a-value
// convention describes (see edit.go's doc comment on Edit).
func buildEdit(f editFlags) (jirapkg.Edit, error) {
	var e jirapkg.Edit
	set := func(dst **string, v string) {
		if v != "" {
			dst2 := v
			*dst = &dst2
		}
	}
	set(&e.Summary, f.summary)
	set(&e.Assignee, f.assignee)
	set(&e.Priority, f.priority)
	set(&e.Labels, f.labels)
	set(&e.Parent, f.parent)
	set(&e.Sprint, f.sprint)
	set(&e.StoryPoints, f.storyPoints)
	set(&e.Team, f.team)
	set(&e.Components, f.components)
	set(&e.StartDate, f.startDate)
	set(&e.TargetStart, f.targetStart)
	set(&e.Due, f.due)
	set(&e.FixVersions, f.fixVersions)

	description, err := resolveEditDescription(f)
	if err != nil {
		return jirapkg.Edit{}, err
	}
	e.Description = description

	if f.flag && f.noFlag {
		return jirapkg.Edit{}, fmt.Errorf("--flag and --no-flag are mutually exclusive")
	}
	if f.flag {
		v := true
		e.Flag = &v
	} else if f.noFlag {
		v := false
		e.Flag = &v
	}
	return e, nil
}

// hasEditFields reports whether e carries any change at all - `jira edit
// <key>` with no flags is a no-op, same as the old CLI, and should say so
// rather than sending an empty PUT.
func hasEditFields(e jirapkg.Edit) bool {
	return e.Summary != nil || e.Description != nil || e.Assignee != nil ||
		e.Priority != nil || e.Labels != nil || e.Parent != nil ||
		e.Sprint != nil || e.StoryPoints != nil || e.Team != nil ||
		e.StartDate != nil || e.TargetStart != nil || e.Due != nil ||
		e.FixVersions != nil || e.Components != nil || e.Flag != nil
}

// runEdit implements `jira edit <key>`.
func runEdit(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	f, key, err := parseEditFlags(args)
	if err != nil {
		return err
	}
	format, err := parseFormat(f.format)
	if err != nil {
		return err
	}

	edit, err := buildEdit(f)
	if err != nil {
		return err
	}
	extraFields, err := parseFieldsJSON(f.fieldsJSON, f.fieldsJSONFile)
	if err != nil {
		return err
	}
	if !hasEditFields(edit) && f.status == "" && len(extraFields) == 0 {
		return fmt.Errorf("edit needs at least one field flag or --status")
	}

	if f.dryRun {
		printEditDryRun(stdout, key, edit, f.status, extraFields)
		return nil
	}
	if len(extraFields) > 0 {
		// Same limitation as create_cmd.go: Client.Edit takes only the
		// typed Edit fields above, never an arbitrary map, so there is no
		// path to send --fields-json for real yet.
		return fmt.Errorf("--fields-json / --fields-json-file cannot be sent yet: internal/jira has no generic extra-fields path (dry-run still shows them)")
	}

	if hasEditFields(edit) {
		if err := client.Edit(ctx, key, edit); err != nil {
			return err
		}
	}
	if f.status != "" {
		if err := client.Transition(ctx, key, f.status); err != nil {
			return err
		}
	}
	return writeEditOutput(stdout, format, key)
}

// printEditPreview writes edit's would-be PUT /issue/<key> fields body.
// Shared by edit's own --dry-run and create's follow-up Edit preview, since
// both show exactly the same shape.
func printEditPreview(w io.Writer, e jirapkg.Edit) {
	print := func(name string, v *string) {
		if v == nil {
			return
		}
		fmt.Fprintf(w, "  %s: %s\n", name, *v)
	}
	print("summary", e.Summary)
	print("description", e.Description)
	print("assignee", e.Assignee)
	print("priority", e.Priority)
	print("labels", e.Labels)
	print("parent", e.Parent)
	print("sprint", e.Sprint)
	print("story_points", e.StoryPoints)
	print("team", e.Team)
	print("components", e.Components)
	print("start_date", e.StartDate)
	print("target_start", e.TargetStart)
	print("due", e.Due)
	print("fix_versions", e.FixVersions)
	if e.Flag != nil {
		fmt.Fprintf(w, "  flag: %v\n", *e.Flag)
	}
}

// printEditDryRun shows what `jira edit` would send without sending it -
// including --status, which is a separate POST /issue/<key>/transitions
// call this program makes after the PUT, never merged into it.
func printEditDryRun(w io.Writer, key string, e jirapkg.Edit, status string, extraFields map[string]any) {
	if hasEditFields(e) {
		fmt.Fprintf(w, "PUT /issue/%s with:\n", key)
		printEditPreview(w, e)
	}
	if status != "" {
		fmt.Fprintf(w, "POST /issue/%s/transitions -> %s\n", key, status)
	}
	if len(extraFields) > 0 {
		fmt.Fprintln(w, "--fields-json would merge (not currently sendable):")
		for k, v := range extraFields {
			fmt.Fprintf(w, "  %s: %v\n", k, v)
		}
	}
}
