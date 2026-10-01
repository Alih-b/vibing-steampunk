package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewConfig(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		username string
		password string
		opts     []Option
		want     *Config
	}{
		{
			name:     "default config",
			baseURL:  "https://sap.example.com:44300",
			username: "testuser",
			password: "testpass",
			opts:     nil,
			want: &Config{
				BaseURL:     "https://sap.example.com:44300",
				Username:    "testuser",
				Password:    "testpass",
				Client:      "001",
				Language:    "EN",
				SessionType: SessionStateless,
				Timeout:     60 * time.Second,
			},
		},
		{
			name:     "with custom client",
			baseURL:  "https://sap.example.com:44300",
			username: "testuser",
			password: "testpass",
			opts:     []Option{WithClient("100")},
			want: &Config{
				BaseURL:     "https://sap.example.com:44300",
				Username:    "testuser",
				Password:    "testpass",
				Client:      "100",
				Language:    "EN",
				SessionType: SessionStateless,
				Timeout:     60 * time.Second,
			},
		},
		{
			name:     "with custom language",
			baseURL:  "https://sap.example.com:44300",
			username: "testuser",
			password: "testpass",
			opts:     []Option{WithLanguage("DE")},
			want: &Config{
				BaseURL:     "https://sap.example.com:44300",
				Username:    "testuser",
				Password:    "testpass",
				Client:      "001",
				Language:    "DE",
				SessionType: SessionStateless,
				Timeout:     60 * time.Second,
			},
		},
		{
			name:     "with insecure skip verify",
			baseURL:  "https://sap.example.com:44300",
			username: "testuser",
			password: "testpass",
			opts:     []Option{WithInsecureSkipVerify()},
			want: &Config{
				BaseURL:            "https://sap.example.com:44300",
				Username:           "testuser",
				Password:           "testpass",
				Client:             "001",
				Language:           "EN",
				InsecureSkipVerify: true,
				SessionType:        SessionStateless,
				Timeout:            60 * time.Second,
			},
		},
		{
			name:     "with stateful session (explicit)",
			baseURL:  "https://sap.example.com:44300",
			username: "testuser",
			password: "testpass",
			opts:     []Option{WithSessionType(SessionStateful)},
			want: &Config{
				BaseURL:     "https://sap.example.com:44300",
				Username:    "testuser",
				Password:    "testpass",
				Client:      "001",
				Language:    "EN",
				SessionType: SessionStateful,
				Timeout:     60 * time.Second,
			},
		},
		{
			name:     "with custom timeout",
			baseURL:  "https://sap.example.com:44300",
			username: "testuser",
			password: "testpass",
			opts:     []Option{WithTimeout(60 * time.Second)},
			want: &Config{
				BaseURL:     "https://sap.example.com:44300",
				Username:    "testuser",
				Password:    "testpass",
				Client:      "001",
				Language:    "EN",
				SessionType: SessionStateless,
				Timeout:     60 * time.Second,
			},
		},
		{
			name:     "with multiple options",
			baseURL:  "https://sap.example.com:44300",
			username: "testuser",
			password: "testpass",
			opts: []Option{
				WithClient("200"),
				WithLanguage("FR"),
				WithInsecureSkipVerify(),
				WithTimeout(120 * time.Second),
			},
			want: &Config{
				BaseURL:            "https://sap.example.com:44300",
				Username:           "testuser",
				Password:           "testpass",
				Client:             "200",
				Language:           "FR",
				InsecureSkipVerify: true,
				SessionType:        SessionStateless,
				Timeout:            120 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewConfig(tt.baseURL, tt.username, tt.password, tt.opts...)

			if got.BaseURL != tt.want.BaseURL {
				t.Errorf("BaseURL = %v, want %v", got.BaseURL, tt.want.BaseURL)
			}
			if got.Username != tt.want.Username {
				t.Errorf("Username = %v, want %v", got.Username, tt.want.Username)
			}
			if got.Password != tt.want.Password {
				t.Errorf("Password = %v, want %v", got.Password, tt.want.Password)
			}
			if got.Client != tt.want.Client {
				t.Errorf("Client = %v, want %v", got.Client, tt.want.Client)
			}
			if got.Language != tt.want.Language {
				t.Errorf("Language = %v, want %v", got.Language, tt.want.Language)
			}
			if got.InsecureSkipVerify != tt.want.InsecureSkipVerify {
				t.Errorf("InsecureSkipVerify = %v, want %v", got.InsecureSkipVerify, tt.want.InsecureSkipVerify)
			}
			if got.SessionType != tt.want.SessionType {
				t.Errorf("SessionType = %v, want %v", got.SessionType, tt.want.SessionType)
			}
			if got.Timeout != tt.want.Timeout {
				t.Errorf("Timeout = %v, want %v", got.Timeout, tt.want.Timeout)
			}
		})
	}
}

func TestNewHTTPClient(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	client := cfg.NewHTTPClient()

	if client == nil {
		t.Error("NewHTTPClient returned nil")
	}
	if client.Jar == nil {
		t.Error("HTTP client should have cookie jar")
	}
	if client.Timeout != cfg.Timeout {
		t.Errorf("HTTP client timeout = %v, want %v", client.Timeout, cfg.Timeout)
	}

	// Verify transport has proxy configured (fixes #13)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Error("HTTP client transport should be *http.Transport")
	} else if transport.Proxy == nil {
		t.Error("HTTP transport should have Proxy function set (for HTTP_PROXY/HTTPS_PROXY support)")
	}
}

func TestSessionTypes(t *testing.T) {
	if SessionStateful != "stateful" {
		t.Errorf("SessionStateful = %v, want stateful", SessionStateful)
	}
	if SessionStateless != "stateless" {
		t.Errorf("SessionStateless = %v, want stateless", SessionStateless)
	}
	if SessionKeep != "keep" {
		t.Errorf("SessionKeep = %v, want keep", SessionKeep)
	}
}

// TestNewHTTPClient_CheckRedirectPreservesADTHeaders verifies that the
// HTTP client's CheckRedirect callback re-sets headers that are
// load-bearing for the ADT lock→write→unlock sequence across redirects:
//   - Authorization: Go strips this by default on cross-origin redirects
//     (sensitive header per RFC 7235). Without restoration, SAML flows get
//     401 even when curl works (issue #90).
//   - X-CSRF-Token: mutation requests are rejected as CSRF-violating if
//     this disappears mid-sequence.
//   - X-sap-adt-sessiontype: the lock handle is bound to a stateful
//     session; if a redirect hop lands stateless, the subsequent PUT
//     can't find the lock and gets HTTP 423.
//
// Go's default forwards custom headers on same-origin redirects, but this
// test pins the behaviour explicitly so future refactors can't silently
// drop them.
func TestNewHTTPClient_CheckRedirectPreservesADTHeaders(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	client := cfg.NewHTTPClient()

	if client.CheckRedirect == nil {
		t.Fatal("CheckRedirect must be set")
	}

	// Build the "via" chain: the original request that carries the ADT
	// headers we expect to be re-set on the redirect target.
	orig, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://sap.example.com:44300/sap/bc/adt/oo/classes/ZFOO?_action=LOCK", nil)
	if err != nil {
		t.Fatalf("NewRequest(orig): %v", err)
	}
	orig.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	orig.Header.Set("X-CSRF-Token", "ABC123==")
	orig.Header.Set("X-sap-adt-sessiontype", "stateful")

	// The redirect target that Go would follow — initially without any
	// of the ADT headers (simulating Go having stripped them cross-origin).
	next, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://sap.example.com:44300/sap/bc/adt/follow", nil)
	if err != nil {
		t.Fatalf("NewRequest(next): %v", err)
	}

	if err := client.CheckRedirect(next, []*http.Request{orig}); err != nil {
		t.Fatalf("CheckRedirect returned error: %v", err)
	}

	if got := next.Header.Get("Authorization"); got != "Basic dXNlcjpwYXNz" {
		t.Errorf("Authorization = %q, want preserved from initial request (issue #90)", got)
	}
	if got := next.Header.Get("X-CSRF-Token"); got != "ABC123==" {
		t.Errorf("X-CSRF-Token = %q, want preserved — mutation requests need it to survive redirect hops", got)
	}
	if got := next.Header.Get("X-sap-adt-sessiontype"); got != "stateful" {
		t.Errorf("X-sap-adt-sessiontype = %q, want preserved — lock handles are bound to a stateful session (issue #88)", got)
	}
}

// TestNewHTTPClient_CheckRedirectHonoursLimit guards the 10-redirect cap
// that CheckRedirect enforces to prevent infinite loops.
func TestNewHTTPClient_CheckRedirectHonoursLimit(t *testing.T) {
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass")
	client := cfg.NewHTTPClient()

	if client.CheckRedirect == nil {
		t.Fatal("CheckRedirect must be set")
	}

	next, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://sap.example.com:44300/", nil)
	via := make([]*http.Request, 10)
	for i := range via {
		via[i], _ = http.NewRequestWithContext(context.Background(), http.MethodGet, "https://sap.example.com:44300/", nil)
	}

	if err := client.CheckRedirect(next, via); err == nil {
		t.Error("CheckRedirect must return an error on the 10th redirect")
	}
}

// TestCheckRedirect_DoesNotLeakCredentialsOffHost guards the narrowing of the
// header re-attach. The handler exists so a redirect inside the SAP system
// keeps Authorization (issue #90); it must not hand Basic credentials and the
// session CSRF token to an identity provider on another host, which is exactly
// where an expired SSO session redirects to.
// It drives a real redirect through the client rather than calling
// CheckRedirect directly. That distinction is the whole test: net/http copies
// every non-sensitive header onto the next request *before* CheckRedirect runs
// (client.go — copyHeaders then checkRedirect), so a handler that merely
// declines to set X-CSRF-Token off-host leaves Go's copy of it in place and the
// identity provider receives the session token anyway. Calling CheckRedirect
// with a freshly-made empty header map cannot see that, and passed while the
// leak was live.
func TestCheckRedirect_DoesNotLeakCredentialsOffHost(t *testing.T) {
	var idpAuth, idpCSRF, idpSessionType string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idpAuth = r.Header.Get("Authorization")
		idpCSRF = r.Header.Get("X-CSRF-Token")
		idpSessionType = r.Header.Get("X-sap-adt-sessiontype")
	}))
	defer idp.Close()
	// httptest serves on 127.0.0.1; "localhost" is a different hostname to
	// Go's cross-origin test, which is what makes this a foreign host.
	idpURL := strings.Replace(idp.URL, "127.0.0.1", "localhost", 1)

	var sapAuth, sapCSRF string
	var sap *httptest.Server
	sap = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sap/bc/adt/discovery":
			http.Redirect(w, r, idpURL+"/saml/sso", http.StatusFound)
		case "/sap/bc/adt/hop":
			// Same host, different path — the intra-SAP redirect the handler
			// exists to serve.
			http.Redirect(w, r, sap.URL+"/sap/bc/adt/landed", http.StatusFound)
		default:
			sapAuth = r.Header.Get("Authorization")
			sapCSRF = r.Header.Get("X-CSRF-Token")
		}
	}))
	defer sap.Close()

	cfg := NewConfig(sap.URL, "TESTUSER", "secret")
	client := cfg.NewHTTPClient()

	do := func(path string) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, sap.URL+path, nil)
		if err != nil {
			t.Fatalf("building request: %v", err)
		}
		req.SetBasicAuth("TESTUSER", "secret")
		req.Header.Set("X-CSRF-Token", "tok")
		req.Header.Set("X-sap-adt-sessiontype", "stateful")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
	}

	do("/sap/bc/adt/discovery")
	if idpAuth != "" {
		t.Errorf("the identity provider received Authorization=%q", idpAuth)
	}
	if idpCSRF != "" {
		t.Errorf("the identity provider received X-CSRF-Token=%q — declining to set a "+
			"header is not the same as deleting the copy net/http already made", idpCSRF)
	}
	if idpSessionType != "" {
		t.Errorf("the identity provider received X-sap-adt-sessiontype=%q", idpSessionType)
	}

	do("/sap/bc/adt/hop")
	if sapAuth == "" {
		t.Error("an intra-SAP redirect lost Authorization — issue #90 is what this handler is for")
	}
	if sapCSRF != "tok" {
		t.Errorf("an intra-SAP redirect carried X-CSRF-Token=%q, want tok — the lock→write "+
			"sequence needs it on the second hop", sapCSRF)
	}
}

// TestCheckRedirect_HostMatchIgnoresCaseAndPort pins the comparison itself.
// `req.URL.Host == sapHost` treated an ICM redirect that merely changed the
// case of the FQDN, or spelled out :443, as a hop to a foreign host — which
// silently dropped the very headers the handler exists to preserve. The port is
// ignored outright, because the off-host branch now *deletes* those headers and
// must not be stricter than net/http's own rule, which compares hostnames only:
// one box answering on two ports is not a credential boundary.
func TestCheckRedirect_HostMatchIgnoresCaseAndPort(t *testing.T) {
	cfg := NewConfig("https://SAPDEV.example.com", "TESTUSER", "secret")
	client := cfg.NewHTTPClient()

	first, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://SAPDEV.example.com/sap/bc/adt/discovery", nil)
	first.SetBasicAuth("TESTUSER", "secret")
	first.Header.Set("X-CSRF-Token", "tok")

	for _, tc := range []struct {
		name    string
		target  string
		wantSet bool
	}{
		{"case-differing FQDN is the same host", "https://sapdev.example.com/sap/bc/adt/other", true},
		{"explicit default port is the same host", "https://sapdev.example.com:443/sap/bc/adt/other", true},
		{"another port on the same box is the same host", "https://sapdev.example.com:8443/sap/bc/adt/other", true},
		{"foreign idp is not", "https://idp.example.org/saml/sso", false},
		{"a sibling subdomain is not", "https://idp.sapdev.example.com/saml/sso", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.target, nil)
			if err := client.CheckRedirect(next, []*http.Request{first}); err != nil {
				t.Fatalf("CheckRedirect: %v", err)
			}
			gotAuth := next.Header.Get("Authorization") != ""
			gotCSRF := next.Header.Get("X-CSRF-Token") != ""
			if gotAuth != tc.wantSet || gotCSRF != tc.wantSet {
				t.Errorf("target %s: Authorization set=%v, X-CSRF-Token set=%v, want both %v",
					tc.target, gotAuth, gotCSRF, tc.wantSet)
			}
		})
	}
}

// TestCheckRedirect_DoesNotDowngradeScheme pins the half of the rule the host
// comparison cannot see: a hop to the same host over plain http would send the
// credentials and the CSRF token in clear text.
func TestCheckRedirect_DoesNotDowngradeScheme(t *testing.T) {
	for _, tc := range []struct {
		name    string
		base    string
		target  string
		wantSet bool
	}{
		{"https to http on the same host drops them", "https://sapdev.example.com", "http://sapdev.example.com:8000/sap/bc/adt/x", false},
		{"https to http on the same port drops them", "https://sapdev.example.com:44300", "http://sapdev.example.com:44300/x", false},
		{"http to https on another port keeps them", "http://sapdev.example.com:8000", "https://sapdev.example.com:44300/sap/bc/adt/x", true},
		{"http to http on the same host keeps them", "http://sapdev.example.com:8000", "http://sapdev.example.com:8001/x", true},
		{"https to https on another port keeps them", "https://sapdev.example.com:44300", "https://sapdev.example.com:8443/x", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewConfig(tc.base, "TESTUSER", "secret").NewHTTPClient()
			first, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.base+"/sap/bc/adt/discovery", nil)
			first.SetBasicAuth("TESTUSER", "secret")
			first.Header.Set("X-CSRF-Token", "tok")
			first.Header.Set("X-sap-adt-sessiontype", "stateful")

			next, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.target, nil)
			// What net/http's own copier has already done before CheckRedirect runs.
			next.Header.Set("X-CSRF-Token", "tok")
			next.Header.Set("X-sap-adt-sessiontype", "stateful")
			if err := client.CheckRedirect(next, []*http.Request{first}); err != nil {
				t.Fatalf("CheckRedirect: %v", err)
			}
			for _, h := range []string{"Authorization", "X-CSRF-Token", "X-sap-adt-sessiontype"} {
				if got := next.Header.Get(h) != ""; got != tc.wantSet {
					t.Errorf("%s set=%v, want %v", h, got, tc.wantSet)
				}
			}
		})
	}
}

func TestKeepsSAPCredentials(t *testing.T) {
	parse := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	for _, tc := range []struct {
		name         string
		base, target string
		want         bool
	}{
		{"same origin", "https://sap.example.com", "https://sap.example.com/x", true},
		{"case-folded host", "https://SAP.example.com", "https://sap.example.com/x", true},
		{"explicit default port", "https://sap.example.com", "https://sap.example.com:443/x", true},
		{"foreign host", "https://sap.example.com", "https://idp.example.org/x", false},
		{"downgrade", "https://sap.example.com", "http://sap.example.com/x", false},
		{"upgrade", "http://sap.example.com:8000", "https://sap.example.com:44300/x", true},
		{"scheme-less BaseURL has no host", "sap.example.com", "https://sap.example.com/x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := keepsSAPCredentials(parse(tc.base), parse(tc.target)); got != tc.want {
				t.Errorf("keepsSAPCredentials(%s, %s) = %v, want %v", tc.base, tc.target, got, tc.want)
			}
		})
	}
	if keepsSAPCredentials(nil, parse("https://sap.example.com")) {
		t.Error("nil base keeps credentials")
	}
}
