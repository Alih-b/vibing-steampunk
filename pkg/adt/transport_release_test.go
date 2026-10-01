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

// --- What a release actually did ---
//
// ADT answers a release with HTTP 200 whether or not it released anything; the
// outcome is chkrun:status in the check report. Both answers below are the
// shape an S/4HANA 7.58 system sent, with names replaced.

// A request whose objects are locked elsewhere -- every transport of copies,
// since its objects stay locked in the original -- is not released; ADT asks
// whether to release it anyway.
const testReleaseLockedXML = `<?xml version="1.0" encoding="utf-8"?><tm:root tm:useraction="newreleasejobs" ` +
	`tm:releasetimestamp="20260101120000 " tm:releaseobjlock="yes" tm:number="TR-EXAMPLE" xmlns:tm="http://www.sap.com/cts/adt/tm">` +
	`<tm:releasereports><chkrun:checkReport chkrun:reporter="transportrelease" chkrun:triggeringUri="/sap/bc/adt/cts/transportrequests/TR-EXAMPLE" ` +
	`chkrun:status="relwithignlock" chkrun:statusText="Not all objects in the request could be locked&#10; Do you want to release them anyway?" ` +
	`xmlns:chkrun="http://www.sap.com/adt/checkrun"><chkrun:checkMessageList>` +
	`<chkrun:checkMessage chkrun:uri="/sap/bc/adt/cts/transportrequests/TR-EXAMPLE" chkrun:type="E" chkrun:shortText="ZCL_DEMO_A is locked in request/task TR-TASK"/>` +
	`<chkrun:checkMessage chkrun:uri="/sap/bc/adt/cts/transportrequests/TR-EXAMPLE" chkrun:type="E" chkrun:shortText="ZCL_DEMO_B is locked in request/task TR-TASK"/>` +
	`</chkrun:checkMessageList></chkrun:checkReport></tm:releasereports></tm:root>`

const testReleaseReleasedXML = `<?xml version="1.0" encoding="utf-8"?><tm:root tm:useraction="relwithignlock" ` +
	`tm:releasetimestamp="20260101120001 " tm:number="TR-EXAMPLE" xmlns:tm="http://www.sap.com/cts/adt/tm">` +
	`<tm:releasereports><chkrun:checkReport chkrun:reporter="transportrelease" chkrun:triggeringUri="/sap/bc/adt/cts/transportrequests/TR-EXAMPLE" ` +
	`chkrun:status="released" chkrun:statusText="Release for request/task TR-EXAMPLE has been started" ` +
	`xmlns:chkrun="http://www.sap.com/adt/checkrun"/></tm:releasereports></tm:root>`

type releaseCall struct {
	action, contentType, body string
}

func newReleaseClient(t *testing.T, answer map[string]string) (*Client, func() []releaseCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []releaseCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/cts/transportrequests/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		action := parts[len(parts)-1]
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, releaseCall{action: action, contentType: r.Header.Get("Content-Type"), body: string(b)})
		mu.Unlock()
		// relwithignlock without the tm:root document is refused, as ADT does.
		if action == "relwithignlock" && !strings.Contains(string(b), "tm:root") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">`+
				`<message lang="EN">System expected the element '{http://www.sap.com/cts/adt/tm}root'</message></exc:exception>`)
			return
		}
		w.Header().Set("Content-Type", acceptTransportOrganizerV1)
		_, _ = io.WriteString(w, answer[action])
	}))
	t.Cleanup(srv.Close)
	cfg := NewConfig(srv.URL, "TESTUSER", "secret", WithEnableTransports())
	return NewClientWithTransport(cfg, NewTransport(cfg)), func() []releaseCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]releaseCall(nil), calls...)
	}
}

func TestReleaseTransportV2_ALockedRequestIsNotReportedAsReleased(t *testing.T) {
	client, _ := newReleaseClient(t, map[string]string{"newreleasejobs": testReleaseLockedXML})

	err := client.ReleaseTransportV2(context.Background(), "TR-EXAMPLE", ReleaseTransportOptions{})
	if err == nil {
		t.Fatal("reported success for a request ADT did not release")
	}
	for _, want := range []string{"not released", "ZCL_DEMO_A is locked", "ignore_locks"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}

func TestReleaseTransportV2_Released(t *testing.T) {
	client, _ := newReleaseClient(t, map[string]string{"newreleasejobs": testReleaseReleasedXML})

	if err := client.ReleaseTransportV2(context.Background(), "TR-EXAMPLE", ReleaseTransportOptions{}); err != nil {
		t.Fatalf("a released request came back as an error: %v", err)
	}
}

// relwithignlock is refused without the tm:root document ADT's own client
// sends back; with it, the request is released.
func TestReleaseTransportV2_IgnoreLocksSendsTheRootDocument(t *testing.T) {
	client, calls := newReleaseClient(t, map[string]string{"relwithignlock": testReleaseReleasedXML})

	if err := client.ReleaseTransportV2(context.Background(), "tr-example", ReleaseTransportOptions{IgnoreLocks: true}); err != nil {
		t.Fatalf("ReleaseTransportV2 with IgnoreLocks: %v", err)
	}
	got := calls()
	if len(got) != 1 || got[0].action != "relwithignlock" {
		t.Fatalf("calls = %+v, want one relwithignlock", got)
	}
	for _, want := range []string{"<tm:root", `tm:useraction="relwithignlock"`, `tm:number="TR-EXAMPLE"`} {
		if !strings.Contains(got[0].body, want) {
			t.Errorf("body lacks %s: %s", want, got[0].body)
		}
	}
	if !strings.Contains(got[0].contentType, "transportorganizer") {
		t.Errorf("content type = %q, want the transport organizer type", got[0].contentType)
	}
}

// A system that answers with no report at all keeps the old behaviour: the
// HTTP status is all there is to go on.
func TestReleaseTransportV2_NoReportKeepsTheOldBehaviour(t *testing.T) {
	client, _ := newReleaseClient(t, map[string]string{"newreleasejobs": ""})

	if err := client.ReleaseTransportV2(context.Background(), "TR-EXAMPLE", ReleaseTransportOptions{}); err != nil {
		t.Fatalf("an empty answer became an error: %v", err)
	}
}

// A cut-off answer is not "no reports": that reading would call the request
// released. An empty answer keeps the old meaning.
func TestReleaseTransportV2_AnUnreadableAnswerIsNotSuccess(t *testing.T) {
	client, _ := newReleaseClient(t, map[string]string{"newreleasejobs": testReleaseLockedXML[:200]})
	err := client.ReleaseTransportV2(context.Background(), "TR-EXAMPLE", ReleaseTransportOptions{})
	if err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("a truncated answer: err = %v", err)
	}

	client, _ = newReleaseClient(t, map[string]string{"newreleasejobs": ""})
	if err := client.ReleaseTransportV2(context.Background(), "TR-EXAMPLE", ReleaseTransportOptions{}); err != nil {
		t.Errorf("an empty answer: %v", err)
	}
}
