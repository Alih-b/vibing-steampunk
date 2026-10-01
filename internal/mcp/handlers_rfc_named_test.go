package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A system named by -s / SAP_SYSTEM whose URL or client is not the server's is
// refused, naming both identities, before anything is dialled. Taking it
// anyway sent the server's logon to the named system's gateway, or the named
// system's RFC credentials to the wrong system.
func TestRFCDestination_ANamedSystemOfAnotherURLIsRefused(t *testing.T) {
	for _, c := range []struct{ url, client string }{
		{"https://elsewhere.example:44300", "100"},
		{"https://prodsys-a.example:44300", "200"},
	} {
		s := serverFor(t, c.url, c.client, "prodsys-a")
		_, err := s.rfcDestination(map[string]any{})
		if err == nil {
			t.Fatalf("server %s client %s: named prodsys-a was used", c.url, c.client)
		}
		for _, want := range []string{`"prodsys-a"`, "https://prodsys-a.example:44300 client 100", c.url + " client " + c.client} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
	}
}

func TestRFCNamedSystemMismatch_RefusedBeforeTheGateway(t *testing.T) {
	port, dials := fakeGateway(t)
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"systems": {"other": {"url": "https://other.example:44300", "client": "100",
	  "rfc_host": "127.0.0.1", "rfc_sysnr": "00", "rfc_port": %d, "rfc_user": "OTHER", "rfc_password": "other-secret"}}}`, port)
	if err := os.WriteFile(filepath.Join(dir, ".vsp.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	s := &Server{config: &Config{BaseURL: "https://sap.example:44300", Client: "100", Username: "U", Password: "p", SystemName: "other"}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, handled, err := s.routeRFCAction(ctx, "rfc", "", "", map[string]any{"op": "ping"})
	if !handled || err == nil || !strings.Contains(err.Error(), "this server is connected to") {
		t.Fatalf("want a mismatch refusal, got handled=%v err=%v", handled, err)
	}
	if n := waitDials(dials, 1, 200*time.Millisecond); n != 0 {
		t.Errorf("a mismatched named system still dialled its gateway %d time(s)", n)
	}
}

// A named entry that only names a gateway has no URL to disagree with.
func TestRFCDestination_AURLlessNamedSystemStillApplies(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"systems": {"gw": {"rfc_host": "gw.example.local", "rfc_sysnr": "00"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".vsp.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	s := &Server{config: &Config{BaseURL: "https://sap.example:44300", Client: "100", Username: "U", Password: "p", SystemName: "gw"}}

	dest, err := s.rfcDestination(map[string]any{})
	if err != nil {
		t.Fatalf("rfcDestination: %v", err)
	}
	if dest.Host != "gw.example.local" {
		t.Errorf("host = %q, want the URL-less named gw.example.local", dest.Host)
	}
}

// A per-call host or sysnr chosen by the caller must not receive the
// credentials configured for this server's own gateway.
func TestRFCDestination_AnOverrideToAnotherGatewayIsRefused(t *testing.T) {
	s := serverFor(t, "https://prodsys-a.example:44300", "100", "")
	t.Setenv("VSP_PRODSYS-A_RFC_PASSWORD", "rfc-secret")

	for name, params := range map[string]map[string]any{
		"host":       {"host": "attacker.example"},
		"sysnr":      {"sysnr": "11"},
		"host+user":  {"host": "attacker.example", "user": "SOMEONE"},
		"host+port":  {"host": "attacker.example", "port": float64(3300)},
		"sysnr only": {"sysnr": "00"},
	} {
		dest, err := s.rfcDestination(params)
		if err == nil {
			t.Errorf("%s: override reached %s with the configured password", name, dest.Host)
			continue
		}
		if !strings.Contains(err.Error(), "is blocked") || strings.Contains(err.Error(), "rfc-secret") {
			t.Errorf("%s: unexpected error %v", name, err)
		}
	}

	// A port other than the gateway's own (3300 + sysnr 10) reaches another
	// instance or service on that host.
	for name, params := range map[string]map[string]any{
		"port":            {"port": float64(3300)},
		"port elsewhere":  {"port": float64(22)},
		"same host, port": {"host": "prod-gw.example.local", "port": float64(3311)},
	} {
		if dest, err := s.rfcDestination(params); err == nil {
			t.Errorf("%s: override reached %s:%d with the configured password", name, dest.Host, dest.Port)
		} else if !strings.Contains(err.Error(), "is blocked") {
			t.Errorf("%s: unexpected error %v", name, err)
		}
	}

	// The server's own gateway, named explicitly or spelt differently, stays
	// allowed.
	for name, params := range map[string]map[string]any{
		"same host": {"host": "PROD-GW.example.local"},
		"sysnr 10":  {"sysnr": "10"},
		"own port":  {"port": float64(3310)},
		"all three": {"host": "prod-gw.example.local", "sysnr": "10", "port": float64(3310)},
	} {
		if _, err := s.rfcDestination(params); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRFCOverride_AnotherHostIsNeverDialled(t *testing.T) {
	s := rfcTestServer(t, false)
	port, dials := fakeGateway(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The server's gateway is 127.0.0.1; "localhost" is a different name, so
	// the caller is choosing a destination of its own.
	_, _, err := s.routeRFCAction(ctx, "rfc", "", "", map[string]any{"op": "ping", "host": "localhost", "port": float64(port)})
	if err == nil || !strings.Contains(err.Error(), "is blocked") {
		t.Fatalf("want an override refusal, got %v", err)
	}
	if n := waitDials(dials, 1, 200*time.Millisecond); n != 0 {
		t.Errorf("an override to another host dialled %d time(s)", n)
	}
}
