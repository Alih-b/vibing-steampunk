package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// reportTestServer is an MCP server whose SAP is an httptest server that only
// counts requests: the WebSocket handshake RunReport needs is one of them.
func reportTestServer(t *testing.T, readOnly bool) (*Server, func() int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "no", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return NewServer(&Config{
		BaseURL:  srv.URL,
		Username: "TESTUSER",
		Password: "p",
		Client:   "001",
		Language: "EN",
		ReadOnly: readOnly,
	}), hits.Load
}

func TestRunReport_RefusedUnderReadOnlyBeforeTheWebSocket(t *testing.T) {
	for _, objectType := range []string{"RUN_REPORT", "RUN_REPORT_ASYNC"} {
		t.Run(objectType, func(t *testing.T) {
			s, hits := reportTestServer(t, true)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res, handled, err := s.routeReportAction(ctx, "debug", objectType, "", map[string]any{"report": "ZDEMO_REPORT"})
			if !handled || err != nil {
				t.Fatalf("handled=%v err=%v", handled, err)
			}
			if !res.IsError || !strings.Contains(toolResultText(t, res), "blocked by safety configuration") {
				t.Fatalf("want a safety refusal, got %q", toolResultText(t, res))
			}
			if n := hits(); n != 0 {
				t.Errorf("a refused report still reached SAP %d time(s)", n)
			}
		})
	}
}

func TestRunReport_WritableStillReachesSAP(t *testing.T) {
	s, hits := reportTestServer(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, _, _ := s.routeReportAction(ctx, "debug", "RUN_REPORT", "", map[string]any{"report": "ZDEMO_REPORT"})
	if res != nil && strings.Contains(toolResultText(t, res), "blocked") {
		t.Fatalf("RunReport refused without --read-only: %q", toolResultText(t, res))
	}
	if hits() == 0 {
		t.Error("RunReport never reached SAP without --read-only")
	}
}

func TestSetTextElements_RefusedUnderReadOnlyBeforeTheWebSocket(t *testing.T) {
	s, hits := reportTestServer(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, handled, err := s.routeReportAction(ctx, "debug", "SET_TEXT_ELEMENTS", "", map[string]any{
		"program": "ZDEMO_REPORT", "text_symbols": `{"001":"Hello"}`,
	})
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !res.IsError || !strings.Contains(toolResultText(t, res), "blocked by safety configuration") {
		t.Fatalf("want a safety refusal, got %q", toolResultText(t, res))
	}
	if n := hits(); n != 0 {
		t.Errorf("a refused SetTextElements still reached SAP %d time(s)", n)
	}
}

func TestTextElements_ReadsAndWritableWritesReachSAP(t *testing.T) {
	cases := map[string]struct {
		readOnly   bool
		objectType string
	}{
		"read-only get": {true, "GET_TEXT_ELEMENTS"},
		"writable set":  {false, "SET_TEXT_ELEMENTS"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, hits := reportTestServer(t, tc.readOnly)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res, _, _ := s.routeReportAction(ctx, "debug", tc.objectType, "", map[string]any{
				"program": "ZDEMO_REPORT", "text_symbols": `{"001":"Hello"}`,
			})
			if res != nil && strings.Contains(toolResultText(t, res), "blocked") {
				t.Fatalf("refused: %q", toolResultText(t, res))
			}
			if hits() == 0 {
				t.Error("never reached SAP")
			}
		})
	}
}
