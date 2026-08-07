package main

import (
	"os"
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

// A shell expands ~ when the value is typed interactively, but not when it
// arrives quoted, from a script, or from JIRA_DASH_CONFIG set in a profile.
func TestResolveConfigPathExpandsTilde(t *testing.T) {
	home := "/home/someone"

	if got := resolveConfigPath("~/foo.yml", "", home); got != "/home/someone/foo.yml" {
		t.Errorf("flag = %q, want the expanded path", got)
	}
	if got := resolveConfigPath("", "~/bar.yml", home); got != "/home/someone/bar.yml" {
		t.Errorf("env = %q, want the expanded path", got)
	}
	if got := resolveConfigPath("~", "", home); got != home {
		t.Errorf("bare ~ = %q, want %q", got, home)
	}
	// Only a leading ~ is special; a path that merely contains one is left be.
	if got := resolveConfigPath("/tmp/~weird.yml", "", home); got != "/tmp/~weird.yml" {
		t.Errorf("mid-path ~ = %q, should be untouched", got)
	}
}

// TestResolveConfigPathPrefersRepoFileOverDefault covers the repo-scoped
// config: with neither --config nor JIRA_DASH_CONFIG set, a .jira-dash.yml in
// the current directory beats the default path under home.
func TestResolveConfigPathPrefersRepoFileOverDefault(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()

	if err := os.WriteFile(filepath.Join(dir, ".jira-dash.yml"), []byte("sections: []"), 0o644); err != nil {
		t.Fatal(err)
	}

	home := "/home/someone"
	if got, want := resolveConfigPath("", "", home), ".jira-dash.yml"; got != want {
		t.Errorf("resolveConfigPath = %q, want %q", got, want)
	}
}

// The .yaml spelling must be recognised too.
func TestResolveConfigPathAcceptsYamlExtension(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()

	if err := os.WriteFile(filepath.Join(dir, ".jira-dash.yaml"), []byte("sections: []"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, want := resolveConfigPath("", "", "/home/someone"), ".jira-dash.yaml"; got != want {
		t.Errorf("resolveConfigPath = %q, want %q", got, want)
	}
}

// --config and JIRA_DASH_CONFIG are both explicit and must still win over a
// repo file that happens to exist.
func TestResolveConfigPathExplicitBeatsRepoFile(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()

	if err := os.WriteFile(filepath.Join(dir, ".jira-dash.yml"), []byte("sections: []"), 0o644); err != nil {
		t.Fatal(err)
	}

	home := "/home/someone"
	if got, want := resolveConfigPath("/flag.yml", "", home), "/flag.yml"; got != want {
		t.Errorf("--config should still win, got %q", got)
	}
	if got, want := resolveConfigPath("", "/env.yml", home), "/env.yml"; got != want {
		t.Errorf("JIRA_DASH_CONFIG should still win, got %q", got)
	}
}

// With no repo file present, the default path under home is unchanged.
func TestResolveConfigPathFallsBackToDefaultWithNoRepoFile(t *testing.T) {
	dir := t.TempDir()
	restore := chdir(t, dir)
	defer restore()

	home := "/home/someone"
	want := filepath.Join(home, ".config", "jira-dash", "config.yml")
	if got := resolveConfigPath("", "", home); got != want {
		t.Errorf("resolveConfigPath = %q, want %q", got, want)
	}
}

// chdir switches to dir and returns a func that restores the previous
// working directory - t.Chdir would do this automatically, but it panics
// with subtests that run in parallel elsewhere in this package, so tests
// here manage it by hand.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatal(err)
		}
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
