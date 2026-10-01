package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// The RFC settings in .vsp.json belong to one system. A server connected to
// another must not dial the default system's gateway with the default
// system's RFC credentials -- which is what taking cfg.Default did, for every
// server started from environment variables.
const testRFCSystemsJSON = `{
  "default": "devsys",
  "systems": {
    "devsys":    {"url": "https://dev.example.local:44300", "client": "100",
                  "rfc_host": "dev-gw.example.local", "rfc_sysnr": "00", "rfc_user": "DEVRFC", "rfc_password": "dev-secret"},
    "prodsys-a": {"url": "https://prodsys-a.example:44300", "client": "100",
                  "rfc_host": "prod-gw.example.local", "rfc_sysnr": "10"},
    "prodsys-b": {"url": "https://prodsys-a.example:44300", "client": "200",
                  "rfc_host": "prod-gw-b.example.local", "rfc_sysnr": "10"}
  }
}`

func serverFor(t *testing.T, url, client, name string) *Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".vsp.json"), []byte(testRFCSystemsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("SAP_USER", "TESTUSER")
	t.Setenv("SAP_PASSWORD", "test-secret")
	return &Server{config: &Config{BaseURL: url, Client: client, Username: "TESTUSER", Password: "test-secret", SystemName: name}}
}

func TestRFCDestination_UsesTheConnectedSystemNotTheDefault(t *testing.T) {
	s := serverFor(t, "https://prodsys-a.example:44300", "100", "")

	dest, err := s.rfcDestination(map[string]any{})
	if err != nil {
		t.Fatalf("rfcDestination: %v", err)
	}
	if dest.Host != "prod-gw.example.local" {
		t.Errorf("host = %q, want prod-gw.example.local (the connected system's rfc_host, not the default's)", dest.Host)
	}
	if dest.User == "DEVRFC" || dest.Password == "dev-secret" {
		t.Errorf("the default system's RFC credentials were used for another system: user %q", dest.User)
	}
}

// Two clients of one system share the URL; the client tells them apart.
func TestRFCDestination_TheClientSelectsTheSystem(t *testing.T) {
	s := serverFor(t, "https://prodsys-a.example:44300/", "200", "")

	dest, err := s.rfcDestination(map[string]any{})
	if err != nil {
		t.Fatalf("rfcDestination: %v", err)
	}
	if dest.Host != "prod-gw-b.example.local" {
		t.Errorf("host = %q, want prod-gw-b.example.local", dest.Host)
	}
}

func TestRFCDestination_ANamedSystemWins(t *testing.T) {
	s := serverFor(t, "https://prodsys-a.example:44300", "100", "prodsys-a")

	dest, err := s.rfcDestination(map[string]any{})
	if err != nil {
		t.Fatalf("rfcDestination: %v", err)
	}
	if dest.Host != "prod-gw.example.local" {
		t.Errorf("host = %q, want the named system's prod-gw.example.local", dest.Host)
	}
}

// A server whose system is not in .vsp.json gets no system's RFC settings:
// the gateway comes from its own URL.
func TestRFCDestination_AnUnknownSystemBorrowsNothing(t *testing.T) {
	s := serverFor(t, "https://other.example:44300", "100", "")

	dest, err := s.rfcDestination(map[string]any{})
	if err != nil {
		t.Fatalf("rfcDestination: %v", err)
	}
	if dest.Host != "other.example" {
		t.Errorf("host = %q, want other.example from the server's own URL", dest.Host)
	}
	if dest.User == "DEVRFC" {
		t.Error("borrowed the default system's RFC user")
	}
}

func TestRFCDestination_TheDefaultSystemStillGetsItsSettings(t *testing.T) {
	s := serverFor(t, "https://dev.example.local:44300", "100", "")

	dest, err := s.rfcDestination(map[string]any{})
	if err != nil {
		t.Fatalf("rfcDestination: %v", err)
	}
	if dest.Host != "dev-gw.example.local" || dest.User != "DEVRFC" {
		t.Errorf("host %q user %q, want dev-gw.example.local / DEVRFC", dest.Host, dest.User)
	}
}

// A server connected to one system logs on over RFC as itself, not as the
// SAP_USER a .env of another system puts into every server's environment.
func TestRFCDestination_TheLogonIsTheServersOwnNotSAPUser(t *testing.T) {
	s := serverFor(t, "https://prodsys-a.example:44300", "100", "prodsys-a")
	s.config.Username, s.config.Password = "READER", "reader-secret"
	t.Setenv("SAP_USER", "OTHERSYS")
	t.Setenv("SAP_PASSWORD", "other-secret")

	dest, err := s.rfcDestination(map[string]any{})
	if err != nil {
		t.Fatalf("rfcDestination: %v", err)
	}
	if dest.Host != "prod-gw.example.local" {
		t.Errorf("host = %q, want the named system's gateway", dest.Host)
	}
	if dest.User != "READER" || string(dest.Password) != "reader-secret" {
		t.Errorf("RFC logon = %q, want the server's own READER, not SAP_USER", dest.User)
	}
}

// The system's own rfc_password may come from VSP_<SYSTEM>_RFC_PASSWORD.
func TestRFCDestination_TheSystemsRFCPasswordVariable(t *testing.T) {
	s := serverFor(t, "https://prodsys-a.example:44300", "100", "")
	t.Setenv("VSP_PRODSYS-A_RFC_PASSWORD", "rfc-secret")

	dest, err := s.rfcDestination(map[string]any{})
	if err != nil {
		t.Fatalf("rfcDestination: %v", err)
	}
	if string(dest.Password) != "rfc-secret" {
		t.Errorf("password not taken from VSP_PRODSYS-A_RFC_PASSWORD")
	}
}

// An entry that only names a gateway has no URL to match on. As the default,
// it still applies to a server that matches nothing else, as it did before.
func TestRFCDestination_AURLlessDefaultStillApplies(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"default": "gw", "systems": {
	  "gw":    {"rfc_host": "gw.example.local", "rfc_sysnr": "00"},
	  "other": {"url": "https://other.example:44300", "client": "100", "rfc_host": "other-gw.example.local"}
	}}`
	if err := os.WriteFile(filepath.Join(dir, ".vsp.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	s := &Server{config: &Config{BaseURL: "https://sap.example:44300", Client: "100", Username: "TESTUSER", Password: "test-secret"}}

	dest, err := s.rfcDestination(map[string]any{})
	if err != nil {
		t.Fatalf("rfcDestination: %v", err)
	}
	if dest.Host != "gw.example.local" {
		t.Errorf("host = %q, want the URL-less default's gw.example.local", dest.Host)
	}
}

// An omitted client is the default client, not a wildcard.
func TestSameSystem_OmittedClientIsTheDefault(t *testing.T) {
	for _, c := range []struct {
		ua, ca, ub, cb string
		want           bool
	}{
		{"https://dev.example.local/", "100", "https://DEV.example.local", "100", true},
		{"https://dev.example.local", "", "https://dev.example.local", "001", true},
		{"https://dev.example.local", "", "https://dev.example.local", "100", false},
		{"https://dev.example.local", "100", "https://dev.example.local", "", false},
		{"https://dev.example.local", "100", "https://prodsys-a.example", "100", false},
		{"", "", "https://dev.example.local", "", false},
	} {
		if got := sameSystem(c.ua, c.ca, c.ub, c.cb); got != c.want {
			t.Errorf("sameSystem(%q,%q,%q,%q) = %v, want %v", c.ua, c.ca, c.ub, c.cb, got, c.want)
		}
	}
}

// A cookie or SSO server has no logon of its own. It keeps the RFC logon from
// SAP_USER/SAP_PASSWORD when SAP_URL and SAP_CLIENT name its system -- and only
// then, so a .env of one system does not log a server on another one in.
func TestRFCDestination_ACookieServerTakesTheEnvLogonOfItsOwnSystemOnly(t *testing.T) {
	cookieServer := func(t *testing.T, envURL, envClient string) *Server {
		s := serverFor(t, "https://prodsys-a.example:44300", "100", "")
		s.config.Username, s.config.Password = "", ""
		t.Setenv("SAP_URL", envURL)
		t.Setenv("SAP_CLIENT", envClient)
		return s
	}

	dest, err := cookieServer(t, "https://prodsys-a.example:44300/", "100").rfcDestination(map[string]any{})
	if err != nil {
		t.Fatalf("a cookie server lost its RFC logon: %v", err)
	}
	if dest.User != "TESTUSER" || dest.Password != "test-secret" {
		t.Errorf("logon = %q/%q, want SAP_USER/SAP_PASSWORD", dest.User, dest.Password)
	}

	for _, other := range []struct{ url, client string }{
		{"https://dev.example.local:44300", "100"},
		{"https://prodsys-a.example:44300", "200"},
	} {
		if dest, err := cookieServer(t, other.url, other.client).rfcDestination(map[string]any{}); err == nil {
			t.Errorf("SAP_URL=%s SAP_CLIENT=%s: a cookie server on another system logged on as %q", other.url, other.client, dest.User)
		}
	}
}
