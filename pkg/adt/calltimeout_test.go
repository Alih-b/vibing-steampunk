package adt

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"
)

// A call given its own deadline must not be cut off by the client's shorter
// per-request Timeout; a call without one must still be.
func TestCallDeadlineLiftsClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("done"))
	}))
	defer srv.Close()

	cfg := NewConfig(srv.URL, "u", "p")
	tr := NewTransportWithClient(cfg, &http.Client{Timeout: 100 * time.Millisecond})

	if _, err := tr.Request(context.Background(), "/sap/bc/adt/slow", &RequestOptions{Method: http.MethodGet}); err == nil {
		t.Fatal("unmarked call: want the client Timeout to cut the request off")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := tr.Request(WithCallDeadline(ctx), "/sap/bc/adt/slow", &RequestOptions{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("call with its own deadline: %v", err)
	}
	if string(resp.Body) != "done" {
		t.Fatalf("body = %q", resp.Body)
	}

	short, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	if _, err := tr.Request(WithCallDeadline(short), "/sap/bc/adt/slow", &RequestOptions{Method: http.MethodGet}); err == nil {
		t.Fatal("call deadline shorter than the request: want it to end the request")
	}
}

func TestWithCallDeadlineNeedsADeadline(t *testing.T) {
	if callDeadlineGoverns(WithCallDeadline(context.Background())) {
		t.Fatal("a context without a deadline must stay bounded by the client Timeout")
	}
}

// The mark is a context value, so it survives context.WithoutCancel, which
// drops the deadline. Such a context must not lift the per-request Timeout:
// nothing would bound the request then.
func TestCallDeadlineNeedsTheDeadlineStillPresent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	marked := WithCallDeadline(ctx)
	if !callDeadlineGoverns(marked) {
		t.Fatal("a marked context with a deadline must govern")
	}
	if callDeadlineGoverns(context.WithoutCancel(marked)) {
		t.Fatal("a detached context keeps the mark but has no deadline: the client Timeout must apply")
	}
	rebounded, cancel2 := context.WithTimeout(context.WithoutCancel(marked), time.Minute)
	defer cancel2()
	if !callDeadlineGoverns(rebounded) {
		t.Fatal("a detached context given a deadline of its own governs again")
	}
}

// A stateless request that cannot share the stateful context is sent by an
// isolated copy of the client (no jar). That copy must lift the Timeout under
// a call deadline just as send does.
func TestCallDeadlineLiftsTimeoutOnIsolatedStatelessRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("done"))
	}))
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(srv.URL, "u", "p")
	tr := NewTransportWithClient(cfg, &http.Client{Timeout: 100 * time.Millisecond, Jar: jar})
	// A stateful request in flight sends every stateless one down the
	// isolated path.
	tr.contextInFlight.Add(1)
	defer tr.contextInFlight.Add(-1)

	if _, err = tr.Request(context.Background(), "/sap/bc/adt/slow", &RequestOptions{Method: http.MethodGet}); err == nil {
		t.Fatal("unmarked isolated request: want the client Timeout to cut it off")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := tr.Request(WithCallDeadline(ctx), "/sap/bc/adt/slow", &RequestOptions{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("isolated request under a call deadline: %v", err)
	}
	if string(resp.Body) != "done" {
		t.Fatalf("body = %q", resp.Body)
	}
}
