package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// defaultTimeout bounds a single HTTP round trip. Without it a hung
// connection (a dead proxy, a Jira instance that stopped answering) blocks
// this program forever instead of surfacing as an error the caller can act
// on.
const defaultTimeout = 30 * time.Second

// maxAttempts caps doAt's retry loop, including the first try. Retrying
// forever on a Jira outage just delays the same failure the caller would
// get from attempt 1; three tries is enough to ride out a blip without
// making a broken Jira feel hung.
const maxAttempts = 3

// retryBaseDelay is doAt's exponential-backoff starting point (doubling each
// attempt: 200ms, 400ms, ...). Tests override it via a package var, rather
// than a parameter threaded through every call, so client_test.go can run
// the same retry loop in milliseconds instead of seconds.
var retryBaseDelay = 200 * time.Millisecond

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

	// nearRateLimit is the most recent response's X-RateLimit-NearLimit
	// observation. Jira sends this header on every successful response, not
	// just 429s, as an early-warning signal before throttling actually
	// kicks in. A plain bool would race under the Client-is-shared-across-
	// goroutines contract above, so this is an atomic.Bool; "latest
	// observation wins" is simple and good enough for a pressure signal
	// that is inherently a snapshot, not a running total.
	nearRateLimit atomic.Bool
}

// NearRateLimit reports whether the most recent response this Client
// received carried X-RateLimit-NearLimit: true - Jira's own warning that
// this token is close to being throttled. Callers can use it to back off
// voluntarily (e.g. widen a polling interval) before a 429 forces the
// issue. It reflects only the latest response; a Client freshly built, or
// one whose most recent call did not include the header, reports false.
func (c *Client) NearRateLimit() bool {
	return c.nearRateLimit.Load()
}

// NewClient builds a Client authenticated as creds. It performs no network
// call - a bad token only surfaces on the first request, which is when this
// program can actually say what went wrong.
func NewClient(creds Credentials) *Client {
	return &Client{creds: creds, http: &http.Client{Timeout: defaultTimeout}}
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
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepForRetry(ctx, retryBaseDelay, attempt, lastErr); err != nil {
				return err
			}
		}

		var bodyReader io.Reader
		if encoded != nil {
			bodyReader = bytes.NewReader(encoded)
		}

		req, err := http.NewRequestWithContext(ctx, method, root+path, bodyReader)
		if err != nil {
			return fmt.Errorf("building request: %w", err)
		}
		// GetBody lets net/http (and this loop, on retry) re-read the body
		// after a redirect or a failed attempt has already drained the
		// bytes.Reader above once.
		if encoded != nil {
			req.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(encoded)), nil
			}
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
			// A cancelled/expired context surfaces through http.Client as a
			// wrapped context error; retrying it would just spin until the
			// same context error fires again, so stop here instead.
			if ctx.Err() != nil {
				return fmt.Errorf("%s %s: %w", method, path, err)
			}
			lastErr = fmt.Errorf("%s %s: %w", method, path, err)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("reading response for %s %s: %w", method, path, err)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if !isRetryableStatus(resp.StatusCode) {
				return httpError(resp.StatusCode, respBody)
			}
			lastErr = retryableStatusError{status: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")), body: respBody}
			continue
		}

		// "latest observation wins" - recorded on every successful
		// response, whether or not the header was present, so a client
		// that stops seeing the header (rate pressure eased) also stops
		// reporting NearRateLimit.
		c.nearRateLimit.Store(resp.Header.Get("X-RateLimit-NearLimit") == "true")

		if out == nil || len(respBody) == 0 {
			return nil
		}
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding response for %s %s: %w", method, path, err)
		}
		return nil
	}

	if rse, ok := lastErr.(retryableStatusError); ok {
		return httpError(rse.status, rse.body)
	}
	return lastErr
}

// isRetryableStatus is Jira saying "ask again later" (429) or "something on
// my end broke" (5xx). Any other 4xx means the request itself was wrong, and
// retrying an unchanged wrong request just gets the same rejection.
func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// retryableStatusError carries a retryable non-2xx response through the
// retry loop so the final attempt's httpError message (with Jira's own
// wording) is what the caller sees, not a generic "gave up after N tries".
type retryableStatusError struct {
	status     int
	retryAfter time.Duration
	body       []byte
}

func (e retryableStatusError) Error() string {
	return httpError(e.status, e.body).Error()
}

// parseRetryAfter reads a 429's Retry-After header, which Jira sends as a
// count of seconds rather than an HTTP-date. A missing or unparseable
// header returns 0, leaving the caller to fall back to plain exponential
// backoff.
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	seconds, err := strconv.Atoi(header)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// sleepForRetry waits before doAt's next attempt: Retry-After verbatim when
// the previous failure was a 429 that specified one, otherwise exponential
// backoff with jitter (attempt 1 -> base, attempt 2 -> 2*base, ...). Jitter
// keeps many concurrent callers hitting a rate limit from retrying in
// lockstep. It returns early with ctx.Err() if the context is cancelled
// mid-wait, so a caller that gave up does not sit through a needless sleep.
func sleepForRetry(ctx context.Context, base time.Duration, attempt int, lastErr error) error {
	delay := base * time.Duration(1<<(attempt-1))
	if rse, ok := lastErr.(retryableStatusError); ok && rse.retryAfter > 0 {
		delay = rse.retryAfter
	} else {
		delay += time.Duration(rand.Int63n(int64(base)))
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// httpStatusError carries the HTTP status code of a failed request through
// to callers, alongside the human-readable message httpError already builds.
// Without this, a caller that needs to branch on "was this specifically a
// 400" (create.go's ActiveSprint, treating a kanban board's 400 on
// /sprint as "no sprints" rather than a real failure) would have to parse
// the status back out of the formatted error string. errors.As is how
// callers recover it.
type httpStatusError struct {
	statusCode int
	msg        string
}

func (e *httpStatusError) Error() string { return e.msg }

// StatusCode is the HTTP status Jira responded with.
func (e *httpStatusError) StatusCode() int { return e.statusCode }

// httpError turns a non-2xx response into an error a person can act on. 401
// and 403 both mean the token Jira was handed is no good, whether it expired
// or was never valid, and the fix in either case is the same command.
func httpError(status int, body []byte) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return &httpStatusError{
			statusCode: status,
			msg:        fmt.Sprintf("jira rejected the credentials (HTTP %d): run %q", status, "jira auth login"),
		}
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
		msg = fmt.Sprintf("jira returned HTTP %d", status)
	} else {
		msg = fmt.Sprintf("jira returned HTTP %d: %s", status, msg)
	}
	return &httpStatusError{statusCode: status, msg: msg}
}
