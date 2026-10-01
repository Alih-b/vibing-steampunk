package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestRetryRequest_AdoptsTheSessionItWasHandedBack pins the read-back on the
// retry path. The first write is refused for its CSRF token; the retry after
// the refresh succeeds and SAP hands back a new session cookie and a new
// token with it. The next write must send exactly that session and that
// token — before the fix it sent the old session id from the config and the
// token of the refresh, a pair the server rejects.
func TestRetryRequest_AdoptsTheSessionItWasHandedBack(t *testing.T) {
	const cookie = "SAP_SESSIONID_A4H_001"
	var (
		mu     sync.Mutex
		posts  int
		lastRq *http.Request
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost {
			// The CSRF probe.
			w.Header().Set("X-CSRF-Token", "token-refresh")
			w.WriteHeader(http.StatusOK)
			return
		}
		posts++
		lastRq = r.Clone(context.Background())
		switch posts {
		case 1:
			w.WriteHeader(http.StatusForbidden) // CSRF token invalid
		case 2:
			http.SetCookie(w, &http.Cookie{Name: cookie, Value: "session-new", Path: "/"})
			w.Header().Set("X-CSRF-Token", "token-new")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	cfg := NewConfig(srv.URL, "", "", WithCookies(map[string]string{cookie: "session-old"}))
	tr := NewTransport(cfg)
	tr.setCSRFToken("token-stale")

	post := func() {
		t.Helper()
		if _, err := tr.Request(context.Background(), "/sap/bc/adt/demo", &RequestOptions{Method: http.MethodPost}); err != nil {
			t.Fatalf("request: %v", err)
		}
	}
	post() // 403, refresh, retry hands back the new session
	post() // must use it

	mu.Lock()
	defer mu.Unlock()
	if posts != 3 {
		t.Fatalf("server saw %d writes, want 3", posts)
	}
	if got := lastRq.Header.Get("X-CSRF-Token"); got != "token-new" {
		t.Errorf("next write sent X-CSRF-Token %q, want token-new", got)
	}
	var values []string
	for _, c := range lastRq.Cookies() {
		if c.Name == cookie {
			values = append(values, c.Value)
		}
	}
	// The jar and the configured cookies may both carry it, as they do after
	// any Request; what must not happen is the dead session riding along.
	if len(values) == 0 || strings.Contains(strings.Join(values, ","), "session-old") {
		t.Errorf("next write sent %s=%s, want only session-new", cookie, strings.Join(values, ","))
	}
}
