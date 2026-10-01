package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestHandleSearchObject_ServerSideTypeFilter verifies the MCP search path goes
// through SearchObjectByType: the short-form type is canonicalized and sent to
// the server as the objectType query param, and maxResults is forwarded — so
// max applies after the type filter (the bug txape10 flagged on PR #126).
// The handler asks for one more than requested (see handleSearchObject) so it
// can tell a full page from a truncated one; that over-fetch applies here too.
func TestHandleSearchObject_ServerSideTypeFilter(t *testing.T) {
	var searchQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "informationsystem/search") {
			searchQuery = r.URL.RawQuery
		}
		w.Header().Set("X-CSRF-Token", "test-token")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
			`<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core"/>`))
	}))
	defer ts.Close()

	cfg := &Config{
		BaseURL:            ts.URL,
		Username:           "u",
		Password:           "p",
		Client:             "001",
		Language:           "EN",
		InsecureSkipVerify: true,
	}
	server := NewServer(cfg)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}

	req := newRequest(map[string]any{
		"query":      "Z*",
		"objectType": "CLAS",
		"maxResults": float64(5),
	})
	res, err := server.handleSearchObject(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSearchObject error: %v", err)
	}
	if res.IsError {
		t.Fatalf("handleSearchObject returned an error result: %+v", res)
	}

	if searchQuery == "" {
		t.Fatal("no search request reached the server")
	}
	q, err := url.ParseQuery(searchQuery)
	if err != nil {
		t.Fatalf("parsing captured query %q: %v", searchQuery, err)
	}
	if got := q.Get("objectType"); got != "CLAS/OC" {
		t.Errorf("objectType = %q, want %q (short form should be canonicalized)", got, "CLAS/OC")
	}
	if got := q.Get("maxResults"); got != "6" {
		t.Errorf("maxResults = %q, want %q (requested 5, plus the truncation-detection over-fetch)", got, "6")
	}
	if got := q.Get("query"); got != "Z*" {
		t.Errorf("query = %q, want %q", got, "Z*")
	}
}

// SAP(action="search", params={"exact": true}) keeps only the objects named
// exactly as the query, with the type and max filters still applied.
func TestRouteSearchExact(t *testing.T) {
	var searchQuery url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "informationsystem/search") {
			searchQuery = r.URL.Query()
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
			`<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">` +
			`<adtcore:objectReference adtcore:uri="/sap/bc/adt/oo/classes/zcl_order" adtcore:type="CLAS/OC" adtcore:name="ZCL_ORDER"/>` +
			`<adtcore:objectReference adtcore:uri="/sap/bc/adt/oo/classes/zcl_order_item" adtcore:type="CLAS/OC" adtcore:name="ZCL_ORDER_ITEM"/>` +
			`</adtcore:objectReferences>`))
	}))
	defer ts.Close()
	server := NewServer(&Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN"})

	res, err := server.handleUniversalTool(context.Background(), newRequest(map[string]any{
		"action": "search", "target": "zcl_order",
		"params": map[string]any{"exact": true, "type": "CLAS", "max": float64(5)},
	}))
	if err != nil || res.IsError {
		t.Fatalf("search failed: %v %+v", err, res)
	}
	text := resultText(res)
	if !strings.Contains(text, `"ZCL_ORDER"`) || strings.Contains(text, "ZCL_ORDER_ITEM") {
		t.Fatalf("want only ZCL_ORDER, got %s", text)
	}
	if searchQuery.Get("objectType") != "CLAS/OC" {
		t.Fatalf("type filter not sent: %v", searchQuery)
	}

	res, _ = server.handleUniversalTool(context.Background(), newRequest(map[string]any{
		"action": "search", "target": "ZCL_*", "params": map[string]any{"exact": true},
	}))
	if !res.IsError || !strings.Contains(resultText(res), "not a pattern") {
		t.Fatalf("exact with a wildcard must be refused, got %s", resultText(res))
	}
}

// A full search window is said next to the exact hits, not swallowed: more
// objects of that name may lie beyond it.
func TestRouteSearchExactFullWindowSaysIncomplete(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sb strings.Builder
		sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">`)
		sb.WriteString(`<adtcore:objectReference adtcore:uri="/sap/bc/adt/oo/classes/zcl_order" adtcore:type="CLAS/OC" adtcore:name="ZCL_ORDER"/>`)
		for i := 1; i < 1000; i++ {
			fmt.Fprintf(&sb, `<adtcore:objectReference adtcore:uri="/sap/bc/adt/oo/classes/zcl_order_%04d" adtcore:type="CLAS/OC" adtcore:name="ZCL_ORDER_%04d"/>`, i, i)
		}
		sb.WriteString(`</adtcore:objectReferences>`)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(sb.String()))
	}))
	defer ts.Close()
	server := NewServer(&Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN"})

	res, err := server.handleUniversalTool(context.Background(), newRequest(map[string]any{
		"action": "search", "target": "ZCL_ORDER", "params": map[string]any{"exact": true},
	}))
	if err != nil || res.IsError {
		t.Fatalf("search failed: %v %+v", err, res)
	}
	text := resultText(res)
	if !strings.Contains(text, `"results"`) || !strings.Contains(text, `"ZCL_ORDER"`) ||
		!strings.Contains(text, `"incomplete"`) || !strings.Contains(text, "pass type") {
		t.Fatalf("want the hit and an incomplete note, got %s", text)
	}
}
