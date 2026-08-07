package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const httpTimeout = 30 * time.Second

// Client is an authenticated door to one Jira Cloud site. It holds no
// mutable state beyond the http.Client's own connection pool, so a single
// Client is safe to share across goroutines.
type Client struct {
	creds Credentials
	http  *http.Client

	// baseURL overrides the api.atlassian.com host for tests. Production
	// leaves it empty and builds the real host from creds.CloudID; a test
	// points it at an httptest.Server instead of adding a second constructor.
	baseURL string
}

// NewClient builds a Client authenticated as creds. It performs no network
// call - a bad token only surfaces on the first request, which is when this
// program can actually say what went wrong.
func NewClient(creds Credentials) *Client {
	return &Client{creds: creds, http: &http.Client{Timeout: httpTimeout}}
}

// BaseURL is the root of the platform REST API: issues, comments, fields,
// search.
func (c *Client) BaseURL() string {
	if c.baseURL != "" {
		return c.baseURL + "/rest/api/3"
	}
	return "https://api.atlassian.com/ex/jira/" + c.creds.CloudID + "/rest/api/3"
}

// AgileURL is the root of the Agile API: boards and sprints. A separate root
// because Atlassian versions and deprecates it independently of the platform
// API.
func (c *Client) AgileURL() string {
	if c.baseURL != "" {
		return c.baseURL + "/rest/agile/1.0"
	}
	return "https://api.atlassian.com/ex/jira/" + c.creds.CloudID + "/rest/agile/1.0"
}

// jiraErrorBody is the shape Jira's own error responses take. Neither field
// is guaranteed to be present - a 500 from an edge proxy carries neither -
// which is why do() falls back to the bare status code when both are empty.
type jiraErrorBody struct {
	ErrorMessages []string          `json:"errorMessages"`
	Errors        map[string]string `json:"errors"`
}

// do sends one request against path (relative to BaseURL, e.g. "/myself")
// and decodes a 2xx JSON body into out. A nil out discards the body, for
// endpoints called only for their side effect.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	return c.doAt(ctx, c.BaseURL(), method, path, body, out)
}

// doAgile is do's Agile-API counterpart: same request/error handling,
// rooted at AgileURL instead of BaseURL. Boards and sprints (create.go's
// ActiveSprint) live on that separate root, per client.go's own note on why
// AgileURL exists.
func (c *Client) doAgile(ctx context.Context, method, path string, body, out any) error {
	return c.doAt(ctx, c.AgileURL(), method, path, body, out)
}

// doAt is do and doAgile's shared body, parameterised on which API root to
// hit - the two APIs differ only in that root, never in auth, headers, or
// error handling.
func (c *Client) doAt(ctx context.Context, root, method, path string, body, out any) error {
	var bodyReader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, root+path, bodyReader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	// Basic auth over HTTPS, per Atlassian's own API: the token never touches
	// a query string or a log line this way.
	req.SetBasicAuth(c.creds.Email, c.creds.APIToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// The error from net/http can embed the request URL, but never the
		// Authorization header, so nothing here needs scrubbing.
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response for %s %s: %w", method, path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpError(resp.StatusCode, respBody)
	}

	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decoding response for %s %s: %w", method, path, err)
	}
	return nil
}

// httpError turns a non-2xx response into an error a person can act on. 401
// and 403 both mean the token Jira was handed is no good, whether it expired
// or was never valid, and the fix in either case is the same command.
func httpError(status int, body []byte) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("jira rejected the credentials (HTTP %d): run %q", status, "jira auth login")
	}

	var parsed jiraErrorBody
	// A body that is not the shape Jira's own errors take (an edge proxy's
	// HTML, say) is not itself an error worth reporting - the status code
	// below still says what happened.
	_ = json.Unmarshal(body, &parsed)

	var msg string
	switch {
	case len(parsed.ErrorMessages) > 0:
		msg = parsed.ErrorMessages[0]
	case len(parsed.Errors) > 0:
		for field, reason := range parsed.Errors {
			msg = fmt.Sprintf("%s: %s", field, reason)
			break
		}
	}
	if msg == "" {
		return fmt.Errorf("jira returned HTTP %d", status)
	}
	return fmt.Errorf("jira returned HTTP %d: %s", status, msg)
}
