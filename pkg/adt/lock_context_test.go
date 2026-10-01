package adt

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// contextServer plays the ICM's part in a lock chain: a stateful LOCK opens a
// context (or joins the live one) and hands out its sap-contextid; a stateless
// request that arrives in that context ends it; a write is accepted only in the
// live context; and a second request arriving in a context that is still busy
// with one is refused with 400. A stateless request with no context gets a
// context id of its own, as SAP sends one on every response.
type contextServer struct {
	mu   sync.Mutex
	live string // the context the lock handles are bound to
	busy bool   // the live context is serving a request
	next int
}

func (s *contextServer) handler(w http.ResponseWriter, r *http.Request) {
	presented := ""
	if c, err := r.Cookie("sap-contextid"); err == nil {
		presented = c.Value
	}
	stateless := r.Header.Get("X-sap-adt-sessiontype") == "stateless"

	s.mu.Lock()
	inContext := presented != "" && presented == s.live
	if inContext && !stateless {
		if s.busy {
			s.mu.Unlock()
			http.Error(w, "session in use", http.StatusBadRequest)
			return
		}
		s.busy = true
		s.mu.Unlock()
		time.Sleep(2 * time.Millisecond) // long enough for a concurrent request to collide
		s.mu.Lock()
		s.busy = false
	}
	defer s.mu.Unlock()
	w.Header().Set("x-csrf-token", "TOKEN")

	switch {
	case r.URL.Query().Get("_action") == "LOCK":
		if !inContext {
			s.next++
			s.live = fmt.Sprintf("CTX-%d", s.next)
		}
		http.SetCookie(w, &http.Cookie{Name: "sap-contextid", Value: s.live, Path: "/"})
		s.next++
		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><asx:abap xmlns:asx="http://www.sap.com/abapxml">
		  <asx:values><DATA><LOCK_HANDLE>HANDLE-%d</LOCK_HANDLE>
		  <MODIFICATION_SUPPORT>Modification</MODIFICATION_SUPPORT></DATA></asx:values></asx:abap>`, s.next)
	case r.Method == http.MethodPut:
		if s.live == "" || presented != s.live {
			w.WriteHeader(http.StatusLocked)
			_, _ = w.Write([]byte(`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">` +
				`<type id="ExceptionResourceInvalidLockHandle"/><message lang="EN">invalid lock handle</message></exc:exception>`))
			return
		}
		w.WriteHeader(http.StatusOK)
	case stateless:
		if presented != "" && presented == s.live {
			s.live = "" // a stateless request ends the context it arrives in
		}
		s.next++
		http.SetCookie(w, &http.Cookie{Name: "sap-contextid", Value: fmt.Sprintf("CTX-%d", s.next), Path: "/"})
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

// Another caller's stateless read between LOCK and the write must neither end
// the context the handle is bound to nor replace its id in the jar.
func TestLockContext_AStatelessRequestInsideTheLockWindowLeavesTheContextAlone(t *testing.T) {
	s := &contextServer{}
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "TESTUSER", "pw")
	ctx := context.Background()

	objectURL := "/sap/bc/adt/programs/programs/ZDEMO"
	lock, err := c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	if _, err := c.transport.Request(ctx, "/sap/bc/adt/repository/informationsystem/search", nil); err != nil {
		t.Fatalf("stateless read: %v", err)
	}
	if err := c.UpdateSource(ctx, objectURL+"/source/main", "REPORT zdemo.", lock.LockHandle, ""); err != nil {
		t.Fatalf("the write after a stateless read inside the lock window: %v", err)
	}
}

// Outside a lock window a stateless request keeps the jar, so it still ends
// the context a finished chain left behind instead of letting it linger.
func TestLockContext_OutsideTheLockWindowAStatelessRequestStillEndsTheContext(t *testing.T) {
	s := &contextServer{}
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "TESTUSER", "pw")
	ctx := context.Background()

	objectURL := "/sap/bc/adt/programs/programs/ZDEMO"
	lock, err := c.LockObject(ctx, objectURL, "MODIFY")
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	if err := c.UnlockObject(ctx, objectURL, lock.LockHandle); err != nil {
		t.Fatalf("UnlockObject: %v", err)
	}
	if _, err := c.transport.Request(ctx, "/sap/bc/adt/repository/informationsystem/search", nil); err != nil {
		t.Fatalf("stateless read: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live != "" {
		t.Errorf("context %s outlived the chain: the stateless request no longer reached it", s.live)
	}
}

// Two callers running lock chains through one client while a third reads: the
// chains share the context, and every write has to land.
func TestLockContext_ConcurrentChainsAndReadersAllLand(t *testing.T) {
	s := &contextServer{}
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "TESTUSER", "pw")
	ctx := context.Background()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = c.transport.Request(ctx, "/sap/bc/adt/repository/informationsystem/search", nil)
		}
	}()

	var chains sync.WaitGroup
	// Room for every error the producers can send -- 20 rounds, two chains,
	// update and unlock each -- so a regression fails the test, not hangs it.
	errs := make(chan error, 80)
	for _, name := range []string{"ZDEMO_A", "ZDEMO_B"} {
		chains.Add(1)
		go func(objectURL string) {
			defer chains.Done()
			for i := 0; i < 20; i++ {
				lock, err := c.LockObject(ctx, objectURL, "MODIFY")
				if err != nil {
					errs <- err
					continue
				}
				if err := c.UpdateSource(ctx, objectURL+"/source/main", "REPORT x.", lock.LockHandle, ""); err != nil {
					errs <- err
				}
				if err := c.UnlockObject(ctx, objectURL, lock.LockHandle); err != nil {
					errs <- err
				}
			}
		}("/sap/bc/adt/programs/programs/" + name)
	}
	chains.Wait()
	close(stop)
	readers.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// A lock older than the keep-alive's thirty minutes still isolates stateless
// requests: its context may well be alive.
func TestLockWindow_PresenceOutlivesTheKeepAliveLimit(t *testing.T) {
	w := &lockWindow{open: map[string]time.Time{"H": time.Now().Add(-45 * time.Minute)}}
	if w.outstanding() {
		t.Error("keep-alive still suppressed after 45 minutes")
	}
	if !w.present() {
		t.Error("the transport forgot a lock after 45 minutes")
	}
	w.open["OLD"] = time.Now().Add(-13 * time.Hour)
	w.present()
	if _, ok := w.open["OLD"]; ok {
		t.Error("a record past lockRecordMaxAge was kept")
	}
}

// A context id the configuration put on the request goes; the empty one the
// proxy guard sends, and every other cookie, stay.
func TestStripContextID(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://sap.example.com/x", nil)
	req.AddCookie(&http.Cookie{Name: "SAP_SESSIONID_X", Value: "s"})
	req.AddCookie(&http.Cookie{Name: "sap-contextid", Value: "SID:ANON:abc"})
	stripContextID(req)
	if c, err := req.Cookie("sap-contextid"); err == nil {
		t.Errorf("sap-contextid kept: %v", c)
	}
	if _, err := req.Cookie("SAP_SESSIONID_X"); err != nil {
		t.Error("the session cookie was dropped")
	}

	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, "https://sap.example.com/x", nil)
	req.AddCookie(&http.Cookie{Name: "sap-contextid", Value: ""})
	stripContextID(req)
	if _, err := req.Cookie("sap-contextid"); err != nil {
		t.Error("the guard's empty sap-contextid was removed")
	}
}

// A stateful request the server never answers must hold up neither a stateless
// read, which goes isolated straight away, nor, past its own context, another
// request waiting for the stateful context.
func TestLockContext_AStuckStatefulRequestHoldsUpNobody(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-csrf-token", "TOKEN")
		if r.URL.Path == "/stuck" {
			entered <- struct{}{}
			<-release
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs first: lets the stuck handler go
	c := NewClient(srv.URL, "TESTUSER", "pw")

	go func() {
		_, _ = c.transport.Request(context.Background(), "/stuck", &RequestOptions{Stateful: true})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the stateful request never reached the server")
	}

	finish := func(name string, run func() error) error {
		done := make(chan error, 1)
		go func() { done <- run() }()
		select {
		case err := <-done:
			return err
		case <-time.After(2 * time.Second):
			t.Fatalf("%s is still waiting behind the stuck stateful request", name)
			return nil
		}
	}

	if err := finish("a stateless read", func() error {
		_, err := c.transport.Request(context.Background(), "/sap/bc/adt/repository/informationsystem/search", nil)
		return err
	}); err != nil {
		t.Errorf("stateless read: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := finish("a stateful request whose context ended", func() error {
		_, err := c.transport.Request(ctx, "/sap/bc/adt/programs/programs/ZDEMO", &RequestOptions{Stateful: true})
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("stateful request with an expired context: got %v, want %v", err, context.DeadlineExceeded)
	}
}

// waitQueued returns once a writer is queued for (or holds) g: from then on
// tryShared fails.
func waitQueued(t *testing.T, g contextGate) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for g.tryShared() {
		g.releaseShared()
		if time.Now().After(deadline) {
			t.Fatal("the writer never queued for the gate")
		}
		time.Sleep(time.Millisecond)
	}
}

// lockAsync starts g.lock(ctx) and returns its result channel.
func lockAsync(g contextGate, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- g.lock(ctx) }()
	return done
}

// A writer that gives up while queued must pass the turn on: the writer
// behind it still gets the gate once it is free.
func TestContextGate_ACancelledWriterDoesNotStrandTheNext(t *testing.T) {
	g := newContextGate()
	if !g.tryShared() {
		t.Fatal("a fresh gate refused a reader")
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	first := lockAsync(g, ctx1)
	waitQueued(t, g)
	second := lockAsync(g, context.Background())
	time.Sleep(10 * time.Millisecond) // let the second writer queue behind the first

	cancel1()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled writer: got %v, want %v", err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled writer is still waiting")
	}

	g.releaseShared()
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("second writer: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second writer never got the gate after the first gave up")
	}
	g.unlock()
}

// Writers get the gate in the order they asked for it.
func TestContextGate_WritersAreServedInArrivalOrder(t *testing.T) {
	g := newContextGate()
	if err := g.lock(context.Background()); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var order []int
	var wg sync.WaitGroup
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := g.lock(context.Background()); err != nil {
				t.Errorf("writer %d: %v", i, err)
				return
			}
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			g.unlock()
		}(i)
		// Each writer has to be in the queue before the next one starts.
		time.Sleep(20 * time.Millisecond)
	}
	g.unlock()
	wg.Wait()

	if fmt.Sprint(order) != "[1 2 3]" {
		t.Errorf("writers acquired in order %v, want [1 2 3]", order)
	}
}

// A stateless request that arrives while a request into the context waits
// for the gate does not wait behind it: it goes isolated, without the
// context's sap-contextid.
func TestLockContext_AStatelessRequestWhileAWriterWaitsGoesIsolated(t *testing.T) {
	var mu sync.Mutex
	var presented []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-csrf-token", "TOKEN")
		if r.Header.Get("X-sap-adt-sessiontype") == "stateful" {
			http.SetCookie(w, &http.Cookie{Name: "sap-contextid", Value: "CTX-1", Path: "/"})
		}
		if r.Header.Get("X-sap-adt-sessiontype") == "stateless" {
			v := ""
			if c, err := r.Cookie("sap-contextid"); err == nil {
				v = c.Value
			}
			mu.Lock()
			presented = append(presented, v)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "TESTUSER", "pw")
	tr := c.transport

	// Open the context: the jar learns sap-contextid.
	if _, err := tr.Request(context.Background(), "/open", &RequestOptions{Stateful: true}); err != nil {
		t.Fatalf("stateful request: %v", err)
	}

	// An earlier stateless request holds the gate shared, and a request into
	// the context -- unmarked, so the in-flight count stays at zero -- queues.
	if !tr.contextGate.tryShared() {
		t.Fatal("the gate is not free")
	}
	writer := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/probe", nil)
		resp, err := tr.do(req)
		if err == nil {
			resp.Body.Close()
		}
		writer <- err
	}()
	waitQueued(t, tr.contextGate)
	if n := tr.contextInFlight.Load(); n != 0 {
		t.Fatalf("in-flight count %d: the test would not exercise the gate", n)
	}

	done := make(chan error, 1)
	go func() {
		_, err := tr.Request(context.Background(), "/sap/bc/adt/repository/informationsystem/search", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stateless request: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stateless request waited behind the queued writer")
	}

	tr.contextGate.releaseShared()
	if err := <-writer; err != nil {
		t.Fatalf("queued writer: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(presented) == 0 {
		t.Fatal("the stateless request never reached the server")
	}
	if v := presented[len(presented)-1]; v != "" {
		t.Errorf("the stateless request carried sap-contextid=%q; it should have gone isolated", v)
	}
}

// Readers and writers with short deadlines hammer the gate: a writer is never
// in with anyone else, and a reader never in with a writer.
func TestContextGate_StressNoOverlap(t *testing.T) {
	g := newContextGate()
	var readers, writers atomic.Int32
	var overlaps, wrote, read atomic.Int64
	stop := time.Now().Add(time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for time.Now().Before(stop) {
				// Pause between turns: writers are preferred, and with no gaps
				// a queue of them would keep every reader out.
				time.Sleep(time.Duration(rng.Intn(300)) * time.Microsecond)
				if rng.Intn(4) == 0 {
					ctx, cancel := context.WithTimeout(context.Background(), time.Duration(rng.Intn(500))*time.Microsecond)
					err := g.lock(ctx)
					if err != nil {
						cancel()
						if !errors.Is(err, context.DeadlineExceeded) {
							t.Errorf("writer: unexpected error %v", err)
						}
						continue
					}
					if writers.Add(1) != 1 || readers.Load() != 0 {
						overlaps.Add(1)
					}
					time.Sleep(time.Duration(rng.Intn(100)) * time.Microsecond)
					writers.Add(-1)
					g.unlock()
					cancel()
					wrote.Add(1)
					continue
				}
				if !g.tryShared() {
					continue
				}
				readers.Add(1)
				if writers.Load() != 0 {
					overlaps.Add(1)
				}
				time.Sleep(time.Duration(rng.Intn(100)) * time.Microsecond)
				readers.Add(-1)
				g.releaseShared()
				read.Add(1)
			}
		}(int64(i))
	}
	wg.Wait()

	if n := overlaps.Load(); n != 0 {
		t.Errorf("%d overlaps between a writer and anyone else", n)
	}
	if wrote.Load() == 0 || read.Load() == 0 {
		t.Errorf("the mix never exercised both sides: %d writes, %d reads", wrote.Load(), read.Load())
	}
	// Nothing leaked: the gate is free again.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := g.lock(ctx); err != nil {
		t.Fatalf("the gate was left held: %v", err)
	}
	g.unlock()
	t.Logf("%d writes, %d reads", wrote.Load(), read.Load())
}
