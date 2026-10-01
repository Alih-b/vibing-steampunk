package adt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchObjectExact(t *testing.T) {
	var gotQuery, gotType, gotMax string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		gotType = r.URL.Query().Get("objectType")
		gotMax = r.URL.Query().Get("maxResults")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">` +
			`<adtcore:objectReference adtcore:uri="/sap/bc/adt/oo/classes/zcl_order_helper" adtcore:type="CLAS/OC" adtcore:name="ZCL_ORDER_HELPER"/>` +
			`<adtcore:objectReference adtcore:uri="/sap/bc/adt/oo/classes/zcl_order" adtcore:type="CLAS/OC" adtcore:name="ZCL_ORDER"/>` +
			`<adtcore:objectReference adtcore:uri="/sap/bc/adt/programs/programs/zcl_order" adtcore:type="PROG/P" adtcore:name="zcl_order"/>` +
			`<adtcore:objectReference adtcore:uri="/sap/bc/adt/oo/classes/zcl_orders" adtcore:type="CLAS/OC" adtcore:name="ZCL_ORDERS"/>` +
			`</adtcore:objectReferences>`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "u", "p")

	got, incomplete, err := c.SearchObjectExact(context.Background(), "zcl_order", "CLAS", 0)
	if err != nil || incomplete != "" {
		t.Fatal(err, incomplete)
	}
	if len(got) != 2 || got[0].Name != "ZCL_ORDER" || got[1].Name != "zcl_order" {
		t.Fatalf("want only the two objects named ZCL_ORDER, got %+v", got)
	}
	// The name goes as it is: no wildcard is added.
	if gotQuery != "zcl_order" || gotType != "CLAS/OC" || gotMax != "1000" {
		t.Fatalf("query=%q type=%q max=%q", gotQuery, gotType, gotMax)
	}

	got, _, err = c.SearchObjectExact(context.Background(), "ZCL_ORDER", "", 1)
	if err != nil || len(got) != 1 || got[0].Name != "ZCL_ORDER" {
		t.Fatalf("max 1: got %+v, %v", got, err)
	}

	if _, _, err = c.SearchObjectExact(context.Background(), "ZCL_*", "", 0); err == nil || !strings.Contains(err.Error(), "not a pattern") {
		t.Fatalf("a pattern must be refused, got %v", err)
	}
}

// A release that reads a bare name as a prefix fills the 1000-hit window
// with longer names. A full window cannot tell "absent" from "ranked beyond
// the window": without an equal name it is inconclusive, and with some it
// may be incomplete. Either way it must say so.
func TestSearchObjectExactFullWindow(t *testing.T) {
	hits, equal := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sb strings.Builder
		sb.WriteString(`<?xml version="1.0" encoding="utf-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">`)
		for i := 0; i < equal; i++ {
			fmt.Fprintf(&sb, `<adtcore:objectReference adtcore:uri="/sap/bc/adt/x/%d" adtcore:type="PROG/P" adtcore:name="ZCL_X"/>`, i)
		}
		for i := equal; i < hits; i++ {
			fmt.Fprintf(&sb, `<adtcore:objectReference adtcore:uri="/sap/bc/adt/oo/classes/zcl_x_%04d" adtcore:type="CLAS/OC" adtcore:name="ZCL_X_%04d"/>`, i, i)
		}
		sb.WriteString(`</adtcore:objectReferences>`)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(sb.String()))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "u", "p")
	ctx := context.Background()

	hits, equal = exactSearchFetch, 0
	_, _, err := c.SearchObjectExact(ctx, "ZCL_X", "", 0)
	if !errors.Is(err, ErrExactSearchWindowFull) ||
		!strings.Contains(err.Error(), "not found within the first 1000 matches; pass type to narrow") {
		t.Fatalf("full window, no exact hit: got %v", err)
	}
	if _, _, err = c.SearchObjectExact(ctx, "ZCL_X", "CLAS", 0); err == nil || strings.Contains(err.Error(), "pass type") {
		t.Fatalf("with a type already given, the advice must not be to pass one: %v", err)
	}

	// Full window with an exact hit, no limit: the hit, and the warning.
	hits, equal = exactSearchFetch, 1
	got, incomplete, err := c.SearchObjectExact(ctx, "ZCL_X", "", 0)
	if err != nil || len(got) != 1 || !strings.Contains(incomplete, "may be incomplete") || !strings.Contains(incomplete, "pass type") {
		t.Fatalf("full window with a hit: got %+v, %q, %v", got, incomplete, err)
	}
	// A limit the hits already fill has nothing missing.
	got, incomplete, err = c.SearchObjectExact(ctx, "ZCL_X", "", 1)
	if err != nil || len(got) != 1 || incomplete != "" {
		t.Fatalf("full window, limit reached: got %+v, %q, %v", got, incomplete, err)
	}
	// A limit it does not fill is still short of what may be there.
	hits, equal = exactSearchFetch, 2
	got, incomplete, err = c.SearchObjectExact(ctx, "ZCL_X", "", 5)
	if err != nil || len(got) != 2 || incomplete == "" {
		t.Fatalf("full window, limit not reached: got %+v, %q, %v", got, incomplete, err)
	}

	hits, equal = exactSearchFetch-1, 0
	got, incomplete, err = c.SearchObjectExact(ctx, "ZCL_X", "", 0)
	if err != nil || len(got) != 0 || incomplete != "" {
		t.Fatalf("a window that is not full is conclusive: got %+v, %q, %v", got, incomplete, err)
	}
	hits, equal = exactSearchFetch-1, 1
	got, incomplete, err = c.SearchObjectExact(ctx, "ZCL_X", "", 0)
	if err != nil || len(got) != 1 || incomplete != "" {
		t.Fatalf("a window that is not full is complete: got %+v, %q, %v", got, incomplete, err)
	}
}
