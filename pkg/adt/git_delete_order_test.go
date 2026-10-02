package adt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// orderObj is one object of the order fixture: its TADIR type, the ADT type
// the search reports for it, and its ADT URI.
type orderObj struct{ typ, adtType, uri string }

var orderObjs = map[string]orderObj{
	"ZORD_PROG":   {"PROG", "PROG/P", "/sap/bc/adt/programs/programs/zord_prog"},
	"ZORD_PROG2":  {"PROG", "PROG/P", "/sap/bc/adt/programs/programs/zord_prog2"},
	"ZCL_ORD":     {"CLAS", "CLAS/OC", "/sap/bc/adt/oo/classes/zcl_ord"},
	"ZORD_SB":     {"SRVB", "SRVB/SVB", "/sap/bc/adt/businessservices/bindings/zord_sb"},
	"ZORD_SD":     {"SRVD", "SRVD/SRV", "/sap/bc/adt/ddic/srvd/sources/zord_sd"},
	"ZORD_BDEF":   {"BDEF", "BDEF/BDO", "/sap/bc/adt/bo/behaviordefinitions/zord_bdef"},
	"ZORD_CDS":    {"DDLS", "DDLS/DF", "/sap/bc/adt/ddic/ddl/sources/zord_cds"},
	"ZORD_TT":     {"TTYP", "TTYP/DA", "/sap/bc/adt/ddic/tabletypes/zord_tt"},
	"ZORD_TAB":    {"TABL", "TABL/DT", "/sap/bc/adt/ddic/tables/zord_tab"},
	"ZORD_TAB2":   {"TABL", "TABL/DT", "/sap/bc/adt/ddic/tables/zord_tab2"},
	"ZORD_DTEL":   {"DTEL", "DTEL/DE", "/sap/bc/adt/ddic/dataelements/zord_dtel"},
	"ZORD_DTEL2":  {"DTEL", "DTEL/DE", "/sap/bc/adt/ddic/dataelements/zord_dtel2"},
	"ZORD_DOMA":   {"DOMA", "DOMA/DD", "/sap/bc/adt/ddic/domains/zord_doma"},
	"$ZORD":       {"DEVC", "DEVC/K", "/sap/bc/adt/packages/$zord"}, // as the server sees it
	"ZORD_UNUSED": {"PROG", "PROG/P", "/sap/bc/adt/programs/programs/zord_unused"},
}

// orderRoute answers the ADT side of the deletes as SAP does: the exact
// search gitDeleteURL runs (every object in $ZORD, at its own URI), LOCK,
// DELETE, UNLOCK.
func orderRoute(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.Contains(r.URL.Path, "informationsystem/search"):
		q := strings.ToUpper(strings.Trim(r.URL.Query().Get("query"), "*"))
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">`)
		if o, ok := orderObjs[q]; ok && searchTypeMatches(r, o.adtType) {
			fmt.Fprintf(&b, `<adtcore:objectReference adtcore:uri="%s" adtcore:type="%s" adtcore:name="%s" adtcore:packageName="$ZORD"/>`, o.uri, o.adtType, q)
		}
		b.WriteString(`</adtcore:objectReferences>`)
		_, _ = io.WriteString(w, b.String())
	case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
		w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
		_, _ = io.WriteString(w, testLockXML)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

// orderFixture: a client and a ZADT_VSP fake for $ZORD holding names; the
// package is empty once they are deleted.
func orderFixture(t *testing.T, names ...string) (*Client, *adtRecorder, *fakeGitWS) {
	t.Helper()
	rec := &adtRecorder{}
	cl := newStubbedClient(t, rec, orderRoute, WithAllowedPackages("$ZORD"))
	var objs [][2]string
	for _, n := range names {
		objs = append(objs, [2]string{orderObjs[n].typ, n})
	}
	ws := &fakeGitWS{contents: []map[string]any{
		pkgContents("$ZORD", objs, nil, false),
		pkgContents("$ZORD", nil, nil, false),
	}}
	return cl, rec, ws
}

func orderItems(names ...string) []GitDeleteItem {
	out := make([]GitDeleteItem, 0, len(names))
	for _, n := range names {
		out = append(out, GitDeleteItem{Type: orderObjs[n].typ, Name: n})
	}
	return out
}

// deletedNames is the objects DELETEd, by name, in the order sent.
func deletedNames(calls []wireCall) []string {
	byURI := map[string]string{}
	for n, o := range orderObjs {
		byURI[strings.ToLower(o.uri)] = n
	}
	var out []string
	for _, p := range deletedPaths(calls) {
		out = append(out, byURI[strings.ToLower(p)])
	}
	return out
}

func typeNames(names ...string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, orderObjs[n].typ+" "+n)
	}
	return out
}

func TestGitDeleteRank(t *testing.T) {
	order := [][]string{
		{"PROG", "CLAS", "INTF", "FUGR", "FUNC", "XSLT", "ENHO", "zzzz"},
		{"SRVB"}, {"SRVD"}, {"BDEF"}, {"DCLS", "DDLX"}, {"DDLS"},
		{"SHLP", "ENQU"}, {"TTYP"}, {"TABL"}, {"DTEL"}, {"DOMA"},
	}
	last := -1
	for _, group := range order {
		r := GitDeleteRank(group[0])
		if r <= last {
			t.Errorf("%s ranks %d, not after %d", group[0], r, last)
		}
		for _, typ := range group[1:] {
			if GitDeleteRank(typ) != r {
				t.Errorf("%s ranks %d, want %d (as %s)", typ, GitDeleteRank(typ), r, group[0])
			}
		}
		last = r
	}
	if GitDeleteRank(" tabl ") != GitDeleteRank("TABL") {
		t.Error("rank depends on case or spaces")
	}
}

// A mixed list is deleted users before what they use: code, RAP (binding,
// definition, behaviour, view), table types, tables, data elements,
// domains -- whatever order it was given in. Objects keeps the order given;
// Order is the order used.
func TestDeleteGitObjectsUsersBeforeUsed(t *testing.T) {
	given := []string{"ZORD_DOMA", "ZORD_TAB", "ZORD_CDS", "ZORD_DTEL", "ZORD_PROG", "ZORD_TT", "ZORD_BDEF", "ZORD_SB", "ZCL_ORD", "ZORD_SD"}
	want := []string{"ZORD_PROG", "ZCL_ORD", "ZORD_SB", "ZORD_SD", "ZORD_BDEF", "ZORD_CDS", "ZORD_TT", "ZORD_TAB", "ZORD_DTEL", "ZORD_DOMA"}
	cl, rec, ws := orderFixture(t, given...)
	res, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZORD", orderItems(given...), GitDeleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The package still goes last, after every object.
	if got := deletedNames(rec.snapshot()); strings.Join(got, ",") != strings.Join(want, ",")+",$ZORD" {
		t.Errorf("DELETEs\n  %v\nwant\n  %v, then $ZORD", got, want)
	}
	if got := strings.Join(res.Order, ","); got != strings.Join(typeNames(want...), ",") {
		t.Errorf("order %s; want %v", got, typeNames(want...))
	}
	for i, o := range res.Objects {
		if o.Name != given[i] || o.Status != "deleted" {
			t.Errorf("objects[%d] = %s %s; want %s deleted, in the order given", i, o.Name, o.Status, given[i])
		}
	}
	if !res.PackageDeleted {
		t.Errorf("package kept: %s", res.PackageNote)
	}
}

// Within one type, the order given stays: not sorted by name.
func TestDeleteGitObjectsOrderStableWithinARank(t *testing.T) {
	given := []string{"ZORD_TAB2", "ZORD_DTEL2", "ZORD_TAB", "ZORD_PROG2", "ZORD_DTEL", "ZORD_PROG"}
	want := []string{"ZORD_PROG2", "ZORD_PROG", "ZORD_TAB2", "ZORD_TAB", "ZORD_DTEL2", "ZORD_DTEL"}
	cl, rec, ws := orderFixture(t, given...)
	res, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZORD", orderItems(given...), GitDeleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := deletedNames(rec.snapshot()); strings.Join(got, ",") != strings.Join(want, ",")+",$ZORD" {
		t.Errorf("DELETEs %v; want %v, then $ZORD", got, want)
	}
	if got := strings.Join(res.Order, ","); got != strings.Join(typeNames(want...), ",") {
		t.Errorf("order %s", got)
	}
}

// keep_order: exactly the order given. Objects that are not deleted (not in
// the package, a package as an item) are not in Order.
func TestDeleteGitObjectsKeepOrder(t *testing.T) {
	given := []string{"ZORD_DOMA", "ZORD_DTEL", "ZORD_TAB", "ZORD_PROG"}
	cl, rec, ws := orderFixture(t, given...)
	items := append(orderItems(given...), GitDeleteItem{Type: "PROG", Name: "ZORD_UNUSED"}, GitDeleteItem{Type: "DEVC", Name: "$ZORD"})
	res, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZORD", items, GitDeleteOptions{KeepOrder: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := deletedNames(rec.snapshot()); strings.Join(got[:len(given)], ",") != strings.Join(given, ",") {
		t.Errorf("DELETEs %v; want %v as given", got, given)
	}
	if got := strings.Join(res.Order, ","); got != strings.Join(typeNames(given...), ",") {
		t.Errorf("order %s; want %v", got, typeNames(given...))
	}
}

// dependentWS answers object_versions with the dependency abapGit's
// serialisation has: once what an object uses is deleted, its sha256 is
// another one. It models that dependency; it does not prove that abapGit's
// serialisation of a TABL changes when its DTEL is deleted. The evidence on
// a real system is the live run in the PR (a 7.58: DOMA, DTEL and a
// structure using them, listed used-first, all deleted under their sha256);
// the old order's "changed" was not reproduced there.
type dependentWS struct {
	*fakeGitWS
	rec  *adtRecorder
	uses map[string]string // name -> name of what it uses
}

func (d *dependentWS) SendDomainRequest(ctx context.Context, domain, action string, params map[string]any, timeout time.Duration) (*WSResponse, error) {
	if action != "object_versions" {
		return d.fakeGitWS.SendDomainRequest(ctx, domain, action, params, timeout)
	}
	gone := map[string]bool{}
	for _, n := range deletedNames(d.rec.snapshot()) {
		gone[n] = true
	}
	objs, _ := params["objects"].(string)
	var out []any
	for _, o := range strings.Split(objs, ",") {
		typ, name, _ := strings.Cut(o, " ")
		sha := shaOld
		if u, ok := d.uses[name]; ok && gone[u] {
			sha = shaNew
		}
		out = append(out, map[string]any{"type": typ, "name": name, "devclass": "$ZORD", "in_package": true, "inactive": false, "sha256": sha})
	}
	return gitOK(map[string]any{"package": params["package"], "objects": out})
}

// The sha256s are read before the call, and listed used-first (DOMA, DTEL,
// TABL): in that order the DTEL's delete changes the table's sha256, and
// the table comes back changed and is kept. Users before used deletes all
// three, each still at the sha256 read before the call.
func TestDeleteGitObjectsUsedLastKeepsTheirUsersSHA256(t *testing.T) {
	given := []string{"ZORD_DOMA", "ZORD_DTEL", "ZORD_TAB"}
	items := orderItems(given...)
	for i := range items {
		items[i].Expect = &GitExpect{SHA256: shaOld}
	}
	uses := map[string]string{"ZORD_TAB": "ZORD_DTEL"}

	t.Run("order given", func(t *testing.T) {
		cl, rec, fws := orderFixture(t, given...)
		ws := &dependentWS{fakeGitWS: fws, rec: rec, uses: uses}
		res, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZORD", items, GitDeleteOptions{KeepOrder: true})
		if err == nil || res == nil {
			t.Fatalf("err %v, res %v; want the dependents kept", err, res)
		}
		st := map[string]string{}
		for _, o := range res.Objects {
			st[o.Name] = o.Status
		}
		if st["ZORD_DOMA"] != "deleted" || st["ZORD_DTEL"] != "deleted" || st["ZORD_TAB"] != "changed" {
			t.Errorf("statuses %v; want DOMA and DTEL deleted, TABL changed", st)
		}
	})
	t.Run("users before used", func(t *testing.T) {
		cl, rec, fws := orderFixture(t, given...)
		ws := &dependentWS{fakeGitWS: fws, rec: rec, uses: uses}
		res, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZORD", items, GitDeleteOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range res.Objects {
			if o.Status != "deleted" || o.Observed == nil || o.Observed.SHA256 != shaOld {
				t.Errorf("%s: %s (observed %+v); want deleted at %s", o.Name, o.Status, o.Observed, shaOld)
			}
		}
		if got := deletedNames(rec.snapshot()); strings.Join(got[:3], ",") != "ZORD_TAB,ZORD_DTEL,ZORD_DOMA" {
			t.Errorf("DELETEs %v", got)
		}
		if !res.PackageDeleted {
			t.Errorf("package kept: %s", res.PackageNote)
		}
	})
}

// Order lists every attempt as it was made: an object whose DELETE failed
// once is retried after the others, and is in Order twice.
func TestDeleteGitObjectsOrderListsRetries(t *testing.T) {
	var mu sync.Mutex
	failed := false
	route := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.EqualFold(r.URL.Path, orderObjs["ZORD_DTEL"].uri) {
			mu.Lock()
			first := !failed
			failed = true
			mu.Unlock()
			if first {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "in use")
				return
			}
		}
		orderRoute(w, r)
	}
	rec := &adtRecorder{}
	cl := newStubbedClient(t, rec, route, WithAllowedPackages("$ZORD"))
	ws := &fakeGitWS{contents: []map[string]any{
		pkgContents("$ZORD", [][2]string{{"DTEL", "ZORD_DTEL"}, {"DOMA", "ZORD_DOMA"}}, nil, false),
		pkgContents("$ZORD", nil, nil, false),
	}}
	res, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZORD", orderItems("ZORD_DOMA", "ZORD_DTEL"), GitDeleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := "DTEL ZORD_DTEL,DOMA ZORD_DOMA,DTEL ZORD_DTEL"
	if got := strings.Join(res.Order, ","); got != want {
		t.Errorf("order %s; want %s", got, want)
	}
	if got := strings.Join(deletedNames(rec.snapshot()), ","); got != "ZORD_DTEL,ZORD_DOMA,ZORD_DTEL,$ZORD" {
		t.Errorf("DELETEs %s", got)
	}
	for _, o := range res.Objects {
		if o.Status != "deleted" {
			t.Errorf("%s: %s %s", o.Name, o.Status, o.Reason)
		}
	}
}
