package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// upgradeRecorder refuses every request and keeps the headers of the last
// WebSocket upgrade it saw.
func upgradeRecorder(t *testing.T) (*httptest.Server, func() http.Header) {
	t.Helper()
	var mu sync.Mutex
	var last http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" {
			mu.Lock()
			last = r.Header.Clone()
			mu.Unlock()
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	return srv, func() http.Header {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

func TestWebSocketFromClient_CarriesTheSessionNotThePassword(t *testing.T) {
	srv, last := upgradeRecorder(t)
	c := NewClient(srv.URL, "TESTUSER", "s3cret", WithClient("100"),
		WithCookies(map[string]string{"MYSAPSSO2": "startup"}))
	// A re-authentication replaces the session wholesale.
	c.SetCookies(map[string]string{"MYSAPSSO2": "renewed"})

	for name, connect := range map[string]func(context.Context) error{
		"debug": c.NewDebugWebSocketClient().Connect,
		"amdp":  c.NewAMDPWebSocketClient().Connect,
	} {
		if err := connect(context.Background()); err == nil {
			t.Fatalf("%s: the server refuses every upgrade; Connect succeeded", name)
		}
		h := last()
		if h == nil {
			t.Fatalf("%s: no WebSocket upgrade reached the server", name)
		}
		if got := h.Get("Cookie"); got != "MYSAPSSO2=renewed" {
			t.Errorf("%s: upgrade carried cookie %q, want the client's current session", name, got)
		}
		if got := h.Get("Authorization"); got != "" {
			t.Errorf("%s: upgrade carried Authorization %q alongside the session", name, got)
		}
	}

	if _, password, _ := c.wsCredentials(); password != "" {
		t.Error("a WebSocket built for a session-authenticated client was handed the password")
	}
}

func TestWebSocketFromClient_PasswordWithoutSession(t *testing.T) {
	srv, last := upgradeRecorder(t)
	c := NewClient(srv.URL, "TESTUSER", "s3cret", WithClient("100"))

	if err := c.NewDebugWebSocketClient().Connect(context.Background()); err == nil {
		t.Fatal("the server refuses every upgrade; Connect succeeded")
	}
	h := last()
	if h == nil {
		t.Fatal("no WebSocket upgrade reached the server")
	}
	want, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	want.SetBasicAuth("TESTUSER", "s3cret")
	if got := h.Get("Authorization"); got != want.Header.Get("Authorization") {
		t.Errorf("upgrade carried Authorization %q, want the client's basic auth", got)
	}
	if got := h.Get("Cookie"); got != "" {
		t.Errorf("upgrade carried cookie %q from a password client", got)
	}
}

// The APC probe in the compatibility check builds its WebSocket the same way,
// so it sees the session the client holds now.
func TestAPCProbeUsesTheCurrentSession(t *testing.T) {
	srv, last := upgradeRecorder(t)
	c := NewClient(srv.URL, "", "", WithClient("100"),
		WithCookies(map[string]string{"MYSAPSSO2": "startup"}))
	c.SetCookies(map[string]string{"MYSAPSSO2": "renewed"})

	c.probeAPCTunnel(context.Background(), CompatCheck{ID: apcCheckID})
	h := last()
	if h == nil {
		t.Fatal("the probe never attempted the WebSocket upgrade")
	}
	if got := h.Get("Cookie"); got != "MYSAPSSO2=renewed" {
		t.Errorf("probe carried cookie %q, want the renewed session", got)
	}
}

// A client on a password picks up SAP's session cookies in its jar as it
// works. Those must not leak into CurrentCookies, or the WebSocket built from
// the client would switch from basic auth to a cookie it was never configured
// with.
func TestWebSocketFromBasicAuthClient_StaysOnBasicAfterRequests(t *testing.T) {
	var mu sync.Mutex
	var upgrades []http.Header
	var httpCookies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "" {
			mu.Lock()
			httpCookies = append(httpCookies, r.Header.Get("Cookie"))
			mu.Unlock()
		} else {
			mu.Lock()
			upgrades = append(upgrades, r.Header.Clone())
			mu.Unlock()
			w.WriteHeader(http.StatusForbidden)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SAP_SESSIONID_DEV_100", Value: "issued", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "sap-usercontext", Value: "sap-client=100", Path: "/"})
		w.Header().Set("x-csrf-token", "token")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "TESTUSER", "s3cret", WithClient("100"))
	for i := 0; i < 2; i++ {
		if _, err := c.transport.Request(context.Background(), "/sap/bc/adt/core/discovery", &RequestOptions{Method: http.MethodGet}); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	mu.Lock()
	jarInPlay := false
	for _, ck := range httpCookies {
		jarInPlay = jarInPlay || strings.Contains(ck, "SAP_SESSIONID_DEV_100=issued")
	}
	mu.Unlock()
	if !jarInPlay {
		t.Fatalf("the HTTP requests never sent back the issued session (%q); the test proves nothing", httpCookies)
	}
	if got := c.CurrentCookies(); len(got) != 0 {
		t.Fatalf("CurrentCookies() = %v after requests on basic auth, want empty", got)
	}

	for name, connect := range map[string]func(context.Context) error{
		"debug": c.NewDebugWebSocketClient().Connect,
		"amdp":  c.NewAMDPWebSocketClient().Connect,
	} {
		if err := connect(context.Background()); err == nil {
			t.Fatalf("%s: the server refuses every upgrade; Connect succeeded", name)
		}
	}

	want, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	want.SetBasicAuth("TESTUSER", "s3cret")
	mu.Lock()
	defer mu.Unlock()
	if len(upgrades) != 2 {
		t.Fatalf("saw %d WebSocket upgrades, want 2", len(upgrades))
	}
	for i, h := range upgrades {
		if got := h.Get("Authorization"); got != want.Header.Get("Authorization") {
			t.Errorf("upgrade %d carried Authorization %q, want the client's basic auth", i, got)
		}
		if got := h.Get("Cookie"); got != "" {
			t.Errorf("upgrade %d carried cookie %q from a basic-auth client", i, got)
		}
	}
}
