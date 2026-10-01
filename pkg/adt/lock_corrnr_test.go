package adt

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// corrNr on the LOCK request. ADT takes the transport on the LOCK itself, and
// on-premise systems that bind the lock to a request expect it there. These
// tests pin it for LockObject and for the write paths that lock on their own.

// TestLockObject_EmitsCorrNr pins both directions: a supplied transport is on
// the LOCK, and without one the request is exactly what it was before.
func TestLockObject_EmitsCorrNr(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport []string
		want      string
	}{
		{"with transport", []string{"TR-EXAMPLE"}, "TR-EXAMPLE"},
		{"empty transport", []string{""}, ""},
		{"no transport argument", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &lockQueryRecorder{respond: func(*http.Request) (int, string) { return http.StatusOK, lockHandleXML }}
			if _, err := transportableEditClient(rec).LockObject(context.Background(),
				"/sap/bc/adt/oo/classes/zcl_demo", "MODIFY", tc.transport...); err != nil {
				t.Fatalf("LockObject: %v", err)
			}
			if len(rec.locks) != 1 {
				t.Fatalf("recorded %d LOCK requests, want 1", len(rec.locks))
			}
			if got, present := rec.locks[0].Get("corrNr"), rec.locks[0].Has("corrNr"); got != tc.want || present != (tc.want != "") {
				t.Errorf("LOCK corrNr = %q (present %v), want %q", got, present, tc.want)
			}
		})
	}
}

// lockQueryRecorder answers every request through respond and remembers the
// query of each LOCK. Bodies are built per call, so a document read twice —
// once before the lock and once under it — is served twice.
type lockQueryRecorder struct {
	respond func(r *http.Request) (int, string)
	locks   []url.Values
}

func (m *lockQueryRecorder) Do(r *http.Request) (*http.Response, error) {
	if r.URL.Query().Get("_action") == "LOCK" {
		m.locks = append(m.locks, r.URL.Query())
	}
	status, body := m.respond(r)
	h := http.Header{}
	h.Set("X-CSRF-Token", "test-token")
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: h}, nil
}

const lockHandleXML = `<?xml version="1.0"?><asx:abap xmlns:asx="http://www.sap.com/abapxml">` +
	`<asx:values><DATA><LOCK_HANDLE>LH-1</LOCK_HANDLE></DATA></asx:values></asx:abap>`

func transportableEditClient(doer HTTPDoer) *Client {
	safety := UnrestrictedSafetyConfig()
	safety.AllowTransportableEdits = true
	cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithSafety(safety))
	return NewClientWithTransport(cfg, NewTransportWithClient(cfg, doer))
}

func assertLockCarried(t *testing.T, locks []url.Values, want string) {
	t.Helper()
	if len(locks) == 0 {
		t.Fatal("no LOCK request was recorded")
	}
	for _, q := range locks {
		if got := q.Get("corrNr"); got != want {
			t.Errorf("LOCK carried corrNr=%q, want %q", got, want)
		}
	}
}

// TestSetDescription_PassesTransportToLock pins corrNr on the LOCK of the
// description write (#201), which takes its own lock.
func TestSetDescription_PassesTransportToLock(t *testing.T) {
	rec := &lockQueryRecorder{respond: func(r *http.Request) (int, string) {
		switch {
		case r.URL.Query().Get("_action") == "LOCK":
			return http.StatusOK, lockHandleXML
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/programs/programs/zdemo"):
			return http.StatusOK, `<program:abapProgram xmlns:adtcore="http://www.sap.com/adt/core" adtcore:description="Old"/>`
		default:
			return http.StatusOK, ""
		}
	}}

	if _, err := transportableEditClient(rec).SetDescription(context.Background(), "PROG", "ZDEMO", "", "New", "TR-EXAMPLE"); err != nil {
		t.Fatalf("SetDescription failed: %v", err)
	}
	assertLockCarried(t, rec.locks, "TR-EXAMPLE")
}

// TestWriteTextPool_PassesTransportToLock is the same pin for the text pool
// write (#200).
func TestWriteTextPool_PassesTransportToLock(t *testing.T) {
	rec := &lockQueryRecorder{respond: func(r *http.Request) (int, string) {
		switch {
		case r.URL.Query().Get("_action") == "LOCK":
			return http.StatusOK, lockHandleXML
		case strings.Contains(r.URL.Path, "/datapreview/"):
			// MasterLanguage reads TADIR; E is the master, so EN is no translation.
			return http.StatusOK, tableXML(xmlCol{name: "MASTERLANG", data: []string{"E"}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/textelements/"):
			return http.StatusOK, "001=Old\n"
		default:
			return http.StatusOK, ""
		}
	}}

	texts := map[string]map[string]string{"I": {"001": "New"}}
	_, err := transportableEditClient(rec).WriteTextPool(context.Background(),
		TextPoolTarget{Type: "CLAS", Name: "ZCL_DEMO"}, "EN", texts, "TR-EXAMPLE", TextPoolOptions{})
	if err != nil {
		t.Fatalf("WriteTextPool failed: %v", err)
	}
	assertLockCarried(t, rec.locks, "TR-EXAMPLE")
}

func TestTransportChoice_LockCorrNr(t *testing.T) {
	for _, tc := range []struct {
		name     string
		plan     *TransportChoice
		supplied string
		want     string
	}{
		{"supplied wins over the plan", &TransportChoice{Transport: "TR-PLANNED"}, "TR-NAMED", "TR-NAMED"},
		{"no plan, nothing supplied", nil, "", ""},
		{"no plan, supplied", nil, "TR-NAMED", "TR-NAMED"},
		{"plan chose a request", &TransportChoice{Transport: "TR-PLANNED"}, "", "TR-PLANNED"},
		{"plan chose nothing", &TransportChoice{Reason: "left to SAP"}, "", ""},
		{"plan failed to create one", &TransportChoice{Transport: "TR-PLANNED", Err: errTransportCreate}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.plan.lockCorrNr(tc.supplied); got != tc.want {
				t.Errorf("lockCorrNr = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSetDescription_PlannedTransportGoesOnLock pins the case the supplied-only
// tests cannot: the caller names no request, the plan picks one, and the LOCK
// must carry it — the write that follows uses the plan's request, so a bare
// LOCK would bind the lock and the PUT to different ones.
func TestSetDescription_PlannedTransportGoesOnLock(t *testing.T) {
	const check = `<?xml version="1.0"?><asx:abap xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA>` +
		`<OBJECTNAME>ZDEMO</OBJECTNAME><DEVCLASS>ZDEMO</DEVCLASS><RECORDING>X</RECORDING><REQUESTS><CTS_REQUEST><REQ_HEADER>` +
		`<TRKORR>TR-EXAMPLE</TRKORR><TRSTATUS>D</TRSTATUS><AS4TEXT>feature</AS4TEXT></REQ_HEADER></CTS_REQUEST></REQUESTS></DATA></asx:values></asx:abap>`
	rec := &lockQueryRecorder{respond: func(r *http.Request) (int, string) {
		switch {
		case r.URL.Query().Get("_action") == "LOCK":
			return http.StatusOK, lockHandleXML
		case strings.HasSuffix(r.URL.Path, "/cts/transportchecks"):
			return http.StatusOK, check
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/programs/programs/zdemo"):
			return http.StatusOK, `<program:abapProgram xmlns:adtcore="http://www.sap.com/adt/core" adtcore:description="Old"/>`
		default:
			return http.StatusOK, ""
		}
	}}

	res, err := transportableEditClient(rec).SetDescription(context.Background(), "PROG", "ZDEMO", "", "New", "")
	if err != nil {
		t.Fatalf("SetDescription failed: %v", err)
	}
	assertLockCarried(t, rec.locks, "TR-EXAMPLE")
	if res.Transport != "TR-EXAMPLE" {
		t.Errorf("write went under %q, want the planned TR-EXAMPLE", res.Transport)
	}
}

// TestLockObject_RefusesADisallowedTransportBeforeTheLock: the transport goes
// out on the LOCK, so the transport policy has to be checked before it, not
// only in the write that follows.
func TestLockObject_RefusesADisallowedTransportBeforeTheLock(t *testing.T) {
	for _, tc := range []struct {
		name   string
		safety func(*SafetyConfig)
	}{
		{"transportable edits disabled", func(s *SafetyConfig) { s.AllowTransportableEdits = false }},
		{"transport not in the allowlist", func(s *SafetyConfig) {
			s.AllowTransportableEdits = true
			s.AllowedTransports = []string{"TR-ALLOWED*"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &lockQueryRecorder{respond: func(*http.Request) (int, string) { return http.StatusOK, lockHandleXML }}
			safety := UnrestrictedSafetyConfig()
			tc.safety(&safety)
			cfg := NewConfig("https://sap.example.com:44300", "user", "pass", WithSafety(safety))
			c := NewClientWithTransport(cfg, NewTransportWithClient(cfg, rec))
			if _, err := c.LockObject(context.Background(), "/sap/bc/adt/oo/classes/zcl_demo", "MODIFY", "TR-EXAMPLE"); err == nil {
				t.Error("LockObject accepted a transport the configuration disallows")
			}
			if len(rec.locks) != 0 {
				t.Errorf("sent %d LOCK requests carrying a disallowed transport, want 0", len(rec.locks))
			}
		})
	}
}
