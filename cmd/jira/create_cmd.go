package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	jirapkg "jira-dash/internal/jira"
)

// createFlags holds every flag `jira create` parses. A struct rather than
// loose locals because buildCreateEdit and dry-run preview both need the
// same set, and passing eleven separate strings around invites transposing
// two of them.
type createFlags struct {
	project, issueType, summary                                string
	description, descriptionFile, editor                       string
	assignee, priority, labels, parent, sprint                 string
	storyPoints, team, components, startDate, targetStart, due string
	fixVersions                                                string
	fieldsJSON, fieldsJSONFile                                 string
	dryRun                                                     bool
	format                                                     string
}

func parseCreateFlags(args []string) (createFlags, error) {
	var f createFlags
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.StringVar(&f.project, "p", "", "project key")
	fs.StringVar(&f.project, "project", "", "project key")
	fs.StringVar(&f.issueType, "t", "", "issue type")
	fs.StringVar(&f.issueType, "type", "", "issue type")
	fs.StringVar(&f.summary, "s", "", "summary")
	fs.StringVar(&f.summary, "summary", "", "summary")
	fs.StringVar(&f.description, "d", "", "description (Markdown)")
	fs.StringVar(&f.description, "description", "", "description (Markdown)")
	fs.StringVar(&f.descriptionFile, "D", "", "read the description from a file (- for stdin)")
	fs.StringVar(&f.descriptionFile, "description-file", "", "read the description from a file (- for stdin)")
	fs.StringVar(&f.assignee, "a", "", "assignee account id")
	fs.StringVar(&f.assignee, "assignee", "", "assignee account id")
	fs.StringVar(&f.priority, "priority", "", "priority")
	fs.StringVar(&f.labels, "l", "", "labels, comma-separated")
	fs.StringVar(&f.labels, "labels", "", "labels, comma-separated")
	fs.StringVar(&f.parent, "parent", "", "parent issue key")
	fs.StringVar(&f.sprint, "S", "", "sprint name or id")
	fs.StringVar(&f.sprint, "sprint", "", "sprint name or id")
	fs.StringVar(&f.editor, "e", "", "open $EDITOR (or this command) to write the description")
	fs.StringVar(&f.editor, "editor", "", "open $EDITOR (or this command) to write the description")
	fs.StringVar(&f.fieldsJSON, "fields-json", "", "extra fields as a JSON object")
	fs.StringVar(&f.fieldsJSONFile, "fields-json-file", "", "extra fields as a JSON file")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print the request instead of sending it")
	fs.StringVar(&f.storyPoints, "story-points", "", "story points")
	fs.StringVar(&f.team, "team", "", "team id")
	fs.StringVar(&f.components, "components", "", "components, comma-separated")
	fs.StringVar(&f.startDate, "start-date", "", "start date (YYYY-MM-DD)")
	fs.StringVar(&f.targetStart, "target-start", "", "target start date (YYYY-MM-DD)")
	fs.StringVar(&f.due, "due", "", "due date (YYYY-MM-DD)")
	fs.StringVar(&f.fixVersions, "fix-versions", "", "fix versions, comma-separated")
	fs.StringVar(&f.format, "f", "table", "output format")
	fs.StringVar(&f.format, "format", "table", "output format")
	if err := fs.Parse(args); err != nil {
		return createFlags{}, err
	}
	if fs.NArg() != 0 {
		return createFlags{}, fmt.Errorf("create takes no positional arguments")
	}
	return f, nil
}

// resolveDescription applies -d/-D/-e in the old CLI's own priority: an
// inline -d wins outright, then a file (or stdin via "-"), and only then an
// editor - the slowest of the three, and the only one that blocks on a
// person.
func resolveDescription(description, descriptionFile, editor string) (string, error) {
	if description != "" {
		return description, nil
	}
	if descriptionFile != "" {
		return readFileOrStdin(descriptionFile)
	}
	if editor != "" || editorCommand(editor) != "" {
		return openInEditor(editorCommand(editor), "")
	}
	return "", nil
}

// postCreateEdit builds the Edit this program sends right after create
// succeeds, carrying every create flag NewIssue itself has no field for
// (internal/jira's NewIssue only covers project/type/summary/description/
// sprint - see create.go's own comment on why). Edit's pointer fields
// happen to name almost exactly this same flag set, since edit and create
// share most of an issue's editable surface, so no new mapping had to be
// invented here.
func postCreateEdit(f createFlags) (jirapkg.Edit, bool) {
	var e jirapkg.Edit
	var hasAny bool
	set := func(dst **string, v string) {
		if v != "" {
			dst2 := v
			*dst = &dst2
			hasAny = true
		}
	}
	set(&e.Assignee, f.assignee)
	set(&e.Priority, f.priority)
	set(&e.Labels, f.labels)
	set(&e.Parent, f.parent)
	set(&e.StoryPoints, f.storyPoints)
	set(&e.Team, f.team)
	set(&e.Components, f.components)
	set(&e.StartDate, f.startDate)
	set(&e.TargetStart, f.targetStart)
	set(&e.Due, f.due)
	set(&e.FixVersions, f.fixVersions)
	return e, hasAny
}

// runCreate implements `jira create`.
func runCreate(ctx context.Context, client jiraClient, args []string, stdout io.Writer) error {
	f, err := parseCreateFlags(args)
	if err != nil {
		return err
	}
	format, err := parseFormat(f.format)
	if err != nil {
		return err
	}
	if f.project == "" || f.issueType == "" || f.summary == "" {
		return fmt.Errorf("create needs -p/--project, -t/--type and -s/--summary")
	}

	description, err := resolveDescription(f.description, f.descriptionFile, f.editor)
	if err != nil {
		return err
	}

	extraFields, err := parseFieldsJSON(f.fieldsJSON, f.fieldsJSONFile)
	if err != nil {
		return err
	}

	edit, hasEdit := postCreateEdit(f)

	if f.dryRun {
		printCreateDryRun(stdout, f, description, extraFields)
		return nil
	}
	if len(extraFields) > 0 {
		// See parseFieldsJSON's caller in edit_cmd.go for the same
		// limitation: neither Client.Create nor Client.Edit takes an
		// arbitrary field map, only the typed fields above, so there is no
		// path to actually send these once dry-run is off.
		return fmt.Errorf("--fields-json / --fields-json-file cannot be sent yet: internal/jira has no generic extra-fields path (dry-run still shows them)")
	}

	issue, err := client.Create(ctx, jirapkg.NewIssue{
		ProjectKey:  f.project,
		Type:        f.issueType,
		Summary:     f.summary,
		Description: description,
		Sprint:      f.sprint,
	})
	if err != nil {
		return err
	}

	if hasEdit {
		if err := client.Edit(ctx, issue.Key, edit); err != nil {
			// The issue itself was created; only the extra fields failed.
			// Naming the key here is the difference between "nothing
			// happened" and "go finish this by hand".
			return fmt.Errorf("created %s, but applying the remaining fields failed: %w", issue.Key, err)
		}
		if refreshed, _, err := client.IssueWithDescription(ctx, issue.Key); err == nil {
			issue = refreshed
		}
	}

	return writeCreateOutput(stdout, format, issue)
}

// printCreateDryRun shows what would be sent without sending it: the base
// fields NewIssue would carry (customfield ids not resolved - that needs a
// network call, which --dry-run must not make, per the migration plan),
// the fields a follow-up Edit would carry, and any --fields-json content.
func printCreateDryRun(w io.Writer, f createFlags, description string, extraFields map[string]any) {
	fmt.Fprintf(w, "POST /issue (project=%s, issuetype=%s)\n", f.project, f.issueType)
	fmt.Fprintf(w, "  summary: %s\n", f.summary)
	if description != "" {
		fmt.Fprintln(w, "  description: <markdown, sent as ADF>")
	}
	if f.sprint != "" {
		fmt.Fprintf(w, "  sprint: %s (name/id resolved to a board sprint at send time)\n", f.sprint)
	}
	if edit, hasEdit := postCreateEdit(f); hasEdit {
		fmt.Fprintln(w, "then PUT /issue/<new key> with:")
		printEditPreview(w, edit)
	}
	if len(extraFields) > 0 {
		fmt.Fprintln(w, "--fields-json would merge (not currently sendable):")
		for k, v := range extraFields {
			fmt.Fprintf(w, "  %s: %v\n", k, v)
		}
	}
}
