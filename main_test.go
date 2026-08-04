package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigPathPrecedence(t *testing.T) {
	home := "/home/someone"

	if got := resolveConfigPath("/flag.yml", "/env.yml", home); got != "/flag.yml" {
		t.Errorf("--config should win, got %q", got)
	}
	if got := resolveConfigPath("", "/env.yml", home); got != "/env.yml" {
		t.Errorf("the env var should be next, got %q", got)
	}

	want := filepath.Join(home, ".config", "jira-dash", "config.yml")
	if got := resolveConfigPath("", "", home); got != want {
		t.Errorf("default = %q, want %q", got, want)
	}
}

func TestSectionIndexByTitle(t *testing.T) {
	cfg := testConfig()

	got, err := sectionIndex(cfg, "Sprint")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 1 {
		t.Errorf("index = %d, want 1", got)
	}

	if _, err := sectionIndex(cfg, "Nope"); err == nil {
		t.Fatal("an unknown section must be an error")
	} else if !strings.Contains(err.Error(), "Mine") {
		t.Errorf("the error should list the available sections, got: %v", err)
	}
}

func TestSectionIndexEmptyMeansFirst(t *testing.T) {
	got, err := sectionIndex(testConfig(), "")
	if err != nil || got != 0 {
		t.Errorf("index = %d, err = %v; want 0, nil", got, err)
	}
}
