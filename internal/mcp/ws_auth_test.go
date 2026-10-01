package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// A long-running server re-authenticates when its session lapses. The config it
// was constructed with still holds the map from startup, so anything reading
// that instead of the client is holding a dead session — ordinary calls keep
// working while every WebSocket fails, which points at the WebSocket.
//
// Both ZADT_VSP clients the server opens -- the debug client the transport
// merge, move, add and remove tools share, and the AMDP one -- must send the
// session the ADT client holds now, and only that.
func TestWSAuthFollowsTheRefreshedSession(t *testing.T) {
	var mu sync.Mutex
	var cookies, basics []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" {
			mu.Lock()
			cookies = append(cookies, r.Header.Get("Cookie"))
			basics = append(basics, r.Header.Get("Authorization"))
			mu.Unlock()
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	startup := map[string]string{"SAP_SESSIONID_DEV_100": "lapsed"}
	s := NewServer(&Config{
		BaseURL: srv.URL, Client: "100", Language: "EN",
		Mode: "hyperfocused", Cookies: startup,
	})

	// What a re-authentication does: replace the map wholesale.
	s.adtClient.SetCookies(map[string]string{"SAP_SESSIONID_DEV_100": "fresh"})

	if err := s.ensureDebugWSClient(context.Background()); err == nil {
		t.Fatal("the server refuses every upgrade; the debug WebSocket connected")
	}
	if res := s.ensureWSConnected(context.Background(), "AMDPDebuggerStart"); res == nil {
		t.Fatal("the server refuses every upgrade; the AMDP WebSocket connected")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(cookies) != 2 {
		t.Fatalf("saw %d WebSocket upgrades, want 2", len(cookies))
	}
	for i, c := range cookies {
		if c != "SAP_SESSIONID_DEV_100=fresh" {
			t.Errorf("upgrade %d carried cookie %q, want the refreshed session", i, c)
		}
		if basics[i] != "" {
			t.Errorf("upgrade %d carried Authorization %q alongside the session", i, basics[i])
		}
	}
}
