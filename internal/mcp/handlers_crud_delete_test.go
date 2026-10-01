package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// --- DeleteObject's self-lock window (issue #238) ---
//
// With --allowed-packages set, DeleteObject's own gate resolves the package
// through a stateless search. The handler used to take its lock first, so that
// search landed inside the lock window, retired the session the handle belongs
// to, and the DELETE came back 423 ExceptionResourceInvalidLockHandle. The
// handler now gates before it locks, as UpdateSource's deploy path does.

type deleteCall struct {
	method, path, action, sessionType string
}

func (c deleteCall) String() string {
	return fmt.Sprintf("%-6s %s action=%q sessiontype=%q", c.method, c.path, c.action, c.sessionType)
}

func newDeleteTestServer(t *testing.T, pkg string) (*Server, func() []deleteCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []deleteCall

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, deleteCall{
			method:      r.Method,
			path:        r.URL.Path,
			action:      r.URL.Query().Get("_action"),
			sessionType: r.Header.Get("X-sap-adt-sessiontype"),
		})
		mu.Unlock()

		w.Header().Set("X-CSRF-Token", "TOKEN")
		switch {
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:objectReference adtcore:uri="/sap/bc/adt/programs/programs/zdemo_del" adtcore:type="PROG/P" adtcore:name="ZDEMO_DEL" adtcore:packageName="`+pkg+`"/>
</adtcore:objectReferences>`)
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>
<LOCK_HANDLE>HANDLE-1</LOCK_HANDLE><IS_LOCAL>X</IS_LOCAL>
</DATA></asx:values></asx:abap>`)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(ts.Close)

	server := NewServer(&Config{
		BaseURL:            ts.URL,
		Username:           "u",
		Password:           "p",
		Client:             "001",
		Language:           "EN",
		InsecureSkipVerify: true,
		AllowedPackages:    []string{"$TMP"},
	})
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	return server, func() []deleteCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]deleteCall(nil), calls...)
	}
}

func dumpDeleteCalls(t *testing.T, calls []deleteCall) {
	t.Helper()
	for i, c := range calls {
		t.Logf("  [%d] %s", i, c)
	}
}

func TestHandleDeleteObject_SelfLockChecksPackageBeforeLock(t *testing.T) {
	server, trace := newDeleteTestServer(t, "$TMP")

	res, err := server.handleDeleteObject(context.Background(), newRequest(map[string]any{
		"object_url": "/sap/bc/adt/programs/programs/ZDEMO_DEL",
	}))
	if err != nil {
		t.Fatalf("handleDeleteObject: %v", err)
	}

	calls := trace()
	if res.IsError {
		dumpDeleteCalls(t, calls)
		t.Fatalf("delete failed: %+v", res.Content)
	}

	lockAt, delAt, searchAt := -1, -1, -1
	for i, c := range calls {
		switch {
		case c.method == http.MethodPost && c.action == "LOCK":
			lockAt = i
		case c.method == http.MethodDelete:
			delAt = i
		case strings.Contains(c.path, "informationsystem/search"):
			searchAt = i
		}
	}
	if lockAt < 0 || delAt < lockAt {
		dumpDeleteCalls(t, calls)
		t.Fatal("expected a LOCK followed by a DELETE")
	}
	if searchAt < 0 || searchAt > lockAt {
		t.Errorf("package lookup at %d, LOCK at %d; want the lookup above the lock", searchAt, lockAt)
	}
	for _, c := range calls[lockAt+1 : delAt] {
		if c.sessionType != "stateful" {
			t.Errorf("request inside the lock window is not stateful: %s — it retires the "+
				"session the lock handle lives in, and the DELETE returns 423 (issue #238)", c)
		}
	}
	if t.Failed() {
		dumpDeleteCalls(t, calls)
	}
}

func TestHandleDeleteObject_ForeignPackageRefusedBeforeLock(t *testing.T) {
	server, trace := newDeleteTestServer(t, "ZSOMEONE_ELSE")

	res, err := server.handleDeleteObject(context.Background(), newRequest(map[string]any{
		"object_url": "/sap/bc/adt/programs/programs/ZDEMO_DEL",
	}))
	if err != nil {
		t.Fatalf("handleDeleteObject: %v", err)
	}
	if !res.IsError {
		t.Fatal("a delete outside the allowlist succeeded")
	}
	for i, c := range trace() {
		if c.action == "LOCK" || c.method == http.MethodDelete {
			t.Errorf("an object outside the allowlist was touched: [%d] %s", i, c)
		}
	}
}
