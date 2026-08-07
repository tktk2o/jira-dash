package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// fieldsCacheTTL is how long a cached FieldIDs is trusted before this
// program refetches: long enough that every call in a normal working day
// hits the cache, short enough that a site's custom fields (added or
// renamed rarely, per FieldIDs' own doc comment) do not stay stale forever
// just because deleting the file by hand is the only other way to notice.
const fieldsCacheTTL = 24 * time.Hour

// FieldIDs are the customfield_NNNNN ids this program needs, resolved at
// runtime rather than hardcoded: each site's Jira admin assigns its own ids
// when a custom field is created, so the same field name carries a different
// id on every site. The old TypeScript CLI baked six of these into its
// source; that value cannot follow it here (see the migration plan's Global
// Constraints).
//
// A field this site has never configured resolves to the empty string, not
// an error - most sites have no "Team" field, and failing every call over a
// field nobody asked to use would be wrong.
type FieldIDs struct {
	Sprint      string
	StoryPoints string
	Team        string
	StartDate   string
	TargetStart string
	Flagged     string
}

// rawField is one entry of GET /field.
type rawField struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	UntranslatedName string `json:"untranslatedName"`
}

// fieldTargets pairs the name this program looks for with where a matching
// id gets written. untranslatedName is checked first (see resolveFieldIDs):
// name is subject to the viewer's locale, and matching on a translated
// string would silently stop working the day someone's Jira language
// changes.
var fieldTargets = []struct {
	name string
	set  func(*FieldIDs, string)
}{
	{"Sprint", func(f *FieldIDs, id string) { f.Sprint = id }},
	{"Story Points", func(f *FieldIDs, id string) { f.StoryPoints = id }},
	{"Team", func(f *FieldIDs, id string) { f.Team = id }},
	{"Start date", func(f *FieldIDs, id string) { f.StartDate = id }},
	{"Target start", func(f *FieldIDs, id string) { f.TargetStart = id }},
	{"Flagged", func(f *FieldIDs, id string) { f.Flagged = id }},
}

// resolveFieldIDs matches fieldTargets against GET /field's response,
// case-insensitively, preferring untranslatedName over name. The first raw
// field that matches wins; Jira does not promise the list is free of
// duplicate names, but a site that has two fields called "Team" is not a
// case this program can disambiguate any better by trying harder.
func resolveFieldIDs(raws []rawField) FieldIDs {
	var out FieldIDs
	for _, target := range fieldTargets {
		for _, raw := range raws {
			candidate := raw.UntranslatedName
			if candidate == "" {
				candidate = raw.Name
			}
			if strings.EqualFold(candidate, target.name) {
				target.set(&out, raw.ID)
				break
			}
		}
	}
	return out
}

// fieldsCacheKeyReplacer keeps a cloud id from escaping the cache directory,
// mirroring the root package's own safeKey for the same reason: the id comes
// from a credentials file this program did not write, so nothing here should
// assume it is free of path separators.
var fieldsCacheKeyReplacer = strings.NewReplacer("/", "_", `\`, "_", "..", "_")

// fieldsCachePath is where one site's resolved FieldIDs live. Keyed by cloud
// id, not a fixed name, because a machine can hold credentials for more than
// one Jira site and the ids do not transfer between them. Returns "" when
// the home directory cannot be found, which callers treat as "caching is
// simply unavailable this run" rather than an error - resolving fields still
// works without it, just slower.
func fieldsCachePath(cloudID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "jira-dash", "fields-"+fieldsCacheKeyReplacer.Replace(cloudID)+".json")
}

// readFieldIDsCache treats every failure - missing file, unreadable,
// corrupt JSON - as a miss. The cache holds nothing but derived data, so a
// bad file costs one re-fetch, not an error surfaced to the user.
func readFieldIDsCache(path string) (FieldIDs, bool) {
	if path == "" {
		return FieldIDs{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return FieldIDs{}, false
	}
	var ids FieldIDs
	if err := json.Unmarshal(b, &ids); err != nil {
		return FieldIDs{}, false
	}
	return ids, true
}

// fieldsCacheFresh reports whether path's cache file is younger than
// fieldsCacheTTL, using the file's own mtime rather than a timestamp stored
// inside it - one less thing that can drift out of sync with the file it
// describes. A file that cannot be stat'd (missing, permissions) is treated
// as not fresh, matching readFieldIDsCache's own "every failure is a miss"
// rule.
func fieldsCacheFresh(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < fieldsCacheTTL
}

// writeFieldIDsCache best-efforts a write and swallows any failure for the
// same reason readFieldIDsCache treats every read failure as a miss: this is
// derived data, and a user who cannot write ~/.cache should still get a
// working FieldIDs, just refetched next time.
func writeFieldIDsCache(path string, ids FieldIDs) {
	if path == "" {
		return
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o644)
}

// FieldIDs resolves this site's custom field ids by name from GET /field,
// caching the result on disk keyed by cloud id so that every other call in
// this program - and every other run on this machine - can skip the round
// trip. The cache expires after fieldsCacheTTL (a site adds or renames
// custom fields rarely, but rarely is not never), and a fetch that fails
// with a stale cache still on disk falls back to that stale cache rather
// than failing outright: a working-but-old FieldIDs is more useful than no
// FieldIDs at all when, say, the network is briefly down.
func (c *Client) FieldIDs(ctx context.Context) (FieldIDs, error) {
	path := fieldsCachePath(c.creds.CloudID)
	if fieldsCacheFresh(path) {
		if ids, ok := readFieldIDsCache(path); ok {
			return ids, nil
		}
	}

	var raws []rawField
	if err := c.do(ctx, http.MethodGet, "/field", nil, &raws); err != nil {
		if ids, ok := readFieldIDsCache(path); ok {
			return ids, nil
		}
		return FieldIDs{}, err
	}

	ids := resolveFieldIDs(raws)
	writeFieldIDsCache(path, ids)
	return ids, nil
}
