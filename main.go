// jira-dash shows Jira issues the way gh dash shows pull requests: tabs of
// issues defined by JQL in a config file.
//
// It is read-only. Anything that changes an issue runs through a configured
// keybinding, so the dashboard itself can never write to Jira.
//
// Every query goes through the `jira` CLI, which pays ~360ms of tsx startup
// per invocation. Sections therefore render from cache first and refresh
// behind that; see docs/design.local.md for the measurements.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.1.0"

func main() {
	configPath := flag.String("config", "", "path to a config file (default ~/.config/jira-dash/config.yml)")
	section := flag.String("section", "", "title of the section to open on")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("jira-dash " + version)
		return
	}

	if err := run(*configPath, *section); err != nil {
		fmt.Fprintln(os.Stderr, "jira-dash: "+err.Error())
		os.Exit(1)
	}
}

func run(configFlag, section string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// A missing CLI is the one dependency worth naming explicitly; every
	// section would otherwise fail with the same unhelpful error.
	if _, err := exec.LookPath("jira"); err != nil {
		return errors.New("the `jira` CLI is not on PATH")
	}

	path := resolveConfigPath(configFlag, os.Getenv("JIRA_DASH_CONFIG"), home)
	cfg, err := LoadConfig(path)
	if err != nil {
		return err
	}

	start, err := sectionIndex(cfg, section)
	if err != nil {
		return err
	}

	cache := NewCache(filepath.Join(home, ".cache", "jira-dash"))
	model := NewModel(cfg, CLI{Bin: "jira"}, cache, time.Now)
	model.active = start

	_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}

func resolveConfigPath(flagValue, env, home string) string {
	if flagValue != "" {
		return flagValue
	}
	if env != "" {
		return env
	}
	return filepath.Join(home, ".config", "jira-dash", "config.yml")
}

// sectionIndex maps --section to a tab. An unknown title lists what is
// available rather than silently opening the first tab.
func sectionIndex(cfg *Config, title string) (int, error) {
	if title == "" {
		return 0, nil
	}
	titles := make([]string, 0, len(cfg.Sections))
	for i, s := range cfg.Sections {
		if s.Title == title {
			return i, nil
		}
		titles = append(titles, s.Title)
	}
	return 0, fmt.Errorf("no section titled %q; available: %s", title, strings.Join(titles, ", "))
}
