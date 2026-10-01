package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const silentRefusalXML = `<?xml version="1.0" encoding="utf-8"?><chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist">` +
	`<chkl:properties checkExecuted="true" activationExecuted="false" generationExecuted="false"/></chkl:messages>`

func inactiveFeed(parts ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/inactivectsobjects">`)
	for _, p := range parts {
		b.WriteString(`<ioc:entry><ioc:object ioc:user="TESTUSER"><ioc:ref adtcore:uri="/sap/bc/adt/functions/groups/zdemo/includes/` +
			strings.ToLower(p) + `" adtcore:type="FUGR/I" adtcore:name="` + p + `" xmlns:adtcore="http://www.sap.com/adt/core"/></ioc:object></ioc:entry>`)
	}
	b.WriteString(`</ioc:inactiveObjects>`)
	return b.String()
}

// partsServer refuses every activation without a word and serves the
// inactive list in turn from feeds, the last one for every later read.
func partsServer(t *testing.T, user string, feeds ...string) (*Client, func() int) {
	t.Helper()
	var mu sync.Mutex
	reads, activations := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("X-CSRF-Token", "TOKEN")
		switch {
		case strings.Contains(r.URL.Path, "/activation/inactiveobjects"):
			feed := feeds[len(feeds)-1]
			if reads < len(feeds) {
				feed = feeds[reads]
			}
			reads++
			_, _ = w.Write([]byte(feed))
		case strings.Contains(r.URL.Path, "/activation"):
			activations++
			_, _ = w.Write([]byte(silentRefusalXML))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	cfg := NewConfig(srv.URL, user, "secret")
	return NewClientWithTransport(cfg, NewTransport(cfg)), func() int { mu.Lock(); defer mu.Unlock(); return activations }
}

// Refused again without a word: what is named is what is inactive after the
// try, not everything that was tried.
func TestActivateWithInactiveParts_NamesWhatIsStillInactive(t *testing.T) {
	c, _ := partsServer(t, "TESTUSER", inactiveFeed("LZDEMOTOP", "LZDEMOF01"), inactiveFeed("LZDEMOF01"))
	res, err := c.activateWithInactiveParts(context.Background(), "/sap/bc/adt/functions/groups/zdemo", "ZDEMO", &ActivationResult{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Inactive) != 1 || res.Inactive[0].Name != "LZDEMOF01" {
		t.Errorf("still inactive = %+v, want only LZDEMOF01", res.Inactive)
	}
}

// Without a user name (cookie/SSO) nobody's parts are activated along: they
// are named instead.
func TestActivateWithInactiveParts_NoUserNameActivatesNoParts(t *testing.T) {
	c, activations := partsServer(t, "", inactiveFeed("LZDEMOTOP"))
	res, err := c.activateWithInactiveParts(context.Background(), "/sap/bc/adt/functions/groups/zdemo", "ZDEMO", &ActivationResult{})
	if err != nil {
		t.Fatal(err)
	}
	if activations() != 0 {
		t.Errorf("%d activations sent without knowing whose parts they are", activations())
	}
	if len(res.Inactive) != 1 || res.Success {
		t.Errorf("result = %+v, want the part named and no success", res)
	}
}
