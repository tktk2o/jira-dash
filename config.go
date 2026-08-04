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
	Theme       Theme       `yaml:"theme"`
}

type Section struct {
	Title string `yaml:"title"`
	JQL   string `yaml:"jql"`
	Limit int    `yaml:"limit"`
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
				"config not found: %s\nsetup.sh seeds it from jira-dash/config.yml.example", path)
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
	return &c, nil
}
