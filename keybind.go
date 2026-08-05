package main

import (
	"strings"
	"text/template"
)

// IssueVars is the whole contract for a user-defined keybinding. It is a flat
// struct of strings so a config author can see exactly what is available.
//
// Every value is already shell-quoted. The rendered command is handed to
// `sh -c`, and Summary is a Jira issue title - free text written by whoever
// filed the issue. Substituting it raw would let a title like `; rm -rf ~` run
// commands on this machine, so quoting happens here rather than being left to
// each config author to remember.
type IssueVars struct {
	IssueKey   string
	IssueURL   string
	Summary    string
	Status     string
	Assignee   string
	ProjectKey string
	// Prompt is the issue plus whatever instruction was typed at the ask prompt.
	// It is empty for a keybinding without prompt: true, so a command that uses
	// it on such a key gets an empty argument rather than a template error - the
	// same shape as an issue with no assignee.
	Prompt string
	// Input is only what was typed, with none of the issue around it. Both exist
	// because the two uses of the box want opposite things: handing an issue to
	// Claude needs the issue included, while posting a comment must send the text
	// alone - the issue is already the thing being commented on, and repeating it
	// into the comment body would publish it to Jira.
	Input string
	// Choice is the value of the entry picked from a choices list, empty for every
	// other key.
	Choice string
	// Dir is the active section's working directory. Commands that spawn something
	// elsewhere need it as an argument rather than as an inherited cwd: `tmux
	// new-window` takes the cwd from -c, not from the process that ran it.
	Dir string
}

// NewIssueVars is what a keybinding without prompt: true receives: the issue,
// and an empty Prompt and Input.
func NewIssueVars(i Issue) IssueVars {
	return NewAskVars(i, "", "")
}

// WithDir returns the vars with the section's working directory filled in.
func (v IssueVars) WithDir(dir string) IssueVars {
	v.Dir = shellQuote(dir)
	return v
}

// WithChoice returns the vars with the picked value filled in. Quoted like every
// other value: a status name has spaces in it on most sites.
func (v IssueVars) WithChoice(value string) IssueVars {
	v.Choice = shellQuote(value)
	return v
}

// NewAskVars is NewIssueVars plus what the ask prompt produced. Both go through
// the same quoting: a prompt carries an issue title and a description, which are
// free text written by whoever filed the issue, and it is about to become a shell
// argument.
func NewAskVars(i Issue, prompt, input string) IssueVars {
	return IssueVars{
		IssueKey:   shellQuote(i.Key),
		IssueURL:   shellQuote(i.URL),
		Summary:    shellQuote(i.Summary),
		Status:     shellQuote(i.Status),
		Assignee:   shellQuote(i.AssigneeName()),
		ProjectKey: shellQuote(i.Project.Key),
		Prompt:     shellQuote(prompt),
		Input:      shellQuote(input),
		// Quoted even when empty, so a command using one in a place that needs an
		// argument gets '' rather than nothing at all and a shifted argv.
		Dir:    shellQuote(""),
		Choice: shellQuote(""),
	}
}

// shellQuote wraps a value in single quotes, which suppresses every form of
// shell interpretation, and closes/reopens the quoting around any single quote
// in the value itself.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// RenderCommand expands a configured command. An unknown variable is an error:
// text/template fails a struct field it cannot find, so a typo in a config
// stops the keybinding instead of handing a shell a command with a hole in it.
func RenderCommand(text string, v IssueVars) (string, error) {
	tmpl, err := template.New("command").Parse(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}
