// Jira-dash shows Jira issues the way gh dash shows pull requests: tabs of
// issues defined by JQL in a config file.
//
// The name is capitalised because it opens the sentence, which is the convention
// for a command's doc comment; the binary itself is jira-dash.
//
// It is read-only. Anything that changes an issue runs through a configured
// keybinding, so the dashboard itself can never write to Jira.
//
// Every query used to shell out to the `jira` CLI, which paid ~360ms of tsx
// startup per invocation; it now calls internal/jira directly. Sections still
// render from cache first and refresh behind that - the network itself is
// still the network; see docs/design.local.md for the measurements.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	jirapkg "jira-dash/internal/jira"
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

	// Config first: on a machine that is missing both, its error is the one
	// that names a next step ("copy config.yml.example to ..."), whereas a
	// missing credential set can only say to run `jira auth login`.
	path := resolveConfigPath(configFlag, os.Getenv("JIRA_DASH_CONFIG"), home)
	cfg, err := LoadConfig(path)
	if err != nil {
		return err
	}

	// Credentials are resolved once, at startup: a bad or missing set fails
	// here with the actionable message LoadCredentials already produces
	// ("run `jira auth login`"), rather than every section failing that same
	// way once the dashboard is already on screen.
	creds, err := jirapkg.LoadCredentials()
	if err != nil {
		return err
	}

	start, err := sectionIndex(cfg, section)
	if err != nil {
		return err
	}

	cache := NewCache(filepath.Join(home, ".cache", "jira-dash"))
	searcher := Adapter{Client: jirapkg.NewClient(creds)}
	model := NewModel(cfg, searcher, cache, time.Now)
	model.active = start

	_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}

func resolveConfigPath(flagValue, env, home string) string {
	if flagValue != "" {
		return expandHome(flagValue, home)
	}
	if env != "" {
		return expandHome(env, home)
	}
	return filepath.Join(home, ".config", "jira-dash", "config.yml")
}

// expandHome resolves a leading ~ itself. An interactive shell normally does
// it, but not when the value arrives quoted, from a script, or from
// JIRA_DASH_CONFIG set in a profile - and a literal "~/..." path only fails
// later as a confusing not-found error.
func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
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
