package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The key is the query, not the title: renaming a tab must not throw the
// cache away, and editing the JQL must not reuse the old results.
func TestSectionKeyFollowsTheQueryNotTheTitle(t *testing.T) {
	a := SectionKey("assignee = currentUser()", 20)
	b := SectionKey("assignee = currentUser()", 20)
	if a != b {
		t.Errorf("same query gave different keys: %q vs %q", a, b)
	}

	if c := SectionKey("project = ABC", 20); c == a {
		t.Error("a different JQL must give a different key")
	}
	if d := SectionKey("assignee = currentUser()", 50); d == a {
		t.Error("a different limit must give a different key")
	}
	if len(a) != 12 {
		t.Errorf("key length = %d, want 12", len(a))
	}
}

func TestCacheSectionRoundTrip(t *testing.T) {
	cache := NewCache(t.TempDir())
	at := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	name := "Alice Example"
	issues := []Issue{{Key: "ABC-1", Summary: "テスト", Status: "Open", Assignee: &name}}

	if err := cache.WriteSection("deadbeef1234", issues, at); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, ok := cache.ReadSection("deadbeef1234")
	if !ok {
		t.Fatal("want a cache hit")
	}
	if len(got.Issues) != 1 || got.Issues[0].Key != "ABC-1" {
		t.Errorf("issues did not survive the round trip: %+v", got.Issues)
	}
	if !got.FetchedAt.Equal(at) {
		t.Errorf("fetchedAt = %v, want %v", got.FetchedAt, at)
	}
}

func TestCacheSectionMissOnUnknownKey(t *testing.T) {
	if _, ok := NewCache(t.TempDir()).ReadSection("nope"); ok {
		t.Error("want a miss")
	}
}

// A corrupt cache is a miss, never an error the user has to think about.
func TestCacheSectionCorruptFileIsAMiss(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sections"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sections", "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, ok := NewCache(dir).ReadSection("broken"); ok {
		t.Error("a corrupt cache file must read as a miss")
	}
}

func TestCacheIssueHonoursTTL(t *testing.T) {
	cache := NewCache(t.TempDir())
	if err := cache.WriteIssue("ABC-1", "# ABC-1\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	now := time.Now()
	if _, ok := cache.ReadIssue("ABC-1", 10*time.Minute, now); !ok {
		t.Error("a fresh entry should hit")
	}
	if _, ok := cache.ReadIssue("ABC-1", time.Nanosecond, now.Add(time.Hour)); ok {
		t.Error("an entry past its TTL should miss")
	}
}

// A reader must never see a half-written file, so writes land via rename.
func TestCacheWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir)
	if err := cache.WriteSection("k", []Issue{{Key: "ABC-1"}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "sections"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "k.json" {
		t.Errorf("want exactly k.json, got %v", entries)
	}
}
