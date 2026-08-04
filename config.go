package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	defaultLimit           = 20
	defaultPreviewWidth    = 0.5
	defaultPreviewPosition = "right"
)

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

type Section struct {
	Title string `yaml:"title"`
	JQL   string `yaml:"jql"`
	Limit int    `yaml:"limit"`

	// SprintPrefix narrows the section to issues in an active sprint whose name
	// starts with this string - the one thing the JQL cannot express, since the
	// board renames its sprint every iteration. Omitted means no narrowing.
	SprintPrefix string `yaml:"sprintPrefix"`
}

type Defaults struct {
	Preview Preview `yaml:"preview"`
	Limit   int     `yaml:"limit"`
}

// Open is a pointer so that an omitted key and an explicit `open: false` are
// distinguishable: the default is open, and a plain bool cannot express that.
type Preview struct {
	Open     *bool   `yaml:"open"`
	Position string  `yaml:"position"`
	Width    float64 `yaml:"width"`
}

type Keybindings struct {
	Issues []Keybinding `yaml:"issues"`
}

type Keybinding struct {
	Key     string `yaml:"key"`
	Command string `yaml:"command"`
	// Name is what the help calls this key. Optional: without it the help falls
	// back to the command, which is honest but usually longer than the pane.
	Name string `yaml:"name"`
	// Prompt makes the key ask for an instruction before running, and puts the
	// issue plus that instruction in {{.Prompt}}. Without it the key runs at
	// once, which is the right shape for a fixed command like opening a browser.
	Prompt bool `yaml:"prompt"`
}

type Theme struct {
	Colors Colors `yaml:"colors"`
}

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
	}
	return &c, nil
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
