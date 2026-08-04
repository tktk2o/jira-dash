package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// issueTTL bounds how stale a detail pane can be. Sections refresh on every
// launch, but a description is re-read often enough that re-fetching it on
// each cursor pass would waste a 360ms CLI start for nothing.
const issueTTL = 10 * time.Minute

// cacheVersion namespaces every cached file. What is cached has a shape, and
// that shape changes: the issue body went from the CLI's markdown to the
// description alone, and Issue gained priority, labels and sprint. Without this
// an upgraded binary keeps serving the old shape - a preview repeating metadata
// for the whole TTL, or rows rendering a dash for fields the old entry never
// held. Bump it whenever Issue or the body format changes.
const cacheVersion = "v2"

// safeKey keeps a key from escaping the cache directory. Keys are issue keys and
// hashes today, but an issue key comes from Jira, and a path separator in one
// would otherwise write wherever it pointed.
func safeKey(key string) string {
	return strings.NewReplacer("/", "_", `\`, "_", "..", "_").Replace(key)
}

type Cache struct {
	dir string
}

func NewCache(dir string) *Cache {
	return &Cache{dir: dir}
}

// SectionKey keys a cached result by its query rather than the section title.
// A renamed title keeps its cache, and an edited JQL naturally lands on a new
// entry instead of showing results for a query that no longer exists.
func SectionKey(jql string, limit int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", jql, limit)))
	return hex.EncodeToString(sum[:])[:12]
}

type CachedSection struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Issues    []Issue   `json:"issues"`
}

func (c *Cache) sectionPath(key string) string {
	return filepath.Join(c.dir, cacheVersion, "sections", safeKey(key)+".json")
}

func (c *Cache) issuePath(key string) string {
	return filepath.Join(c.dir, cacheVersion, "issues", safeKey(key)+".md")
}

// ReadSection treats every failure as a miss. The cache is pure derived data,
// so a corrupt or unreadable file costs one slow fetch, not an error the user
// has to act on.
func (c *Cache) ReadSection(key string) (*CachedSection, bool) {
	b, err := os.ReadFile(c.sectionPath(key))
	if err != nil {
		return nil, false
	}
	var cs CachedSection
	if err := json.Unmarshal(b, &cs); err != nil {
		return nil, false
	}
	return &cs, true
}

func (c *Cache) WriteSection(key string, issues []Issue, at time.Time) error {
	b, err := json.Marshal(CachedSection{FetchedAt: at, Issues: issues})
	if err != nil {
		return err
	}
	return writeAtomic(c.sectionPath(key), b)
}

func (c *Cache) ReadIssue(key string, ttl time.Duration, now time.Time) (string, bool) {
	path := c.issuePath(key)
	st, err := os.Stat(path)
	if err != nil || now.Sub(st.ModTime()) > ttl {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (c *Cache) WriteIssue(key, markdown string) error {
	return writeAtomic(c.issuePath(key), []byte(markdown))
}

// writeAtomic renames into place so a reader never sees a partial file.
func writeAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
