package adt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// pushFakeWS is fakeTransportWS with pushes: AwaitPush returns push (after
// delay) or closedErr.
type pushFakeWS struct {
	*fakeTransportWS
	push      *WSResponse
	closedErr bool
	delay     time.Duration
}

func (p *pushFakeWS) PushEnabled() bool { return true }
func (p *pushFakeWS) TakePush(string) (*WSResponse, bool) {
	return nil, false
}
func (p *pushFakeWS) AwaitPush(ctx context.Context, id string) (*WSResponse, error) {
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if p.closedErr {
		return nil, ErrWebSocketClosed
	}
	if p.push == nil || p.push.ID != id {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.push, nil
}

func pushFrame(job, outcome string) *WSResponse {
	data, _ := json.Marshal(map[string]any{"event": "add_result", "request": "XYZK900001", "job_count": job, "outcome": outcome, "tp_rc": "0000", "in_buffer": true})
	return &WSResponse{ID: "push:transport:" + job, Success: true, Data: data}
}

// With pushes, the wait is for the job's own message, then one status call
// confirms it; there is no polling before the push arrives.
func TestWaitTransportAddTakesThePush(t *testing.T) {
	saved := transportSafetyPoll
	transportSafetyPoll = time.Hour
	t.Cleanup(func() { transportSafetyPoll = saved })
	ws := &pushFakeWS{fakeTransportWS: newFakeTransportWS(), push: pushFrame("47110001", "queued"), delay: 20 * time.Millisecond}
	ws.statuses = []map[string]any{{"outcome": "queued", "job_status": "F", "in_buffer": true, "system": "QAS", "job_tied": true}}
	st, err := uploadClient(enabled()).WaitTransportAdd(context.Background(), ws, "XYZK900001", "47110001", nil)
	if err != nil || st.Outcome != TransportQueued || st.Push == nil || st.Push.TPRC != "0000" {
		t.Fatalf("%+v %v", st, err)
	}
	if got := strings.Join(ws.actions(), ","); got != "add_status" {
		t.Errorf("sent %s; want one status call after the push", got)
	}
}

// A push alone does not make "queued": the status call decides.
func TestWaitTransportAddPushIsNotTheVerdict(t *testing.T) {
	ws := &pushFakeWS{fakeTransportWS: newFakeTransportWS(), push: pushFrame("47110001", "queued")}
	ws.statuses = []map[string]any{{"outcome": "job_failed", "job_status": "F", "in_buffer": false, "system": "QAS", "job_tied": true}}
	st, _ := uploadClient(enabled()).WaitTransportAdd(context.Background(), ws, "XYZK900001", "47110001", nil)
	if st.Outcome != TransportJobFailed {
		t.Errorf("%+v", st)
	}
}

// If the connection drops while waiting, the status is asked on a new one.
func TestWaitTransportAddFallsBackOnDrop(t *testing.T) {
	ws := &pushFakeWS{fakeTransportWS: newFakeTransportWS(), closedErr: true}
	second := newFakeTransportWS()
	second.statuses = []map[string]any{{"outcome": "queued", "job_status": "F", "in_buffer": true, "system": "QAS", "job_tied": true}}
	reconnects := 0
	st, err := uploadClient(enabled()).WaitTransportAdd(context.Background(), ws, "XYZK900001", "47110001",
		func(context.Context) (TransportService, error) { reconnects++; return second, nil })
	if err != nil || st.Outcome != TransportQueued || reconnects != 1 || len(second.actions()) == 0 {
		t.Fatalf("%+v %v reconnects=%d", st, err, reconnects)
	}
	// Without a way to reconnect, the outcome is unknown.
	ws = &pushFakeWS{fakeTransportWS: newFakeTransportWS(), closedErr: true}
	st, err = uploadClient(enabled()).WaitTransportAdd(context.Background(), ws, "XYZK900001", "47110001", nil)
	if err == nil || st.Outcome != TransportUnknown {
		t.Errorf("%+v %v", st, err)
	}
}

// The base client routes a "push:" frame to its waiter, keeps one nobody
// waits for, and releases waiters when the connection ends.
func TestWebSocketPushRouting(t *testing.T) {
	up := websocket.Upgrader{}
	var mu sync.Mutex
	var server *websocket.Conn
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		server = c
		mu.Unlock()
		_ = c.WriteJSON(map[string]any{"id": "welcome", "success": true, "data": map[string]any{"session": "S1", "push": true}})
		close(ready)
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	c := NewBaseWebSocketClient(srv.URL, "100", "TESTUSER", "unused", true)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-ready
	if !c.PushEnabled() {
		t.Fatal("the welcome's push flag was not taken")
	}
	mu.Lock()
	_ = server.WriteJSON(map[string]any{"id": "push:transport:1", "success": true, "data": map[string]any{"outcome": "queued"}})
	_ = server.WriteJSON(map[string]any{"id": "push:transport:2", "success": true, "data": map[string]any{"outcome": "unknown"}})
	mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := c.AwaitPush(ctx, "push:transport:1")
	if err != nil || !strings.Contains(string(r.Data), "queued") {
		t.Fatalf("%v %v", r, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if r, ok := c.TakePush("push:transport:2"); ok {
			if !strings.Contains(string(r.Data), "unknown") {
				t.Errorf("kept push: %s", r.Data)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("an unawaited push was not kept")
		}
		time.Sleep(5 * time.Millisecond)
	}

	waiting := make(chan error, 1)
	go func() {
		_, err := c.AwaitPush(context.Background(), "push:transport:3")
		waiting <- err
	}()
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	_ = server.Close()
	mu.Unlock()
	select {
	case err := <-waiting:
		if err != ErrWebSocketClosed {
			t.Errorf("got %v, want ErrWebSocketClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a waiter was not released when the connection ended")
	}
}

// A job number is believed only for its own request: one shown to be
// another's is refused, one that cannot be tied gives no verdict, and a wait
// stops at the refusal (PR #296 review).
func TestTransportAddStatusTiesTheJobToTheRequest(t *testing.T) {
	ws := newFakeTransportWS()
	ws.statusErr = &WSError{Code: "JOB_NOT_FOR_REQUEST", Message: "Job 47110001 does not belong to XYZK900001: it is for XYZK900002"}
	if _, err := uploadClient(enabled()).TransportAddStatus(context.Background(), ws, "XYZK900001", "47110001"); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Errorf("got %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := uploadClient(enabled()).WaitTransportAdd(ctx, ws, "XYZK900001", "47110001", nil)
	if err == nil || !strings.Contains(err.Error(), "does not belong") || st.Outcome != TransportUnknown || ctx.Err() != nil {
		t.Errorf("wait: %v %+v (ctx %v)", err, st, ctx.Err())
	}

	ws = newFakeTransportWS()
	ws.statuses = []map[string]any{{"outcome": "queued", "job_status": "F", "in_buffer": true, "system": "QAS", "job_tied": false}}
	st, _ = uploadClient(enabled()).TransportAddStatus(context.Background(), ws, "XYZK900001", "47110001")
	if st.Outcome != TransportUnknown {
		t.Errorf("an untied job gave a verdict: %+v", st)
	}
}
