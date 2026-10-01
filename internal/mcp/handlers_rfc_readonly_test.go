package mcp

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGateway accepts TCP connections on loopback, counts them and hangs up at
// once. It is not an RFC server: it only shows whether an op got as far as
// dialling the gateway.
func fakeGateway(t *testing.T) (port int, dials func() int64) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var n atomic.Int64
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			_ = conn.Close()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, n.Load
}

func rfcTestServer(t *testing.T, readOnly bool) *Server {
	t.Helper()
	// Keep a developer's own .vsp.json out of the destination.
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	return NewServer(&Config{
		BaseURL:  "http://127.0.0.1:1",
		Username: "u",
		Password: "p",
		Client:   "001",
		Language: "EN",
		ReadOnly: readOnly,
	})
}

func rfcParams(port int, extra map[string]any) map[string]any {
	p := map[string]any{"host": "127.0.0.1", "sysnr": "00", "port": float64(port)}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// waitDials gives a dial that already happened time to reach Accept.
func waitDials(dials func() int64, want int64, within time.Duration) int64 {
	deadline := time.Now().Add(within)
	for dials() < want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	return dials()
}

func TestRFCReadOnly_CallRefusedBeforeLogon(t *testing.T) {
	s := rfcTestServer(t, true)
	cases := map[string]map[string]any{
		"explicit op":          {"op": "call", "args": map[string]any{"N": 21.0}},
		"op implied by args":   {"args": map[string]any{"N": 21.0}},
		"explicit op, no args": {"op": "CALL"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			port, dials := fakeGateway(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, handled, err := s.routeRFCAction(ctx, "rfc", "Z_DOUBLE", "", rfcParams(port, extra))
			if !handled {
				t.Fatal("rfc action not handled")
			}
			if err == nil || !strings.Contains(err.Error(), "blocked by safety configuration") {
				t.Fatalf("want a safety refusal, got %v", err)
			}
			if n := waitDials(dials, 1, 200*time.Millisecond); n != 0 {
				t.Errorf("refused call still dialled the gateway %d time(s)", n)
			}
		})
	}
}

func TestRFCReadOnly_ReadOpsStillReachGateway(t *testing.T) {
	s := rfcTestServer(t, true)
	cases := map[string]struct {
		target string
		extra  map[string]any
	}{
		"info":       {"", map[string]any{"op": "info"}},
		"ping":       {"", map[string]any{"op": "ping"}},
		"probe":      {"", map[string]any{"op": "probe"}},
		"describe":   {"STFC_CONNECTION", map[string]any{"op": "describe"}},
		"default":    {"STFC_CONNECTION", nil},
		"search":     {"BAPI_USER_*", map[string]any{"op": "search"}},
		"read_table": {"T000", map[string]any{"op": "read_table", "top": 1.0}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			port, dials := fakeGateway(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, handled, err := s.routeRFCAction(ctx, "rfc", tc.target, "", rfcParams(port, tc.extra))
			if !handled {
				t.Fatal("rfc action not handled")
			}
			if err != nil && strings.Contains(err.Error(), "blocked") {
				t.Fatalf("read op refused under --read-only: %v", err)
			}
			if waitDials(dials, 1, 2*time.Second) == 0 {
				t.Errorf("read op never reached the gateway (err %v)", err)
			}
		})
	}
}

func TestRFCWritable_CallReachesGateway(t *testing.T) {
	s := rfcTestServer(t, false)
	port, dials := fakeGateway(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := s.routeRFCAction(ctx, "rfc", "Z_DOUBLE", "", rfcParams(port, map[string]any{"op": "call"}))
	if err != nil && strings.Contains(err.Error(), "blocked") {
		t.Fatalf("call refused without --read-only: %v", err)
	}
	if waitDials(dials, 1, 2*time.Second) == 0 {
		t.Errorf("call never reached the gateway (err %v)", err)
	}
}
