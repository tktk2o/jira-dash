package main

import (
	"fmt"
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
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"config not found: %s\ncopy config.yml.example from this repository to that path and edit it", path)
		}
		return nil, err
	}

	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
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

	for _, k := range c.Create {
		if k.Key == "" || k.Type == "" {
			return nil, fmt.Errorf("%s: every create entry needs both a key and a type", path)
		}
		// A create key that shadowed j, /, q or r would break navigation in a way
		// that is hard to trace back to the config, so it is refused at load
		// rather than silently losing to the switch in handleKey.
		if reservedKeys[k.Key] {
			return nil, fmt.Errorf("%s: create key %q is already a dashboard key", path, k.Key)
		}
	}
	return &c, nil
}

// reservedKeys are the keys handleKey claims. Kept beside the check that uses
// it so adding a binding there is a visible reason to add it here.
var reservedKeys = map[string]bool{
	"q": true, "tab": true, "shift+tab": true, "h": true, "l": true,
	"left": true, "right": true, "j": true, "k": true, "up": true, "down": true,
	"g": true, "G": true, "p": true, "/": true, "esc": true, "r": true,
	"y": true, "Y": true, "?": true, "enter": true, "ctrl+c": true,
}
