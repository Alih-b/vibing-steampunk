package mcp

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
)

// waitForCtx is a long call that only ends when its context does, failing
// the way the ADT client does then.
func waitForCtx(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	<-ctx.Done()
	return newToolResultError("execution failed: " + ctx.Err().Error()), nil
}

func TestLongCallBudgetFromParams(t *testing.T) {
	s := &Server{config: &Config{}}
	res, err := s.longCall(context.Background(), newRequest(map[string]any{"timeout": 0.05}), "execute_abap", waitForCtx)
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(res)
	if !res.IsError || !strings.Contains(text, "execute_abap timed out after 50ms") ||
		!strings.Contains(text, "may still be running on SAP") {
		t.Fatalf("want a timeout message, got %q", text)
	}
	if !strings.Contains(text, "Detail: execution failed: context deadline exceeded") {
		t.Fatalf("the handler's own text must be kept as detail, got %q", text)
	}
}

func TestLongCallServerDefaultBudget(t *testing.T) {
	s := &Server{config: &Config{CallTimeout: 50 * time.Millisecond}}
	done := make(chan *mcp.CallToolResult, 1)
	go func() {
		res, _ := s.longCall(context.Background(), newRequest(map[string]any{}), "deploy_zip", waitForCtx)
		done <- res
	}()
	select {
	case res := <-done:
		if !strings.Contains(resultText(res), "deploy_zip timed out after 50ms") {
			t.Fatalf("got %q", resultText(res))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server default budget did not end the call")
	}
}

func TestLongCallClientCancel(t *testing.T) {
	s := &Server{config: &Config{}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	res, _ := s.longCall(ctx, newRequest(map[string]any{}), "ABAP Unit run", waitForCtx)
	if text := resultText(res); !strings.Contains(text, "ABAP Unit run was cancelled by the client after") ||
		!strings.Contains(text, "may still be running on SAP") {
		t.Fatalf("got %q", text)
	}
}

func TestLongCallPerRequestTimeout(t *testing.T) {
	s := &Server{config: &Config{}}
	h := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New(`Post "https://sap/sap/bc/adt/abapunit/testruns": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`)
	}
	res, err := s.longCall(context.Background(), newRequest(map[string]any{}), "ABAP Unit run", h)
	if err != nil {
		t.Fatalf("want the error turned into a result, got %v", err)
	}
	if text := resultText(res); !strings.Contains(text, "ABAP Unit run timed out after") ||
		!strings.Contains(text, "per-request limit") || !strings.Contains(text, "params.timeout") {
		t.Fatalf("got %q", text)
	}
}

func TestLongCallLeavesOtherOutcomesAlone(t *testing.T) {
	s := &Server{config: &Config{}}
	ok := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("fine"), nil
	}
	res, _ := s.longCall(context.Background(), newRequest(map[string]any{"timeout": float64(30)}), "x", ok)
	if res.IsError || resultText(res) != "fine" {
		t.Fatalf("got %+v", res)
	}
	failed := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return newToolResultError("syntax error in line 3"), nil
	}
	res, _ = s.longCall(context.Background(), newRequest(map[string]any{}), "x", failed)
	if resultText(res) != "syntax error in line 3" {
		t.Fatalf("an ordinary failure must pass through, got %q", resultText(res))
	}
	for _, bad := range []any{float64(0), float64(-5), "soon"} {
		res, _ = s.longCall(context.Background(), newRequest(map[string]any{"timeout": bad}), "x", ok)
		if !res.IsError || !strings.Contains(resultText(res), "timeout must be") {
			t.Fatalf("timeout=%v: got %q", bad, resultText(res))
		}
	}
}

// slowSAP answers every request after delay.
func slowSAP(t *testing.T, delay time.Duration) *Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("X-CSRF-Token", "t")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(fakeEmptyXML))
	}))
	t.Cleanup(ts.Close)
	return NewServer(&Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN", Mode: "hyperfocused"})
}

// The long calls are wired through longCall, from the universal tool too.
func TestLongCallsHonourTimeoutParam(t *testing.T) {
	s := slowSAP(t, 3*time.Second)
	file := filepath.Join(t.TempDir(), "zdemo_long.prog.abap")
	if err := os.WriteFile(file, []byte("REPORT zdemo_long.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		params map[string]any
		action string
		target string
		op     string
	}{
		{"execute_abap", map[string]any{"type": "execute_abap", "code": "lv_result = 1.", "timeout": 0.2}, "analyze", "", "execute_abap timed out"},
		{"unit tests", map[string]any{"object_url": "/sap/bc/adt/oo/classes/zcl_x", "timeout": 0.2}, "test", "", "ABAP Unit run timed out"},
		{"deploy_from_file", map[string]any{"type": "deploy_from_file", "file_path": file, "package_name": "$TMP", "timeout": 0.2}, "system", "", "deploy_from_file timed out"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start := time.Now()
			res, err := s.handleUniversalTool(context.Background(), newRequest(map[string]any{
				"action": c.action, "target": c.target, "params": c.params,
			}))
			if err != nil {
				t.Fatal(err)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatalf("the call ran %s; its 0.2s budget did not end it", time.Since(start))
			}
			if text := resultText(res); !strings.Contains(text, c.op) {
				t.Fatalf("got %q", text)
			}
		})
	}
}

// A client shutting the stdio session down, by closing stdin or by a signal,
// is a clean exit.
func TestServeStdioShutdownIsClean(t *testing.T) {
	s := NewServer(&Config{BaseURL: "http://127.0.0.1:1", Username: "u", Password: "p", Client: "001", Language: "EN", Mode: "hyperfocused"})

	if err := s.serveStdio(context.Background(), strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("stdin closed: got %v, want a clean exit", err)
	}

	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if err := s.serveStdio(ctx, pr, io.Discard); err != nil {
		t.Fatalf("signalled: got %v, want a clean exit", err)
	}
}

// A deploy_zip report runs to kilobytes and ends with its cleanup warnings.
// The timeout message keeps all of it: the LEFT LOCKED line at the very end
// is the one the caller has to act on, and a cut must never split a rune.
func TestLongCallKeepsTheWholeReportOnTimeout(t *testing.T) {
	const locked = "  • CLAS ZCL_DEMO_LAST: LEFT LOCKED — unlock also failed: context deadline exceeded (clear it in SM12, or wait for the ADT session timeout)"
	report := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-ctx.Done()
		var sb strings.Builder
		for i := 0; sb.Len() < 5000; i++ {
			// Multi-byte runes everywhere, so any byte cut is likely to split one.
			sb.WriteString("  [✓] Create CLAS ZCL_DEMO_ÄÖÜ… ok\n")
		}
		sb.WriteString("\nUpload failures:\n")
		sb.WriteString("  • CLAS ZCL_DEMO_X: upload failed: context deadline exceeded\n")
		sb.WriteString("  • CLAS ZCL_DEMO_Y: source uploaded but LEFT LOCKED: context deadline exceeded\n")
		sb.WriteString(locked + "\n")
		return mcp.NewToolResultText(sb.String()), nil
	}
	s := &Server{config: &Config{}}
	res, _ := s.longCall(context.Background(), newRequest(map[string]any{"timeout": 0.05}), "deploy_zip", report)
	text := resultText(res)
	if !res.IsError || !strings.HasPrefix(text, "deploy_zip timed out after 50ms") {
		t.Fatalf("want a timeout message, got %.200q", text)
	}
	if !strings.HasSuffix(text, locked) {
		t.Fatalf("the final LEFT LOCKED line was lost; message ends %q", text[max(0, len(text)-200):])
	}
	if !strings.Contains(text, "source uploaded but LEFT LOCKED") {
		t.Fatal("an earlier LEFT LOCKED line was lost")
	}
	if !utf8.ValidString(text) {
		t.Fatal("the message is not valid UTF-8: a rune was split")
	}
}

// A handler that finished its work and answered normally did not time out,
// even if the budget ran out as it returned.
func TestLongCallDoesNotRewriteCompletedWork(t *testing.T) {
	done := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-ctx.Done()
		return mcp.NewToolResultText("Deployment complete: 3 ok, 0 failed"), nil
	}
	s := &Server{config: &Config{}}
	res, _ := s.longCall(context.Background(), newRequest(map[string]any{"timeout": 0.02}), "deploy_zip", done)
	if res.IsError || resultText(res) != "Deployment complete: 3 ok, 0 failed" {
		t.Fatalf("completed work was rewritten: %q", resultText(res))
	}

	refused := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-ctx.Done()
		return newToolResultError("syntax error in line 3"), nil
	}
	res, _ = s.longCall(context.Background(), newRequest(map[string]any{"timeout": 0.02}), "x", refused)
	if resultText(res) != "syntax error in line 3" {
		t.Fatalf("a failure unrelated to the deadline was rewritten: %q", resultText(res))
	}
}

// When the caller's own deadline is earlier than the call's budget, that is
// the one that ended it, and the message says so rather than quoting the
// budget.
func TestLongCallReportsTheEffectiveDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	s := &Server{config: &Config{}}
	res, _ := s.longCall(parent, newRequest(map[string]any{"timeout": float64(30)}), "execute_abap", waitForCtx)
	text := resultText(res)
	if !strings.Contains(text, "execute_abap timed out after 50ms, at the caller's own deadline, before its budget of 30s") {
		t.Fatalf("got %q", text)
	}

	parent2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	res, _ = s.longCall(parent2, newRequest(map[string]any{}), "execute_abap", waitForCtx)
	if text := resultText(res); !strings.Contains(text, "timed out after 50ms, at the caller's own deadline;") {
		t.Fatalf("no budget, caller's deadline: got %q", text)
	}
}

// Without a budget of its own, a call is not reported as timed out for a bare
// "context deadline exceeded" in its text: only the Client.Timeout case is
// the per-request limit.
func TestLongCallBareDeadlineTextNeedsABudget(t *testing.T) {
	h := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return newToolResultError("RFC call failed: context deadline exceeded"), nil
	}
	s := &Server{config: &Config{}}
	res, _ := s.longCall(context.Background(), newRequest(map[string]any{}), "x", h)
	if resultText(res) != "RFC call failed: context deadline exceeded" {
		t.Fatalf("no budget: got %q", resultText(res))
	}
	res, _ = s.longCall(context.Background(), newRequest(map[string]any{"timeout": float64(30)}), "x", h)
	if text := resultText(res); !strings.Contains(text, "x stopped after") || !strings.Contains(text, "Detail: RFC call failed") {
		t.Fatalf("with a budget: got %q", text)
	}
}

// deploy_zip goes through longCall: a bad timeout is refused by the budget
// parser before anything else happens.
func TestDeployZipIsALongCall(t *testing.T) {
	s := NewServer(&Config{BaseURL: "http://127.0.0.1:1", Username: "u", Password: "p", Client: "001", Language: "EN", Mode: "expert"})
	res, err := s.handleDeployZip(context.Background(), newRequest(map[string]any{
		"source": "abapgit-standalone", "package": "$ZDEMO", "dry_run": true, "timeout": "soon",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(resultText(res), "timeout must be") {
		t.Fatalf("handleDeployZip ignored params.timeout: %.300q", resultText(res))
	}
}

// deploy_zip releases a lock it took even when the call's context ends inside
// the lock window: the UNLOCK goes out on a context of its own.
func TestDeployZipUnlocksAfterTheCallContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	locks, unlocks := 0, 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "t")
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			mu.Lock()
			locks++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>
<LOCK_HANDLE>HANDLE-1</LOCK_HANDLE><MODIFICATION_SUPPORT>Modification</MODIFICATION_SUPPORT>
</DATA></asx:values></asx:abap>`))
			return
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "UNLOCK":
			mu.Lock()
			unlocks++
			mu.Unlock()
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/source/main"):
			// The call ends while the upload is on the wire.
			cancel()
			select {
			case <-r.Context().Done():
			case <-time.After(200 * time.Millisecond):
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(fakeEmptyXML))
	}))
	t.Cleanup(ts.Close)
	s := NewServer(&Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN", Mode: "expert"})

	res, err := s.handleDeployZip(ctx, newRequest(map[string]any{"source": "abapgit-standalone", "package": "$ZDEMO"}))
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if locks == 0 {
		t.Fatalf("no LOCK reached the server; the test did not reach the lock window: %.500q", resultText(res))
	}
	if unlocks != locks {
		t.Fatalf("LOCK %d, UNLOCK %d: a lock taken before the call ended was not released", locks, unlocks)
	}
}

// A budget beyond the cap is capped in seconds, before it becomes a
// Duration: converted first, a huge value overflows to a non-positive
// Duration and the call would run with no deadline at all.
func TestCallBudgetCapsBeforeConverting(t *testing.T) {
	for _, v := range []any{3600.0, 1e10, 1e300, math.MaxFloat64, "1e300", 9.3e9} {
		d, err := callBudget(map[string]any{"timeout": v}, 0)
		if err != nil || d != MaxCallTimeout {
			t.Errorf("timeout %v: got %v, %v; want the %v cap", v, d, err, MaxCallTimeout)
		}
	}
	if d, err := callBudget(map[string]any{"timeout": 1.5}, 0); err != nil || d != 1500*time.Millisecond {
		t.Errorf("timeout 1.5: got %v, %v", d, err)
	}
	for _, v := range []any{math.NaN(), math.Inf(1), math.Inf(-1), -1.0, 0.0, -1e300, "Inf", "-Inf", "NaN", "+Inf", 1e-12} {
		if d, err := callBudget(map[string]any{"timeout": v}, 0); err == nil {
			t.Errorf("timeout %v: want an error, got budget %v", v, d)
		}
	}
}
