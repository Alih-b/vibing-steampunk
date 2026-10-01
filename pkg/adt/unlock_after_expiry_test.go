package adt

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// cancelAfterPutRT ends the caller's context as soon as the source PUT has
// answered, so the write lands and the UNLOCK after it finds an expired
// context: the timing of a call budget that runs out inside the lock window.
type cancelAfterPutRT struct {
	inner  http.RoundTripper
	cancel context.CancelFunc
}

func (rt *cancelAfterPutRT) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.inner.RoundTrip(req)
	if err == nil && req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/source/main") {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		rt.cancel()
	}
	return resp, err
}

// cancelAfterPut makes c's HTTP client cancel ctx once a source PUT is done.
func cancelAfterPut(t *testing.T, c *Client, cancel context.CancelFunc) {
	t.Helper()
	hc, ok := c.transport.httpClient.(*http.Client)
	if !ok {
		t.Fatal("test needs the transport's *http.Client")
	}
	inner := hc.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	hc.Transport = &cancelAfterPutRT{inner: inner, cancel: cancel}
}

// An UNLOCK after a successful write, on a context that has just expired,
// fails before it is sent. It must not count as done: the detached release
// has to send it.
func TestExecuteABAPUnlockAfterExpiryIsStillSent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := &executeCleanupServer{}
	c := srv.start(t)
	cancelAfterPut(t, c, cancel)

	result, err := c.ExecuteABAP(ctx, "lv_result = 'ok'.", &ExecuteABAPOptions{KeepProgram: true})
	if err != nil {
		t.Fatalf("ExecuteABAP: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("test setup did not cancel the context after the PUT")
	}
	if got := srv.count("unlock"); got != 1 {
		t.Fatalf("UNLOCK requests = %d, want 1: the lock on the temp program leaked", got)
	}
	if !strings.Contains(result.Message, "released on a retry") {
		t.Fatalf("message = %q, want it to say the lock was released", result.Message)
	}
}

// deployUnlockServer answers a DeployFromFile of an existing program and
// counts the UNLOCK requests that reach it.
type deployUnlockServer struct {
	mu      sync.Mutex
	unlocks int
}

func (s *deployUnlockServer) start(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/discovery"):
			w.Header().Set("X-CSRF-Token", "TOKEN")
		case strings.Contains(r.URL.Path, "/checkruns"):
			w.Header().Set("Content-Type", "application/vnd.sap.adt.checkmessages+xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><chkl:messages xmlns:chkl="http://www.sap.com/adt/checklist"/>`))
			return
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>
<LOCK_HANDLE>HANDLE-1</LOCK_HANDLE><MODIFICATION_SUPPORT>Modification</MODIFICATION_SUPPORT>
</DATA></asx:values></asx:abap>`))
			return
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "UNLOCK":
			s.mu.Lock()
			s.unlocks++
			s.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	cfg := NewConfig(srv.URL, "TESTUSER", "secret")
	return NewClientWithTransport(cfg, NewTransport(cfg))
}

func TestDeployUnlockAfterExpiryIsStillSent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zdeploy_expiry.prog.abap")
	if err := os.WriteFile(file, []byte("REPORT zdeploy_expiry.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &deployUnlockServer{}
	c := s.start(t)
	cancelAfterPut(t, c, cancel)

	result, err := c.DeployFromFile(ctx, file, "$TMP", "")
	if err != nil {
		t.Fatalf("DeployFromFile: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("test setup did not cancel the context after the PUT")
	}
	s.mu.Lock()
	unlocks := s.unlocks
	s.mu.Unlock()
	if unlocks != 1 {
		t.Fatalf("UNLOCK requests = %d, want 1: the object was left locked", unlocks)
	}
	if result.Success || !strings.Contains(result.Message, "released on a retry") {
		t.Fatalf("result = %+v, want a failed unlock released on its retry", result)
	}
}
