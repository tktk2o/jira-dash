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
