package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// lockWindowHandler answers LOCK, UNLOCK, DELETE and the CSRF/ping probe, and
// records every path it is asked for.
func lockWindowHandler(seen *[]string, mu *sync.Mutex) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*seen = append(*seen, r.URL.Path+"?"+r.URL.Query().Get("_action"))
		mu.Unlock()
		w.Header().Set("x-csrf-token", "TOKEN")
		if r.URL.Query().Get("_action") == "LOCK" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0"?><asx:abap xmlns:asx="http://www.sap.com/abapxml">
			  <asx:values><DATA><LOCK_HANDLE>HANDLE-1</LOCK_HANDLE>
			  <MODIFICATION_SUPPORT>Modification</MODIFICATION_SUPPORT></DATA></asx:values></asx:abap>`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// lockWindowServer serves lockWindowHandler over a real socket.
func lockWindowServer(t *testing.T, seen *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(lockWindowHandler(seen, mu))
}

func newLockWindowClient(t *testing.T, url string) *Client {
	t.Helper()
	return NewClient(url, "TESTUSER", "pw")
}

// TestLockWindow_SuppressesTheKeepAlivePing is the regression guard for #168:
// a keep-alive ping is an ordinary request, an ordinary request is stamped
// stateless, and a stateless request retires the session the lock handle lives
// in. So no ping may go out while a lock is outstanding.
func TestLockWindow_SuppressesTheKeepAlivePing(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := lockWindowServer(t, &seen, &mu)
	defer srv.Close()

	c := newLockWindowClient(t, srv.URL)
	if c.lockOutstanding() {
		t.Fatal("a fresh client must not report an outstanding lock")
	}

	if _, err := c.LockObject(context.Background(), "/sap/bc/adt/programs/programs/ZDEMO", "MODIFY"); err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	if !c.lockOutstanding() {
		t.Fatal("after a LOCK that returned a handle, the client must be inside a lock window")
	}

	if err := c.UnlockObject(context.Background(), "/sap/bc/adt/programs/programs/ZDEMO", "HANDLE-1"); err != nil {
		t.Fatalf("UnlockObject: %v", err)
	}
	if c.lockOutstanding() {
		t.Fatal("a successful UNLOCK must end the window")
	}
}

// TestLockWindow_DeleteEndsTheWindow pins the defect that sank the first design
// of this feature: DELETE consumes the lock handle and no UNLOCK is ever sent,
// so a window keyed only on unlock stays open forever and the keep-alive is
// disabled for the life of the process.
func TestLockWindow_DeleteEndsTheWindow(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := lockWindowServer(t, &seen, &mu)
	defer srv.Close()

	c := newLockWindowClient(t, srv.URL)
	if _, err := c.LockObject(context.Background(), "/sap/bc/adt/programs/programs/ZDEMO", "MODIFY"); err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	if err := c.DeleteObject(context.Background(), "/sap/bc/adt/programs/programs/ZDEMO", "HANDLE-1", ""); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}

	// Assert on the record itself, not on lockOutstanding(): the predicate can
	// be made to answer "closed" for reasons that have nothing to do with the
	// delete path, and this test exists precisely to pin that path.
	c.locks.mu.Lock()
	remaining := len(c.locks.open)
	c.locks.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("a successful DELETE consumes the handle and must clear it; %d entries left", remaining)
	}
}

// TestLockWindow_StaleEntriesExpire proves a leaked handle cannot disable the
// keep-alive permanently. Nothing can prove a lock was released, so an entry
// that is never closed has to age out rather than suppress the ping forever.
func TestLockWindow_StaleEntriesExpire(t *testing.T) {
	c := &Client{}
	c.noteLockOpened("LEAKED")
	if !c.lockOutstanding() {
		t.Fatal("a just-opened lock must count as outstanding")
	}

	c.locks.mu.Lock()
	c.locks.open["LEAKED"] = time.Now().Add(-lockWindowMaxAge - time.Minute)
	c.locks.mu.Unlock()

	if c.lockOutstanding() {
		t.Fatal("an entry older than lockWindowMaxAge must be pruned, not suppress the ping forever")
	}
}

// TestLockWindow_IsConcurrencySafe runs locks and unlocks from several
// goroutines because Client is shared across MCP handler calls. Run with -race.
func TestLockWindow_IsConcurrencySafe(t *testing.T) {
	c := &Client{}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			h := string(rune('A' + n%26))
			c.noteLockOpened(h)
			_ = c.lockOutstanding()
			c.noteLockClosed(h)
		}(i)
	}
	wg.Wait()
	if c.lockOutstanding() {
		t.Fatal("every lock was closed; the window must be empty")
	}
}

// TestKeepAlive_DoesNotPingDuringALockWindow is the end-to-end guard: it drives
// the real keep-alive goroutine rather than asserting on the flag it reads.
// Without the skip, a tick inside the window sends a request to the discovery
// endpoint, and on a live system that request retires the ADT session the lock
// handle is bound to.
//
// It runs in a synctest bubble against an in-memory server, so the ticker
// runs on fake time: sleeping 150 ms is exactly 15 ticks, every one of which
// the keep-alive has handled once synctest.Wait returns.
func TestKeepAlive_DoesNotPingDuringALockWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const interval, ticks = 10 * time.Millisecond, 15
		var mu sync.Mutex
		var seen []string
		c := newInMemoryClient(lockWindowHandler(&seen, &mu))
		if _, err := c.LockObject(context.Background(), "/sap/bc/adt/programs/programs/ZDEMO", "MODIFY"); err != nil {
			t.Fatalf("LockObject: %v", err)
		}

		mu.Lock()
		afterLock := len(seen)
		mu.Unlock()

		c.StartKeepAlive(interval, false)
		time.Sleep(ticks * interval)
		synctest.Wait() // the tick due now has been handled too
		c.StopKeepAlive()

		mu.Lock()
		extra := seen[afterLock:]
		mu.Unlock()

		if len(extra) != 0 {
			t.Fatalf("keep-alive sent %d request(s) while a lock was outstanding: %v", len(extra), extra)
		}

		// And once the lock is released it must resume, or the feature is just off.
		if err := c.UnlockObject(context.Background(), "/sap/bc/adt/programs/programs/ZDEMO", "HANDLE-1"); err != nil {
			t.Fatalf("UnlockObject: %v", err)
		}
		mu.Lock()
		afterUnlock := len(seen)
		mu.Unlock()

		c.StartKeepAlive(interval, false)
		time.Sleep(ticks * interval)
		synctest.Wait()
		c.StopKeepAlive()

		mu.Lock()
		resumed := len(seen) - afterUnlock
		mu.Unlock()
		// One ping per tick: the same fifteen ticks that were all skipped
		// above all went out once the window closed.
		if resumed != ticks {
			t.Fatalf("with no lock outstanding the keep-alive must ping on every tick: %d pings in %d ticks", resumed, ticks)
		}
	})
}
