// Package jira is the one place this program talks to the Jira Cloud REST
// API. Credential resolution here mirrors the TypeScript CLI it replaces
// exactly, on purpose: keeping the same file format and env vars means both
// implementations can log in once and be swapped for each other at will.
package jira

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// credentialsFile is the path the old CLI reads and writes. Not configurable:
// a second path would mean a second login, which is the thing this format is
// kept identical to avoid.
const credentialsFile = "jira-cli/credentials.json"

// Credentials is everything a request to the Jira API needs. APIToken is
// always the raw secret here - the base64 wrapping in the file is a storage
// detail this type does not carry past LoadCredentials.
type Credentials struct {
	Email    string
	APIToken string
	CloudID  string
	SiteURL  string
	SiteName string
}

// fileCredentials mirrors the on-disk shape the old CLI wrote. APIToken is
// base64 there, not here - see storedCredentials.decode.
type fileCredentials struct {
	Email    string `json:"email"`
	APIToken string `json:"apiToken"`
	CloudID  string `json:"cloudId"`
	SiteURL  string `json:"siteUrl"`
	SiteName string `json:"siteName"`
}

// LoadCredentials resolves credentials the same way the old CLI did: the
// three required env vars, if all three are set, win outright; otherwise the
// saved file. Partial env (say, only JIRA_EMAIL) falls through to the file
// rather than erroring, since a shell that exports one Jira-flavoured var for
// an unrelated reason should not break the file it never meant to override.
func LoadCredentials() (Credentials, error) {
	email, token, cloudID := os.Getenv("JIRA_EMAIL"), os.Getenv("JIRA_API_TOKEN"), os.Getenv("JIRA_CLOUD_ID")
	if email != "" && token != "" && cloudID != "" {
		return Credentials{
			Email:    email,
			APIToken: token,
			CloudID:  cloudID,
			SiteURL:  os.Getenv("JIRA_SITE_URL"),
			SiteName: os.Getenv("JIRA_SITE_NAME"),
		}, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return Credentials{}, fmt.Errorf("no credentials in the environment, and no home directory to check for a saved login: %w", err)
	}

	path := filepath.Join(home, ".config", credentialsFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Credentials{}, fmt.Errorf("not logged in: run %q", "jira auth login")
		}
		return Credentials{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var fc fileCredentials
	if err := json.Unmarshal(raw, &fc); err != nil {
		return Credentials{}, fmt.Errorf("%s is not valid JSON, run %q to rewrite it: %w", path, "jira auth login", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(fc.APIToken)
	if err != nil {
		return Credentials{}, fmt.Errorf("%s has a corrupted token, run %q to log in again: %w", path, "jira auth login", err)
	}

	return Credentials{
		Email:    fc.Email,
		APIToken: string(decoded),
		CloudID:  fc.CloudID,
		SiteURL:  fc.SiteURL,
		SiteName: fc.SiteName,
	}, nil
}

// CredentialsSource reports which of LoadCredentials' two sources won,
// without duplicating that function's own resolution logic in the caller.
// `jira auth status` uses this to say where its answer came from.
func CredentialsSource() string {
	if os.Getenv("JIRA_EMAIL") != "" && os.Getenv("JIRA_API_TOKEN") != "" && os.Getenv("JIRA_CLOUD_ID") != "" {
		return "environment variables"
	}
	return "the saved credentials file"
}

// SaveCredentials writes c to disk in exactly the shape the old CLI wrote:
// the same keys, APIToken base64-encoded, file mode 0o600, at the same path
// LoadCredentials reads. That byte-for-byte compatibility - not merely
// "something LoadCredentials can parse" - is what keeps rolling back to the
// old CLI possible: it reads this file too, and never needs to know a Go
// tool touched it. Returns the path written, so the caller can tell the
// person where their credentials went without hardcoding it twice.
func SaveCredentials(c Credentials) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no home directory to save credentials into: %w", err)
	}

	dir := filepath.Join(home, ".config", "jira-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	fc := fileCredentials{
		Email:    c.Email,
		APIToken: base64.StdEncoding.EncodeToString([]byte(c.APIToken)),
		CloudID:  c.CloudID,
		SiteURL:  c.SiteURL,
		SiteName: c.SiteName,
	}
	raw, err := json.Marshal(fc)
	if err != nil {
		return "", fmt.Errorf("encoding credentials: %w", err)
	}

	path := filepath.Join(dir, "credentials.json")
	// 0o600 from the start, not a chmod after the fact: a window where the
	// token sat world- or group-readable would defeat the point of the mode.
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}
