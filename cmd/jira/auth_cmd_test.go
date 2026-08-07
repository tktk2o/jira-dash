package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jirapkg "jira-dash/internal/jira"
)

// fakeVerify and fakeSave let login/status tests exercise the real prompt
// and output logic without a network call or a real credentials file, per
// the migration plan's ban on either from a test.
func fakeVerify(user jirapkg.User, err error) verifyFunc {
	return func(ctx context.Context, creds jirapkg.Credentials) (jirapkg.User, error) {
		return user, err
	}
}

// TestLoginPromptsInOrderAndSavesOnSuccess feeds the four answers the old
// CLI's login also asked for, in the same order, and checks the saved
// credentials carry them plus the display name Jira returned as siteName.
func TestLoginPromptsInOrderAndSavesOnSuccess(t *testing.T) {
	stdin := strings.NewReader("a@example.com\nsecret-token\ncloud-1\nhttps://site.example.com\n")
	var stdout bytes.Buffer
	var saved jirapkg.Credentials
	savedPath := "/home/x/.config/jira-cli/credentials.json"
	save := func(c jirapkg.Credentials) (string, error) {
		saved = c
		return savedPath, nil
	}
	verify := fakeVerify(jirapkg.User{DisplayName: "Ada Lovelace", AccountID: "acc-1", Active: true}, nil)

	if err := runAuthLogin(stdin, &stdout, verify, save); err != nil {
		t.Fatal(err)
	}

	if saved.Email != "a@example.com" || saved.APIToken != "secret-token" || saved.CloudID != "cloud-1" || saved.SiteURL != "https://site.example.com" {
		t.Errorf("saved = %+v", saved)
	}
	if saved.SiteName != "Ada Lovelace" {
		t.Errorf("SiteName = %q, want the verified account's display name", saved.SiteName)
	}
	if !strings.Contains(stdout.String(), savedPath) {
		t.Errorf("stdout does not say where credentials went: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "secret-token") {
		t.Errorf("stdout leaked the token: %q", stdout.String())
	}
}

// TestLoginRefusesToSaveWhenMyselfRejects checks the ordering constraint
// itself: an httptest server standing in for Jira returns 401, and no file
// must be written as a result.
func TestLoginRefusesToSaveWhenMyselfRejects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	verify := func(ctx context.Context, creds jirapkg.Credentials) (jirapkg.User, error) {
		resp, err := http.Get(srv.URL)
		if err != nil {
			return jirapkg.User{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return jirapkg.User{}, errors.New("jira rejected the credentials (HTTP 401)")
		}
		return jirapkg.User{}, nil
	}

	saveCalled := false
	save := func(c jirapkg.Credentials) (string, error) {
		saveCalled = true
		return "", nil
	}

	stdin := strings.NewReader("a@example.com\nsecret-token\ncloud-1\nhttps://site.example.com\n")
	var stdout bytes.Buffer
	err := runAuthLogin(stdin, &stdout, verify, save)
	if err == nil {
		t.Fatal("want an error when Jira rejects the credentials")
	}
	if saveCalled {
		t.Error("save was called despite a rejected verification")
	}
}

func TestLoginRejectsEmailWithoutAt(t *testing.T) {
	stdin := strings.NewReader("not-an-email\nsecret-token\ncloud-1\nhttps://site.example.com\n")
	saveCalled := false
	save := func(c jirapkg.Credentials) (string, error) { saveCalled = true; return "", nil }
	err := runAuthLogin(stdin, &bytes.Buffer{}, fakeVerify(jirapkg.User{}, nil), save)
	if err == nil {
		t.Fatal("want an error for an email with no @")
	}
	if saveCalled {
		t.Error("save was called despite an invalid email")
	}
}

// TestLoginRejectsEmptyRequiredFields checks the constraint that a blank
// answer must error rather than silently becoming some default value - the
// migration plan explicitly forbids baking in a company cloud ID or site
// URL, and an unchecked empty answer would be indistinguishable from one.
func TestLoginRejectsEmptyRequiredFields(t *testing.T) {
	cases := []struct {
		name  string
		stdin string
	}{
		{"empty cloud id", "a@example.com\nsecret-token\n\nhttps://site.example.com\n"},
		{"empty site url", "a@example.com\nsecret-token\ncloud-1\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saveCalled := false
			save := func(c jirapkg.Credentials) (string, error) { saveCalled = true; return "", nil }
			err := runAuthLogin(strings.NewReader(tc.stdin), &bytes.Buffer{}, fakeVerify(jirapkg.User{}, nil), save)
			if err == nil {
				t.Fatal("want an error for a blank required field")
			}
			if saveCalled {
				t.Error("save was called despite a blank required field")
			}
		})
	}
}

// TestAuthStatusNeverPrintsTheTokenViaEnv covers the env-credentials source:
// even with a token sitting in JIRA_API_TOKEN, neither table nor json output
// may contain it.
func TestAuthStatusNeverPrintsTheTokenViaEnv(t *testing.T) {
	t.Setenv("JIRA_EMAIL", "a@example.com")
	t.Setenv("JIRA_API_TOKEN", "top-secret-token")
	t.Setenv("JIRA_CLOUD_ID", "cloud-1")
	t.Setenv("JIRA_SITE_URL", "https://site.example.com")

	verify := fakeVerify(jirapkg.User{DisplayName: "Ada Lovelace", AccountID: "acc-1", Active: true}, nil)

	for _, format := range []outputFormat{formatTable, formatJSON} {
		var stdout bytes.Buffer
		if err := runAuthStatus(&stdout, format, verify); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if strings.Contains(stdout.String(), "top-secret-token") {
			t.Errorf("%s: output leaked the token: %q", format, stdout.String())
		}
		if !strings.Contains(stdout.String(), "environment") {
			t.Errorf("%s: output does not name env as the source: %q", format, stdout.String())
		}
	}
}

// TestAuthStatusNeverPrintsTheTokenViaFile covers the file-credentials
// source, saved through the real SaveCredentials so the fixture is exactly
// what login would have produced.
func TestAuthStatusNeverPrintsTheTokenViaFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, v := range []string{"JIRA_EMAIL", "JIRA_API_TOKEN", "JIRA_CLOUD_ID"} {
		t.Setenv(v, "")
	}
	if _, err := jirapkg.SaveCredentials(jirapkg.Credentials{
		Email: "a@example.com", APIToken: "top-secret-token", CloudID: "cloud-1", SiteURL: "https://site.example.com",
	}); err != nil {
		t.Fatal(err)
	}

	verify := fakeVerify(jirapkg.User{DisplayName: "Ada Lovelace", AccountID: "acc-1", Active: true}, nil)

	var stdout bytes.Buffer
	if err := runAuthStatus(&stdout, formatJSON, verify); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "top-secret-token") {
		t.Errorf("output leaked the token: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "file") {
		t.Errorf("output does not name the file as the source: %q", stdout.String())
	}
}

// TestAuthStatusReportsAnUnverifiedAccountAsAnError makes sure a rejected
// verification surfaces as a non-nil error (and thus a non-zero exit via
// run), not a silent "verified: no" that a caller might not notice.
func TestAuthStatusReportsAnUnverifiedAccountAsAnError(t *testing.T) {
	t.Setenv("JIRA_EMAIL", "a@example.com")
	t.Setenv("JIRA_API_TOKEN", "bad-token")
	t.Setenv("JIRA_CLOUD_ID", "cloud-1")

	verify := fakeVerify(jirapkg.User{}, errors.New("jira rejected the credentials (HTTP 401)"))
	var stdout bytes.Buffer
	err := runAuthStatus(&stdout, formatTable, verify)
	if err == nil {
		t.Fatal("want an error when verification fails")
	}
	if strings.Contains(stdout.String(), "bad-token") {
		t.Errorf("output leaked the token: %q", stdout.String())
	}
}
