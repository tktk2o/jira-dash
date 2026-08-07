package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	jirapkg "jira-dash/internal/jira"

	"golang.org/x/term"
)

// verifyFunc checks a set of credentials against Jira before they are
// trusted for anything - a parameter (not a bare call to jirapkg.NewClient)
// so runAuthLogin and runAuthStatus's tests can hand in a fake that never
// reaches the network, per the migration plan's ban on tests doing that.
type verifyFunc func(ctx context.Context, creds jirapkg.Credentials) (jirapkg.User, error)

// saveFunc persists credentials - a parameter for the same testing reason
// as verifyFunc, and so runAuthLogin never has to know it is
// jirapkg.SaveCredentials underneath.
type saveFunc func(jirapkg.Credentials) (string, error)

// verifyCredentials is the production verifyFunc: an unauthenticated-looking
// set of credentials only actually gets tested by asking Jira who they
// belong to.
func verifyCredentials(ctx context.Context, creds jirapkg.Credentials) (jirapkg.User, error) {
	return jirapkg.NewClient(creds).Myself(ctx)
}

// runAuth dispatches `jira auth login`/`jira auth status`, the one
// subcommand pair that must work before any credentials exist - main.go
// calls this without building a client first, for exactly that reason.
func runAuth(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: jira auth <login|status>")
	}
	switch args[0] {
	case "login":
		return runAuthLogin(stdin, stdout, verifyCredentials, jirapkg.SaveCredentials)
	case "status":
		fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
		var format string
		fs.StringVar(&format, "f", "table", "output format")
		fs.StringVar(&format, "format", "table", "output format")
		flagArgs, _ := splitArgs(args[1:], map[string]bool{"f": true, "format": true})
		if err := fs.Parse(flagArgs); err != nil {
			return err
		}
		f, err := parseFormat(format)
		if err != nil {
			return err
		}
		return runAuthStatus(stdout, f, verifyCredentials)
	default:
		return fmt.Errorf("unknown auth subcommand %q", args[0])
	}
}

// runAuthLogin prompts for the four fields the old CLI's login also asked
// for, in the same order, verifies them against Jira before saving anything,
// and only then writes the credentials file. No field defaults to a
// company-specific value (the migration plan's own constraint on this task):
// an empty answer for cloudID or siteURL is an error, not a silent fallback.
func runAuthLogin(stdin io.Reader, stdout io.Writer, verify verifyFunc, save saveFunc) error {
	r := bufio.NewReader(stdin)

	email, err := promptLine(r, stdout, "Email: ")
	if err != nil {
		return err
	}
	if !strings.Contains(email, "@") {
		return fmt.Errorf("that does not look like an email address (no @): %q", email)
	}

	token, err := promptSecret(r, stdin, stdout, "API token: ")
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("an API token is required")
	}

	cloudID, err := promptLine(r, stdout, "Cloud ID: ")
	if err != nil {
		return err
	}
	if cloudID == "" {
		return fmt.Errorf("a cloud ID is required")
	}

	siteURL, err := promptLine(r, stdout, "Site URL: ")
	if err != nil {
		return err
	}
	if siteURL == "" {
		return fmt.Errorf("a site URL is required")
	}

	creds := jirapkg.Credentials{Email: email, APIToken: token, CloudID: cloudID, SiteURL: siteURL}

	// Verify before save: writing a file that fails the very next request
	// would leave the person worse off than the "not logged in" state
	// LoadCredentials already reports clearly.
	user, err := verify(context.Background(), creds)
	if err != nil {
		return fmt.Errorf("could not verify these credentials against Jira, nothing was saved: %w", err)
	}
	// siteName is the account's own display name, matching what the old CLI
	// stored there - it is not asked for, since Jira already knows it.
	creds.SiteName = user.DisplayName

	path, err := save(creds)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Logged in as %s. Credentials written to %s.\n", user.DisplayName, path)
	return nil
}

// promptLine writes prompt and reads one line of visible input.
func promptLine(r *bufio.Reader, stdout io.Writer, prompt string) (string, error) {
	_, _ = fmt.Fprint(stdout, prompt)
	return readLine(r)
}

// promptSecret writes prompt and reads one line without echoing it, when
// stdin is an actual terminal. A piped/test stdin has nothing to echo in the
// first place, so it falls back to a plain line read rather than failing -
// term.ReadPassword requires a real file descriptor, which a bytes.Reader
// (as every test here uses) does not have.
func promptSecret(r *bufio.Reader, stdin io.Reader, stdout io.Writer, prompt string) (string, error) {
	_, _ = fmt.Fprint(stdout, prompt)
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		// ReadPassword consumes the trailing newline without echoing
		// anything, including a line break - print one so the next prompt
		// does not land on the same line as this one.
		_, _ = fmt.Fprintln(stdout)
		if err != nil {
			return "", fmt.Errorf("reading the API token: %w", err)
		}
		return string(b), nil
	}
	return readLine(r)
}

// readLine reads up to the next newline, or to EOF if the input ends
// without one - a script piping the last field with no trailing newline is
// still a valid answer, not a read error.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// runAuthStatus reports which source (env or the file) LoadCredentials
// would resolve right now, then verifies those credentials against Jira.
// The token itself never appears in creds' printed fields or in verify's
// result - Credentials.APIToken and User carry nothing else that could leak
// it, so there is no field here that needs redacting.
func runAuthStatus(stdout io.Writer, format outputFormat, verify verifyFunc) error {
	creds, err := jirapkg.LoadCredentials()
	if err != nil {
		return err
	}
	source := jirapkg.CredentialsSource()

	user, verifyErr := verify(context.Background(), creds)
	if err := writeAuthStatusOutput(stdout, format, source, creds, user, verifyErr); err != nil {
		return err
	}
	if verifyErr != nil {
		return fmt.Errorf("credentials are saved but Jira rejected them: %w", verifyErr)
	}
	return nil
}

// authStatusOutput is `jira auth status -f json`'s shape - a new command
// with no prior CLI output to match. Every field here is safe to print:
// none of them is the token.
type authStatusOutput struct {
	Source      string `json:"source"`
	Email       string `json:"email"`
	CloudID     string `json:"cloud_id"`
	SiteURL     string `json:"site_url"`
	Verified    bool   `json:"verified"`
	DisplayName string `json:"display_name,omitempty"`
	AccountID   string `json:"account_id,omitempty"`
	Active      bool   `json:"active,omitempty"`
	Error       string `json:"error,omitempty"`
}

func writeAuthStatusOutput(w io.Writer, format outputFormat, source string, creds jirapkg.Credentials, user jirapkg.User, verifyErr error) error {
	out := authStatusOutput{
		Source:   source,
		Email:    creds.Email,
		CloudID:  creds.CloudID,
		SiteURL:  creds.SiteURL,
		Verified: verifyErr == nil,
	}
	if verifyErr != nil {
		out.Error = verifyErr.Error()
	} else {
		out.DisplayName = user.DisplayName
		out.AccountID = user.AccountID
		out.Active = user.Active
	}
	if format == formatJSON {
		return writeJSON(w, out)
	}

	_, _ = fmt.Fprintf(w, "Source:   %s\n", source)
	_, _ = fmt.Fprintf(w, "Email:    %s\n", creds.Email)
	_, _ = fmt.Fprintf(w, "Cloud ID: %s\n", creds.CloudID)
	_, _ = fmt.Fprintf(w, "Site URL: %s\n", creds.SiteURL)
	if verifyErr != nil {
		_, _ = fmt.Fprintf(w, "Verified: no (%s)\n", verifyErr)
		return nil
	}
	_, _ = fmt.Fprintln(w, "Verified: yes")
	_, _ = fmt.Fprintf(w, "Name:     %s\n", user.DisplayName)
	_, _ = fmt.Fprintf(w, "Account:  %s\n", user.AccountID)
	_, _ = fmt.Fprintf(w, "Active:   %v\n", user.Active)
	return nil
}
