package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `
jiraSections:
  - title: My Issues
    jql: assignee = currentUser()
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := cfg.Sections[0].Limit; got != 20 {
		t.Errorf("section limit = %d, want the default 20", got)
	}
	if cfg.Defaults.Preview.Open == nil || !*cfg.Defaults.Preview.Open {
		t.Error("preview should default to open")
	}
	if got := cfg.Defaults.Preview.Position; got != "right" {
		t.Errorf("preview position = %q, want right", got)
	}
	if got := cfg.Defaults.Preview.Width; got != 0.5 {
		t.Errorf("preview width = %v, want 0.5", got)
	}
}

func TestLoadConfigKeepsExplicitPreviewClosed(t *testing.T) {
	path := writeConfig(t, `
jiraSections:
  - title: My Issues
    jql: assignee = currentUser()
defaults:
  preview:
    open: false
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Defaults.Preview.Open == nil || *cfg.Defaults.Preview.Open {
		t.Error("open: false must survive defaulting")
	}
}

func TestLoadConfigRejectsInvalidNumericAndEnumSettings(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
	}{
		{"negative section limit", "    limit: -1\n"},
		{"negative default limit", "defaults:\n  limit: -1\n"},
		{"preview width outside ratio", "defaults:\n  preview:\n    width: 1.2\n"},
		{"unsupported preview position", "defaults:\n  preview:\n    position: left\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "jiraSections:\n  - title: Mine\n    jql: project = ABC\n" + tc.extra
			if _, err := LoadConfig(writeConfig(t, body)); err == nil {
				t.Fatal("expected invalid config to be rejected")
			}
		})
	}
}

func TestLoadConfigSectionLimitOverridesDefault(t *testing.T) {
	path := writeConfig(t, `
jiraSections:
  - title: A
    jql: project = ABC
    limit: 5
defaults:
  limit: 30
`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := cfg.Sections[0].Limit; got != 5 {
		t.Errorf("section limit = %d, want 5", got)
	}
}

func TestLoadConfigRejectsSectionWithoutJQL(t *testing.T) {
	path := writeConfig(t, `
jiraSections:
  - title: Broken
`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "Broken") {
		t.Errorf("error should name the offending section, got: %v", err)
	}
}

func TestLoadConfigRejectsEmptySections(t *testing.T) {
	if _, err := LoadConfig(writeConfig(t, "defaults:\n  limit: 10\n")); err == nil {
		t.Fatal("want an error for an empty jiraSections")
	}
}

func TestLoadConfigMissingFileMentionsSetup(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "absent.yml"))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "config.yml.example") {
		t.Errorf("error should point at the seed file, got: %v", err)
	}
}

// The two create keys are config, not code: which issue types a site offers is
// site-specific, and on a Japanese site they are named in Japanese.
func TestLoadConfigReadsCreateTypes(t *testing.T) {
	path := writeConfig(t, `
jiraSections:
  - title: Mine
    jql: assignee = currentUser()
create:
  - key: c
    type: Task
  - key: C
    type: Story
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Create) != 2 {
		t.Fatalf("create entries = %d, want 2", len(cfg.Create))
	}
	if cfg.Create[0].Key != "c" || cfg.Create[0].Type != "Task" {
		t.Errorf("first entry = %+v", cfg.Create[0])
	}
}

// A create key that shadows a navigation key would make the dashboard
// unusable in a way that is hard to diagnose, so it is rejected at load.
func TestLoadConfigRejectsACreateKeyThatShadowsNavigation(t *testing.T) {
	path := writeConfig(t, `
jiraSections:
  - title: Mine
    jql: assignee = currentUser()
create:
  - key: j
    type: Task
`)
	if _, err := LoadConfig(path); err == nil {
		t.Error("a create key of j should be rejected")
	}
}

// An entry with no type would send `jira create -t ""`.
func TestLoadConfigRejectsACreateEntryWithoutAType(t *testing.T) {
	path := writeConfig(t, `
jiraSections:
  - title: Mine
    jql: assignee = currentUser()
create:
  - key: c
`)
	if _, err := LoadConfig(path); err == nil {
		t.Error("a create entry with no type should be rejected")
	}
}

// A misspelled key used to unmarshal into nothing at all, so the feature it was
// meant to configure looked broken while the config looked fine.
func TestLoadConfigRejectsAnUnknownKey(t *testing.T) {
	path := writeConfig(t, `
jiraSections:
  - title: Mine
    jql: assignee = currentUser()
    limmit: 5
`)
	if _, err := LoadConfig(path); err == nil {
		t.Error("a misspelled section key should be rejected")
	}
}

// handleKey tries its own switch first, so a keybinding on j would never run.
// Silently losing is worse than refusing to start.
func TestLoadConfigRejectsAKeybindingOnADashboardKey(t *testing.T) {
	path := writeConfig(t, `
jiraSections:
  - title: Mine
    jql: assignee = currentUser()
keybindings:
  issues:
    - key: j
      command: open {{.IssueURL}}
`)
	if _, err := LoadConfig(path); err == nil {
		t.Error("a keybinding on j should be rejected")
	}
}

// Create keys are tried before issue keybindings, so the keybinding on the same
// key is dead code the config author cannot see.
func TestLoadConfigRejectsTwoEntriesOnTheSameKey(t *testing.T) {
	path := writeConfig(t, `
jiraSections:
  - title: Mine
    jql: assignee = currentUser()
create:
  - key: c
    type: Task
keybindings:
  issues:
    - key: c
      command: open {{.IssueURL}}
`)
	if _, err := LoadConfig(path); err == nil {
		t.Error("a key claimed twice should be rejected")
	}
}

// A keybinding with no command would hand `sh -c` an empty string.
func TestLoadConfigRejectsAKeybindingWithoutACommand(t *testing.T) {
	path := writeConfig(t, `
jiraSections:
  - title: Mine
    jql: assignee = currentUser()
keybindings:
  issues:
    - key: o
`)
	if _, err := LoadConfig(path); err == nil {
		t.Error("a keybinding with no command should be rejected")
	}
}

// A board belongs to a repository, so the working directory is per-section - but
// when every tab looks at the same checkout, defaults.dir should cover them.
func TestSectionDirFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	cfg, err := LoadConfig(writeConfig(t, "defaults:\n  dir: "+dir+"\njiraSections:\n"+
		"  - title: A\n    jql: project = A\n"+
		"  - title: B\n    jql: project = B\n    dir: "+other+"\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := cfg.Sections[0].Dir; got != dir {
		t.Errorf("section A dir = %q, want the default %q", got, dir)
	}
	if got := cfg.Sections[1].Dir; got != other {
		t.Errorf("section B dir = %q, want its own %q", got, other)
	}
}

// Checked at load rather than when a key is pressed: otherwise a typo surfaces
// as whatever the command says about a directory it cannot enter, long after the
// file that is wrong was edited.
func TestConfigRefusesADirThatIsNotThere(t *testing.T) {
	_, err := LoadConfig(writeConfig(t,
		"jiraSections:\n  - title: A\n    jql: project = A\n    dir: /nope/definitely/not/here\n"))
	if err == nil {
		t.Fatal("want an error for a missing dir")
	}
	if !strings.Contains(err.Error(), "A") {
		t.Errorf("the error should name the section: %v", err)
	}
}

// A file is not somewhere a command can run.
func TestConfigRefusesADirThatIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(writeConfig(t,
		"jiraSections:\n  - title: A\n    jql: project = A\n    dir: "+file+"\n"))
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want a not-a-directory error, got %v", err)
	}
}

// A key opens one thing. With two of these set, whichever runUserKeybinding
// looked at first would win and the other would be silently dead config.
func TestConfigRefusesAKeyThatWantsBothAPromptAndAPicker(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, `jiraSections:
  - title: A
    jql: project = A
keybindings:
  issues:
    - key: s
      command: "true"
      prompt: true
      choices:
        - value: To Do
`))
	if err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("want a refusal for prompt and choices together, got %v", err)
	}
}

// A misspelled source would otherwise open an empty picker, which reads as a key
// that is broken rather than as a config that is wrong.
func TestConfigRefusesAnUnknownChoicesFrom(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, `jiraSections:
  - title: A
    jql: project = A
keybindings:
  issues:
    - key: s
      command: "true"
      choicesFrom: bogus
`))
	if err == nil || !strings.Contains(err.Error(), "choicesFrom") {
		t.Fatalf("want a refusal for an unknown choicesFrom, got %v", err)
	}
}

// transitions and assignees are the two Task 12 added; a regression that
// dropped either from the accepted set would only surface once someone typed
// it into a real config, long after the config that broke it was edited.
func TestConfigAcceptsTransitionsAndAssigneesChoicesFrom(t *testing.T) {
	for _, source := range []string{"statuses", "transitions", "assignees"} {
		_, err := LoadConfig(writeConfig(t, `jiraSections:
  - title: A
    jql: project = A
keybindings:
  issues:
    - key: s
      command: "true"
      choicesFrom: `+source+`
`))
		if err != nil {
			t.Errorf("choicesFrom: %s should be accepted, got %v", source, err)
		}
	}
}

// An entry with no value would send an empty argument, which for `jira edit` is
// not "leave it alone" but a request it cannot answer.
func TestConfigRefusesAChoiceWithNoValue(t *testing.T) {
	_, err := LoadConfig(writeConfig(t, `jiraSections:
  - title: A
    jql: project = A
keybindings:
  issues:
    - key: s
      command: "true"
      choices:
        - label: 未設定
`))
	if err == nil || !strings.Contains(err.Error(), "no value") {
		t.Fatalf("want a refusal for a value-less choice, got %v", err)
	}
}

// The README's install path is to copy this file, so a template that cannot load
// is a broken first run. It has failed that way once already: an example
// defaults.dir pointing at a path that exists on nobody's machine, checked at
// load, refused to start.
func TestTheExampleConfigLoads(t *testing.T) {
	if _, err := LoadConfig("config.yml.example"); err != nil {
		t.Errorf("config.yml.example should load as shipped: %v", err)
	}
}
