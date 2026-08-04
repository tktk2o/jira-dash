package main

import (
	"strings"
	"text/template"
)

// IssueVars is the whole contract for a user-defined keybinding. It is a flat
// struct of strings so a config author can see exactly what is available, and
// so a typo fails loudly instead of expanding to nothing.
type IssueVars struct {
	IssueKey   string
	IssueURL   string
	Summary    string
	Status     string
	Assignee   string
	ProjectKey string
}

func NewIssueVars(i Issue) IssueVars {
	return IssueVars{
		IssueKey:   i.Key,
		IssueURL:   i.URL,
		Summary:    i.Summary,
		Status:     i.Status,
		Assignee:   i.AssigneeName(),
		ProjectKey: i.Project.Key,
	}
}

// RenderCommand expands a configured command. Option "missingkey=error" is
// deliberate: silently dropping an unknown variable would build a shell
// command with a hole in it.
func RenderCommand(text string, v IssueVars) (string, error) {
	tmpl, err := template.New("command").Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}
