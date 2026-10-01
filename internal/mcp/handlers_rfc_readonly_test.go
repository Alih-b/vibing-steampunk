package mcp

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeGateway accepts TCP connections on loopback, counts them and hangs up at
// once. It is not an RFC server: it only shows whether an op got as far as
// dialling the gateway.
//
// dials is a barrier, not a wait: it dials a probe of its own and returns, once
// the listener has accepted the probe, how many other connections came before
// it. The kernel hands out connections in the order they were established, so
// a dial made before dials was called has been counted by the time it returns;
// there is no window to guess.
func fakeGateway(t *testing.T) (port int, dials func() int64) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done); _ = ln.Close() })
	accepted := make(chan string)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			from := conn.RemoteAddr().String()
			_ = conn.Close()
			select {
			case accepted <- from:
			case <-done:
				return
			}
		}
	}()
	var n int64
	return ln.Addr().(*net.TCPAddr).Port, func() int64 {
		t.Helper()
		probe, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", ln.Addr().String())
		if err != nil {
			t.Fatalf("probe dial: %v", err)
		}
		defer probe.Close()
		self := probe.LocalAddr().String()
		timeout := time.After(5 * time.Second)
		for {
			select {
			case from := <-accepted:
				if from == self {
					return n
				}
				n++
			case <-timeout:
				t.Fatal("the gateway never accepted the probe")
			}
		}
	}
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

// rfcParams points this server's own gateway at the fake one, through a
// .vsp.json entry for its URL and client in the test's working directory, and
// returns the call's params. A per-call host/sysnr/port override would be
// refused: the configured credentials only go to the server's own gateway.
func rfcParams(t *testing.T, port int, extra map[string]any) map[string]any {
	t.Helper()
	cfg := fmt.Sprintf(`{"systems": {"own": {"url": "http://127.0.0.1:1", "client": "001",
	  "rfc_host": "127.0.0.1", "rfc_sysnr": "00", "rfc_port": %d}}}`, port)
	if err := os.WriteFile(".vsp.json", []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	p := map[string]any{}
	for k, v := range extra {
		p[k] = v
	}
	return p
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
			_, handled, err := s.routeRFCAction(ctx, "rfc", "Z_DOUBLE", "", rfcParams(t, port, extra))
			if !handled {
				t.Fatal("rfc action not handled")
			}
			if err == nil || !strings.Contains(err.Error(), "blocked by safety configuration") {
				t.Fatalf("want a safety refusal, got %v", err)
			}
			if n := dials(); n != 0 {
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
			_, handled, err := s.routeRFCAction(ctx, "rfc", tc.target, "", rfcParams(t, port, tc.extra))
			if !handled {
				t.Fatal("rfc action not handled")
			}
			if err != nil && strings.Contains(err.Error(), "blocked") {
				t.Fatalf("read op refused under --read-only: %v", err)
			}
			if dials() == 0 {
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
	_, _, err := s.routeRFCAction(ctx, "rfc", "Z_DOUBLE", "", rfcParams(t, port, map[string]any{"op": "call"}))
	if err != nil && strings.Contains(err.Error(), "blocked") {
		t.Fatalf("call refused without --read-only: %v", err)
	}
	if dials() == 0 {
		t.Errorf("call never reached the gateway (err %v)", err)
	}
}
