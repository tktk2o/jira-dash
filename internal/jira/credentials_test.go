package jira

import (
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
