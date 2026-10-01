package adt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// --- Creating a request inside a CTS project ---
//
// Systems that organise work in CTS projects expect every request to carry
// one. ADT's create endpoint takes it as tm:cts_project, and the target as
// tm:target; vsp used to send both empty, so a request it created belonged to
// no project and had to be fixed up by hand in SE09.

const testCreatedTransportXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<tm:root tm:useraction="newrequest" xmlns:tm="http://www.sap.com/cts/adt/tm">` +
	`<tm:request tm:number="TR-EXAMPLE" tm:desc="demo" tm:type="K"/></tm:root>`

// createRecorder answers the create call and keeps the body it was sent.
type createRecorder struct {
	mu     sync.Mutex
	bodies []string
}

func newCreateClient(t *testing.T, opts ...Option) (*Client, *createRecorder) {
	t.Helper()
	rec := &createRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cts/transportrequests") {
			body, _ := io.ReadAll(r.Body)
			rec.mu.Lock()
			rec.bodies = append(rec.bodies, string(body))
			rec.mu.Unlock()
			w.Header().Set("Content-Type", acceptTransportOrganizerV1)
			_, _ = io.WriteString(w, testCreatedTransportXML)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	opts = append([]Option{WithEnableTransports()}, opts...)
	cfg := NewConfig(srv.URL, "TESTUSER", "secret", opts...)
	return NewClientWithTransport(cfg, NewTransport(cfg)), rec
}

func (r *createRecorder) only(t *testing.T) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) != 1 {
		t.Fatalf("expected one create request, got %d", len(r.bodies))
	}
	return r.bodies[0]
}

func assertAttr(t *testing.T, body, attr, want string) {
	t.Helper()
	if needle := attr + `="` + want + `"`; !strings.Contains(body, needle) {
		t.Errorf("create body lacks %s\nbody: %s", needle, body)
	}
}

func TestCreateTransportV2_SendsProjectAndTarget(t *testing.T) {
	client, rec := newCreateClient(t)

	number, err := client.CreateTransportV2(context.Background(), CreateTransportOptions{
		Description: "demo",
		Package:     "ZDEMO",
		CTSProject:  "PRJ_DEMO",
		Target:      "/ZDEMO/",
	})
	if err != nil {
		t.Fatalf("CreateTransportV2: %v", err)
	}
	if number != "TR-EXAMPLE" {
		t.Errorf("number = %q, want TR-EXAMPLE", number)
	}
	body := rec.only(t)
	assertAttr(t, body, "tm:cts_project", "PRJ_DEMO")
	assertAttr(t, body, "tm:target", "/ZDEMO/")
}

func TestCreateTransportV2_FallsBackToTheConfiguredProject(t *testing.T) {
	client, rec := newCreateClient(t, WithCTSProject("PRJ_DEMO"), WithTransportTarget("/ZDEMO/"))

	if _, err := client.CreateTransportV2(context.Background(), CreateTransportOptions{
		Description: "demo",
		Package:     "ZDEMO",
	}); err != nil {
		t.Fatalf("CreateTransportV2: %v", err)
	}
	body := rec.only(t)
	assertAttr(t, body, "tm:cts_project", "PRJ_DEMO")
	assertAttr(t, body, "tm:target", "/ZDEMO/")
}

func TestCreateTransportV2_AnExplicitProjectWinsOverTheConfiguredOne(t *testing.T) {
	client, rec := newCreateClient(t, WithCTSProject("PRJ_DEFAULT"))

	if _, err := client.CreateTransportV2(context.Background(), CreateTransportOptions{
		Description: "demo",
		Package:     "ZDEMO",
		CTSProject:  "PRJ_OTHER",
	}); err != nil {
		t.Fatalf("CreateTransportV2: %v", err)
	}
	assertAttr(t, rec.only(t), "tm:cts_project", "PRJ_OTHER")
}

func TestCreateTransportV2_WithoutAProjectSendsItEmptyAsBefore(t *testing.T) {
	client, rec := newCreateClient(t)

	if _, err := client.CreateTransportV2(context.Background(), CreateTransportOptions{
		Description: "demo",
		Package:     "ZDEMO",
	}); err != nil {
		t.Fatalf("CreateTransportV2: %v", err)
	}
	body := rec.only(t)
	assertAttr(t, body, "tm:cts_project", "")
	assertAttr(t, body, "tm:target", "")
}

// The request vsp creates by itself for a write that names none must land in
// the project too, or --enable-transports keeps producing orphans.
func TestCreateTransport_TheAutomaticRequestUsesTheConfiguredProject(t *testing.T) {
	client, rec := newCreateClient(t, WithCTSProject("PRJ_DEMO"), WithTransportTarget("/ZDEMO/"))

	if _, err := client.CreateTransport(context.Background(),
		"/sap/bc/adt/programs/programs/ZDEMO_PROG", "demo", "ZDEMO"); err != nil {
		t.Fatalf("CreateTransport: %v", err)
	}
	body := rec.only(t)
	assertAttr(t, body, "tm:cts_project", "PRJ_DEMO")
	assertAttr(t, body, "tm:target", "/ZDEMO/")
}

func TestCreateTransportV2_EscapesTheProject(t *testing.T) {
	client, rec := newCreateClient(t)

	if _, err := client.CreateTransportV2(context.Background(), CreateTransportOptions{
		Description: "demo",
		Package:     "ZDEMO",
		CTSProject:  `P"<&`,
	}); err != nil {
		t.Fatalf("CreateTransportV2: %v", err)
	}
	if body := rec.only(t); strings.Contains(body, `P"<&`) {
		t.Errorf("project went into the XML unescaped: %s", body)
	}
}
