package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// NearRateLimit must flip on when a response carries
// X-RateLimit-NearLimit: true, and flip back off on the next response that
// does not - "latest observation wins", per its own doc comment, not a
// sticky flag that stays set once tripped.
func TestClientNearRateLimitReflectsMostRecentResponse(t *testing.T) {
	near := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if near {
			w.Header().Set("X-RateLimit-NearLimit", "true")
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	if c.NearRateLimit() {
		t.Fatal("NearRateLimit should start false")
	}

	if err := c.do(context.Background(), http.MethodGet, "/myself", nil, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	if !c.NearRateLimit() {
		t.Error("NearRateLimit should be true after a response carrying the header")
	}

	near = false
	if err := c.do(context.Background(), http.MethodGet, "/myself", nil, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	if c.NearRateLimit() {
		t.Error("NearRateLimit should be false after a response without the header")
	}
}
