package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

const (
	defaultLimit           = 20
	defaultPreviewWidth    = 0.5
	defaultPreviewPosition = "right"
)

// Config is the whole file, and the whole contract with the person who edits
// it: every tab, key and colour the dashboard has comes from here.
type Config struct {
	Sections    []Section   `yaml:"jiraSections"`
	Defaults    Defaults    `yaml:"defaults"`
	Keybindings Keybindings `yaml:"keybindings"`
	Create      []CreateKey `yaml:"create"`
	Theme       Theme       `yaml:"theme"`
}

// CreateKey binds a key to an issue type. Which types a site offers is
// site-specific - on a Japanese site they are named in Japanese - so the
// mapping is config rather than code. Everything else about the new issue comes
// from the row the cursor is on.
type CreateKey struct {
	Key  string `yaml:"key"`
	Type string `yaml:"type"`
}

// Section is one tab. The JQL owns what the tab is; everything else here trims
// or labels that result.
type Section struct {
	Title string `yaml:"title"`
	JQL   string `yaml:"jql"`
	Limit int    `yaml:"limit"`

	// SprintPrefix narrows the section to issues in an active sprint whose name
	// starts with this string - the one thing the JQL cannot express, since the
	// board renames its sprint every iteration. Omitted means no narrowing.
	SprintPrefix string `yaml:"sprintPrefix"`

	// Dir is where this tab's keybindings run: the checkout the issues in it are
	// about. It is per-section because that is the grain the answer has - a board
	// belongs to a repository - while a keybinding is written once for every tab.
	// Falls back to defaults.dir. A leading ~ is expanded at load.
	Dir string `yaml:"dir"`
}

// Defaults are what a section that says nothing gets.
type Defaults struct {
	Preview Preview `yaml:"preview"`
	Limit   int     `yaml:"limit"`
	// Dir is the fallback working directory for sections that do not name one.
	Dir string `yaml:"dir"`
}

// Preview is the pane beside the table. Open is a pointer so that an omitted key
// and an explicit `open: false` are distinguishable: the default is open, and a
// plain bool cannot express that.
type Preview struct {
	Open     *bool   `yaml:"open"`
	Position string  `yaml:"position"`
	Width    float64 `yaml:"width"`
}

// Keybindings groups the configured keys by what they act on. Only issue keys
// exist so far, and the nesting is what leaves room for another scope without
// renaming this one.
type Keybindings struct {
	Issues []Keybinding `yaml:"issues"`
}

// Keybinding is one configured key and the shell command it runs. The dashboard
// itself never writes to Jira, so anything that changes an issue is one of
// these.
type Keybinding struct {
	Key     string `yaml:"key"`
	Command string `yaml:"command"`
	// Name is what the help calls this key. Optional: without it the help falls
	// back to the command, which is honest but usually longer than the pane.
	Name string `yaml:"name"`
	// Prompt makes the key ask for an instruction before running, and puts the
	// issue plus that instruction in {{.Prompt}}, or the typed text alone in
	// {{.Input}}. Without it the key runs at once, which is the right shape for a
	// fixed command like opening a browser.
	Prompt bool `yaml:"prompt"`
	// Choices makes the key open a picker instead of running at once, and puts
	// the chosen entry's value in {{.Choice}}. For a field whose accepted values
	// are a short fixed set - an assignee, a priority - a list you pick from beats
	// a line you type: the values are account ids and site-specific status names,
	// which are not things anyone types correctly from memory.
	Choices []Choice `yaml:"choices"`
	// ChoicesFrom builds the picker from a live source instead of the config.
	// "statuses" derives the list from the current tab's rows - no API call, so
	// it is the only source that still works offline. "transitions" and
	// "assignees" call Jira for the selected issue's real transitions and
	// assignable users - see choicesFromTransitions and choicesFromAssignees.
	ChoicesFrom string `yaml:"choicesFrom"`
	// Refresh reloads the preview once the command exits without error, for a key
	// that changed the issue - a posted comment is otherwise invisible until the
	// cursor leaves the row and comes back. Opt-in because the dashboard cannot
	// tell a command that writes from one that opens a browser, and a needless
	// reload costs two ~360ms jira calls.
	Refresh bool `yaml:"refresh"`
}

// Choice is one line of a picker. Label is what the box shows and Value is what
// the command receives, because the two differ for exactly the values a picker
// is worth having: an assignee is picked as a name and sent as an account id.
// A label-less entry shows its value, which is right for a status name.
type Choice struct {
	Label string `yaml:"label"`
	Value string `yaml:"value"`
}

// Name is what the picker draws for this entry.
func (c Choice) Name() string { return orDefault(c.Label, c.Value) }

// choicesFromStatuses derives the picker from the status names the current
// tab's rows carry - the only source with no API call, so the only one that
// still opens with a dead network.
const choicesFromStatuses = "statuses"

// choicesFromTransitions derives the picker from the transitions Jira's
// workflow actually allows on the selected issue right now (Client.Transitions).
const choicesFromTransitions = "transitions"

// choicesFromAssignees derives the picker from the users Jira actually allows
// assigning the selected issue to (Client.AssignableUsers).
const choicesFromAssignees = "assignees"

// choicesFromSources is every accepted ChoicesFrom value. Named so the load-time
// check and the error message that names the accepted set cannot drift apart.
var choicesFromSources = []string{choicesFromStatuses, choicesFromTransitions, choicesFromAssignees}

// Theme is the colours the dashboard draws in. Every field is optional: an
// unset colour falls back to the Dracula shade the layout was designed against.
type Theme struct {
	Colors Colors `yaml:"colors"`
}

// Colors names the four things on screen that carry colour. They are grouped
// the way gh-dash groups its own theme, so a config can be moved across.
type Colors struct {
	Text struct {
		Primary   string `yaml:"primary"`
		Secondary string `yaml:"secondary"`
	} `yaml:"text"`
	Background struct {
		Selected string `yaml:"selected"`
	} `yaml:"background"`
	Border struct {
		Primary string `yaml:"primary"`
	} `yaml:"border"`
}

// LoadConfig reads the YAML at path and fills in defaults. A section that
// cannot run is a hard error rather than a silently dropped tab: an empty tab
// reads as "nothing matches", which is a worse lie than refusing to start.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf(
				"config not found: %s\ncopy config.yml.example from this repository to that path and edit it", path)
		}
		return nil, err
	}

	// KnownFields rather than yaml.Unmarshal: a misspelled key would otherwise
	// unmarshal into nothing at all, and the feature it was meant to configure
	// would look broken instead of the config looking wrong. A hand-edited YAML
	// file is exactly where that typo happens.
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	// An empty file decodes as EOF rather than as an empty document; it is the
	// "jiraSections is empty" case below, which names what to do about it.
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(c.Sections) == 0 {
		return nil, fmt.Errorf("%s: jiraSections is empty", path)
	}

	if c.Defaults.Limit == 0 {
		c.Defaults.Limit = defaultLimit
	}
	if c.Defaults.Preview.Open == nil {
		open := true
		c.Defaults.Preview.Open = &open
	}
	if c.Defaults.Preview.Position == "" {
		c.Defaults.Preview.Position = defaultPreviewPosition
	}
	if c.Defaults.Preview.Width == 0 {
		c.Defaults.Preview.Width = defaultPreviewWidth
	}

	home, _ := os.UserHomeDir()
	c.Defaults.Dir = expandHome(c.Defaults.Dir, home)
	for i, s := range c.Sections {
		if s.Title == "" {
			return nil, fmt.Errorf("%s: jiraSections[%d] has no title", path, i)
		}
		if s.JQL == "" {
			return nil, fmt.Errorf("%s: section %q has no jql", path, s.Title)
		}
		if s.Limit == 0 {
			c.Sections[i].Limit = c.Defaults.Limit
		}

		dir := expandHome(orDefault(s.Dir, c.Defaults.Dir), home)
		// Checked at load, not when a key is pressed: a typo'd path would otherwise
		// surface as whatever the command says about a directory it cannot enter,
		// long after the file that is actually wrong was edited.
		if dir != "" {
			info, err := os.Stat(dir)
			if err != nil {
				return nil, fmt.Errorf("%s: section %q dir: %w", path, s.Title, err)
			}
			if !info.IsDir() {
				return nil, fmt.Errorf("%s: section %q dir is not a directory: %s", path, s.Title, dir)
			}
		}
		c.Sections[i].Dir = dir
	}

	// Every configured key goes through the same two checks, and through one
	// another: handleKey tries the dashboard's own switch, then the create keys,
	// then the issue keybindings, so a key claimed twice silently loses in that
	// order. Refusing at load is the only place the loser is still visible.
	claimed := map[string]string{}
	for _, k := range c.Create {
		if k.Key == "" || k.Type == "" {
			return nil, fmt.Errorf("%s: every create entry needs both a key and a type", path)
		}
		if err := claimKey(path, claimed, k.Key, "create key"); err != nil {
			return nil, err
		}
	}
	for _, k := range c.Keybindings.Issues {
		if k.Key == "" || k.Command == "" {
			return nil, fmt.Errorf("%s: every keybindings.issues entry needs both a key and a command", path)
		}
		if err := claimKey(path, claimed, k.Key, "keybinding"); err != nil {
			return nil, err
		}
		// A key opens one thing. Two of these set means the config asked for both a
		// typed instruction and a picker, and whichever handleKey looked at first
		// would win silently.
		if n := countTrue(k.Prompt, len(k.Choices) > 0, k.ChoicesFrom != ""); n > 1 {
			return nil, fmt.Errorf(
				"%s: keybinding %q sets more than one of prompt, choices and choicesFrom", path, k.Key)
		}
		if k.ChoicesFrom != "" && !slices.Contains(choicesFromSources, k.ChoicesFrom) {
			return nil, fmt.Errorf("%s: keybinding %q has unknown choicesFrom %q (want one of %q)",
				path, k.Key, k.ChoicesFrom, choicesFromSources)
		}
		for j, c := range k.Choices {
			// A value-less entry would send an empty argument, which for `jira edit`
			// is not "leave it alone" but a request it cannot answer.
			if c.Value == "" {
				return nil, fmt.Errorf("%s: keybinding %q choices[%d] has no value", path, k.Key, j)
			}
		}
	}
	return &c, nil
}

func countTrue(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// claimKey refuses a key the dashboard already owns, or that another config
// entry has already taken. A create key that shadowed j, /, q or r would break
// navigation in a way that is hard to trace back to the config.
func claimKey(path string, claimed map[string]string, key, what string) error {
	if reservedKeys[key] {
		return fmt.Errorf("%s: %s %q is already a dashboard key", path, what, key)
	}
	if by, dup := claimed[key]; dup {
		return fmt.Errorf("%s: %s %q is already taken by another %s", path, what, key, by)
	}
	claimed[key] = what
	return nil
}

// reservedKeys are the keys handleKey claims. Kept beside the check that uses
// it so adding a binding there is a visible reason to add it here.
var reservedKeys = map[string]bool{
	"q": true, "tab": true, "shift+tab": true, "h": true, "l": true,
	"left": true, "right": true, "j": true, "k": true, "up": true, "down": true,
	"g": true, "G": true, "p": true, "/": true, "esc": true, "r": true,
	"y": true, "Y": true, "?": true, "enter": true, "ctrl+c": true,
}
