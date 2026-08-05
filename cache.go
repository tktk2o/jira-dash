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
// each cursor pass would waste a real Jira REST round trip (0.5-1.2s) for
// nothing.
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
// The replacer is built once: it compiles a matcher, and safeKey is on the path
// of every cache read and write.
var keyReplacer = strings.NewReplacer("/", "_", `\`, "_", "..", "_")

func safeKey(key string) string {
	return keyReplacer.Replace(key)
}

// Cache is the on-disk store the dashboard renders from before any query
// returns. It holds derived data only, so every read is allowed to fail.
type Cache struct {
	dir string
}

// NewCache roots a cache at dir. The directory is created lazily, on the first
// write, so a run that only reads never leaves one behind.
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

// CachedSection is one section's last known result, with the time it was
// fetched: the footer states that age rather than implying the rows are current.
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

// WriteSection records a section's rows so the next launch has something to draw
// before the query returns.
func (c *Cache) WriteSection(key string, issues []Issue, at time.Time) error {
	b, err := json.Marshal(CachedSection{FetchedAt: at, Issues: issues})
	if err != nil {
		return err
	}
	return writeAtomic(c.sectionPath(key), b)
}

// ReadIssue returns a cached issue body, and false once it is older than ttl.
// Unlike a section, a body has no visible age on screen, so the TTL is the only
// thing keeping the preview honest.
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

// WriteIssue records an issue body. Its modification time is the entry's age,
// which is why nothing here writes a timestamp of its own.
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
