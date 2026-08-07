package jira

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCreds drops a credentials.json at the path the old CLI would have
// written it to, under the given HOME.
func writeCreds(t *testing.T, home, contents string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "jira-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialsPrefersEnvOverTheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCreds(t, home, `{"email":"file@example.com","apiToken":"ZmlsZS10b2tlbg==",
		"cloudId":"cloud-from-file","siteUrl":"https://file.example.com","siteName":"File"}`)
	t.Setenv("JIRA_EMAIL", "env@example.com")
	t.Setenv("JIRA_API_TOKEN", "env-token")
	t.Setenv("JIRA_CLOUD_ID", "cloud-from-env")

	got, err := LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "env@example.com" || got.CloudID != "cloud-from-env" {
		t.Errorf("env should win: %+v", got)
	}
	// env の token は素のまま。base64 の復号はファイル側だけの話。
	if got.APIToken != "env-token" {
		t.Errorf("APIToken = %q, want the env value verbatim", got.APIToken)
	}
}

// ファイルの apiToken は base64 で保存されている。旧 CLI が書いた形をそのまま読む。
func TestCredentialsDecodesTheStoredToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range []string{"JIRA_EMAIL", "JIRA_API_TOKEN", "JIRA_CLOUD_ID"} {
		t.Setenv(v, "")
	}
	writeCreds(t, home, `{"email":"a@example.com","apiToken":"ZmlsZS10b2tlbg==",
		"cloudId":"c","siteUrl":"https://x.example.com","siteName":"S"}`)

	got, err := LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if got.APIToken != "file-token" {
		t.Errorf("APIToken = %q, want the decoded value", got.APIToken)
	}
}

// 認証情報が無いのは「動かない」ではなく「まだログインしていない」。
func TestCredentialsSaysWhatToDoWhenThereAreNone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, v := range []string{"JIRA_EMAIL", "JIRA_API_TOKEN", "JIRA_CLOUD_ID"} {
		t.Setenv(v, "")
	}

	_, err := LoadCredentials()
	if err == nil || !strings.Contains(err.Error(), "jira auth login") {
		t.Fatalf("the error should name the way out: %v", err)
	}
}

// A partial env (one or two of the three vars) is not "logged in via env", so
// it must fall through to the file rather than erroring or silently mixing
// sources.
func TestCredentialsFallsBackToFileWhenEnvIsIncomplete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCreds(t, home, `{"email":"a@example.com","apiToken":"ZmlsZS10b2tlbg==",
		"cloudId":"c","siteUrl":"https://x.example.com","siteName":"S"}`)
	t.Setenv("JIRA_EMAIL", "env@example.com")
	t.Setenv("JIRA_API_TOKEN", "")
	t.Setenv("JIRA_CLOUD_ID", "")

	got, err := LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "a@example.com" {
		t.Errorf("Email = %q, want the file value since env was incomplete", got.Email)
	}
}

// A corrupted file (an apiToken that is not valid base64) must say what to
// do about it, not just fail to decode.
func TestCredentialsNamesTheFixWhenTheStoredTokenIsCorrupted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range []string{"JIRA_EMAIL", "JIRA_API_TOKEN", "JIRA_CLOUD_ID"} {
		t.Setenv(v, "")
	}
	writeCreds(t, home, `{"email":"a@example.com","apiToken":"not-base64!!","cloudId":"c"}`)

	_, err := LoadCredentials()
	if err == nil || !strings.Contains(err.Error(), "jira auth login") {
		t.Fatalf("want an actionable error for a corrupted token, got %v", err)
	}
}

// SaveCredentials then LoadCredentials must round-trip every field -
// otherwise a login-then-status sequence would report something other than
// what was just typed in.
func TestSaveCredentialsRoundTripsThroughLoadCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range []string{"JIRA_EMAIL", "JIRA_API_TOKEN", "JIRA_CLOUD_ID"} {
		t.Setenv(v, "")
	}

	want := Credentials{
		Email:    "a@example.com",
		APIToken: "a-fresh-token",
		CloudID:  "cloud-1",
		SiteURL:  "https://site.example.com",
		SiteName: "Ada Lovelace",
	}
	if _, err := SaveCredentials(want); err != nil {
		t.Fatal(err)
	}

	got, err := LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("LoadCredentials() = %+v, want %+v", got, want)
	}
}

// The saved file must be exactly 0o600 - anything more permissive would
// leave the token readable by other accounts on the machine.
func TestSaveCredentialsWritesFileMode0600(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := SaveCredentials(Credentials{Email: "a@example.com", APIToken: "tok", CloudID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

// SaveCredentials must write the old CLI's own key names and base64
// treatment, not just something LoadCredentials happens to accept - a
// rollback to the old CLI reads this file directly, without going through
// this package at all.
func TestSaveCredentialsUsesTheOldCLIsFieldNamesAndBase64Token(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := SaveCredentials(Credentials{
		Email: "a@example.com", APIToken: "a-fresh-token", CloudID: "cloud-1",
		SiteURL: "https://site.example.com", SiteName: "Ada Lovelace",
	})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"email", "apiToken", "cloudId", "siteUrl", "siteName"} {
		if _, ok := m[key]; !ok {
			t.Errorf("field %q missing from the saved JSON: %v", key, m)
		}
	}
	if m["email"] != "a@example.com" {
		t.Errorf("email = %v", m["email"])
	}
	if m["apiToken"] == "a-fresh-token" {
		t.Error("apiToken was written raw, not base64-encoded")
	}
	decoded, err := base64.StdEncoding.DecodeString(m["apiToken"].(string))
	if err != nil {
		t.Fatalf("apiToken is not valid base64: %v", err)
	}
	if string(decoded) != "a-fresh-token" {
		t.Errorf("decoded apiToken = %q, want the original token", decoded)
	}
}

// A file written by SaveCredentials must load correctly when reopened as if
// by the old CLI, i.e. through the same writeCreds-shaped path this test
// file already exercises for a hand-written fixture.
func TestSaveCredentialsProducesAFileLoadCredentialsCanReread(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range []string{"JIRA_EMAIL", "JIRA_API_TOKEN", "JIRA_CLOUD_ID"} {
		t.Setenv(v, "")
	}

	if _, err := SaveCredentials(Credentials{Email: "a@example.com", APIToken: "tok", CloudID: "c"}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if got.APIToken != "tok" {
		t.Errorf("APIToken = %q", got.APIToken)
	}
}

func TestSaveCredentialsRepairsAnExistingInsecureFileMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "jira-cli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveCredentials(Credentials{Email: "a@example.com", APIToken: "tok", CloudID: "c"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credentials mode = %o, want 600", got)
	}
}

// Sanity check on the test helper itself: it must produce what LoadCredentials
// expects to parse, so a bug in writeCreds cannot masquerade as a passing
// LoadCredentials test.
func TestWriteCredsProducesValidJSON(t *testing.T) {
	home := t.TempDir()
	writeCreds(t, home, `{"email":"a@example.com","apiToken":"dG9r","cloudId":"c"}`)
	raw, err := os.ReadFile(filepath.Join(home, ".config", "jira-cli", "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fc fileCredentials
	if err := json.Unmarshal(raw, &fc); err != nil {
		t.Fatal(err)
	}
}
