package adt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	stampOld = "v2:REPOSRC.REPOTEXT.D020S:20261002101500:1:0123456789abcdef"
	stampNew = "v2:REPOSRC.REPOTEXT.D020S:20261002103000:2:0123456789abcdef"
	shaOld   = "1111111111111111111111111111111111111111111111111111111111111111"
	shaNew   = "2222222222222222222222222222222222222222222222222222222222222222"

	clasStamp = "v2:REPOSRC.REPOTEXT.SEOCLASSDF.SEOCLASSTX.SEOCOMPOTX:20261002101500:7:0123456789abcdef"
)

// versionsWS is fakeGitWS plus object_versions: each object's answer, and,
// for every object_versions call, the ADT calls that had reached the system
// by then -- so a test sees whether the version was read under the lock.
type versionsWS struct {
	*fakeGitWS
	rec *adtRecorder
	// versions answers per "TYPE NAME"; an object missing is answered not
	// in the package.
	versions map[string]map[string]any
	// err fails object_versions without an answer.
	err error

	vmu  sync.Mutex
	seen []versionsCall
}

type versionsCall struct {
	objects string
	sha     bool
	adt     []wireCall
}

func (v *versionsWS) SendDomainRequest(ctx context.Context, domain, action string, params map[string]any, timeout time.Duration) (*WSResponse, error) {
	if action != "object_versions" {
		return v.fakeGitWS.SendDomainRequest(ctx, domain, action, params, timeout)
	}
	objs, _ := params["objects"].(string)
	v.vmu.Lock()
	v.seen = append(v.seen, versionsCall{objects: objs, sha: params["sha256"] == "true", adt: v.rec.snapshot()})
	v.vmu.Unlock()
	if v.err != nil {
		return nil, v.err
	}
	var out []any
	for _, o := range strings.Split(objs, ",") {
		typ, name, _ := strings.Cut(o, " ")
		a, ok := v.versions[o]
		if !ok {
			a = map[string]any{"devclass": "$ZOTHER", "in_package": false}
		}
		m := map[string]any{"type": typ, "name": name, "devclass": "$ZDEMO", "in_package": true, "inactive": false}
		for k, x := range a {
			m[k] = x
		}
		if m["inactive"] == "absent" { // a ZADT_VSP that predates the field
			delete(m, "inactive")
		}
		out = append(out, m)
	}
	return gitOK(map[string]any{"package": params["package"], "objects": out})
}

func (v *versionsWS) calls() []versionsCall {
	v.vmu.Lock()
	defer v.vmu.Unlock()
	return append([]versionsCall(nil), v.seen...)
}

func gitOK(v any) (*WSResponse, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &WSResponse{Success: true, Data: raw}, nil
}

// expectFixture: three programs of $ZDEMO, an offline repository, and the
// ADT side of their deletes.
func expectFixture(t *testing.T, versions map[string]map[string]any) (*Client, *adtRecorder, *versionsWS, map[string]string) {
	t.Helper()
	uris := map[string]string{
		"ZDEMO_A": "/sap/bc/adt/programs/programs/zdemo_a",
		"ZDEMO_B": "/sap/bc/adt/programs/programs/zdemo_b",
		"ZDEMO_C": "/sap/bc/adt/programs/programs/zdemo_c",
		"$ZDEMO":  "/sap/bc/adt/packages/%24zdemo",
	}
	pkgOf := map[string]string{"ZDEMO_A": "$ZDEMO", "ZDEMO_B": "$ZDEMO", "ZDEMO_C": "$ZDEMO", "$ZDEMO": "$ZDEMO"}
	rec := &adtRecorder{}
	cl := newStubbedClient(t, rec, gitDeleteRoute(pkgOf, uris, nil), WithAllowedPackages("$ZDEMO"))
	all := [][2]string{{"PROG", "ZDEMO_A"}, {"PROG", "ZDEMO_B"}, {"PROG", "ZDEMO_C"}}
	ws := &versionsWS{fakeGitWS: &fakeGitWS{contents: []map[string]any{
		pkgContents("$ZDEMO", all, nil, true),
		pkgContents("$ZDEMO", nil, nil, true),
		pkgContents("$ZDEMO", nil, nil, false),
	}}, rec: rec, versions: versions}
	return cl, rec, ws, uris
}

// callsOn is the ADT calls on one object: LOCK, DELETE, UNLOCK, in order.
func callsOn(calls []wireCall, uri string) string {
	var out []string
	for _, c := range calls {
		if !strings.EqualFold(c.path, uri) {
			continue
		}
		switch {
		case c.method == http.MethodDelete:
			out = append(out, "DELETE")
		case c.query.Get("_action") != "":
			out = append(out, c.query.Get("_action"))
		}
	}
	return strings.Join(out, ",")
}

// An object with an expect is deleted only while it is still that version,
// read while the ADT lock is held; one that changed is kept, says what it is
// now, and keeps the repository and the package with it.
func TestDeleteGitObjectsExpectChecksUnderTheLock(t *testing.T) {
	cl, rec, ws, uris := expectFixture(t, map[string]map[string]any{
		"PROG ZDEMO_A": {"stamp": stampOld},
		"PROG ZDEMO_B": {"stamp": stampNew},
	})
	res, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZDEMO", []GitDeleteItem{
		{Type: "PROG", Name: "ZDEMO_A", Expect: &GitExpect{Stamp: stampOld}},
		{Type: "PROG", Name: "ZDEMO_B", Expect: &GitExpect{Stamp: stampOld}},
		{Type: "PROG", Name: "ZDEMO_C"},
	}, GitDeleteOptions{DeleteRepo: true})
	if err == nil || !strings.Contains(err.Error(), "ZDEMO_B was kept") {
		t.Errorf("err %v; want ZDEMO_B kept", err)
	}
	if res == nil {
		t.Fatal("no result")
	}
	got := map[string]GitDeleteOutcome{}
	for _, o := range res.Objects {
		got[o.Name] = o
	}
	if got["ZDEMO_A"].Status != "deleted" || got["ZDEMO_C"].Status != "deleted" {
		t.Errorf("A %s, C %s: want deleted", got["ZDEMO_A"].Status, got["ZDEMO_C"].Status)
	}
	b := got["ZDEMO_B"]
	if b.Status != "changed" || b.Observed == nil || b.Observed.Stamp != stampNew || !strings.Contains(b.Reason, stampNew) {
		t.Errorf("B: %+v (observed %+v); want changed, observed %s", b, b.Observed, stampNew)
	}
	calls := rec.snapshot()
	if d := deletedPaths(calls); len(d) != 2 || d[0] != uris["ZDEMO_A"] || d[1] != uris["ZDEMO_C"] {
		t.Errorf("DELETEs %v; want A and C only", d)
	}
	if s := callsOn(calls, uris["ZDEMO_B"]); s != "LOCK,UNLOCK" {
		t.Errorf("B: %s; want LOCK,UNLOCK", s)
	}
	if s := callsOn(calls, uris["ZDEMO_A"]); s != "LOCK,DELETE,UNLOCK" {
		t.Errorf("A: %s; want LOCK,DELETE,UNLOCK", s)
	}
	// Each version was read with its object locked, and before its DELETE.
	vc := ws.calls()
	if len(vc) != 2 {
		t.Fatalf("object_versions calls %d; want one per expect (2)", len(vc))
	}
	for i, name := range []string{"ZDEMO_A", "ZDEMO_B"} {
		if vc[i].objects != "PROG "+name {
			t.Errorf("versions call %d for %q", i, vc[i].objects)
		}
		if s := callsOn(vc[i].adt, uris[name]); s != "LOCK" {
			t.Errorf("%s: version read after %q; want right after its LOCK", name, s)
		}
	}
	if res.RepoDeleted || res.PackageDeleted || !strings.HasPrefix(res.RepoNote, "kept") || !strings.HasPrefix(res.PackageNote, "kept") {
		t.Errorf("repo %t %q, package %t %q: a changed object keeps both", res.RepoDeleted, res.RepoNote, res.PackageDeleted, res.PackageNote)
	}
	if p := ws.params("delete_repo"); p != nil {
		t.Errorf("delete_repo sent: %v", p)
	}
}

// Never a delete on an uncertain comparison: a version that cannot be read,
// is missing, or is of an object no longer in the package. Either expected
// value matching is enough; the sha256 is read only when one is expected.
func TestDeleteGitObjectsExpectNeverOnUncertainty(t *testing.T) {
	cases := []struct {
		name    string
		answer  map[string]any // nil: not in the package
		wsErr   error
		expect  GitExpect
		status  string
		wantSHA bool
	}{
		{"stamp unreadable", map[string]any{"stamp": "", "stamp_error": "no stamp for type X"}, nil, GitExpect{Stamp: stampOld}, "failed", false},
		{"versions not answered", map[string]any{}, errors.New("not connected"), GitExpect{Stamp: stampOld}, "failed", false},
		{"moved away", nil, nil, GitExpect{Stamp: stampOld}, "failed", false},
		{"no version rows", map[string]any{"stamp": ""}, nil, GitExpect{Stamp: stampOld}, "changed", false},
		{"stamp unreadable, sha matches", map[string]any{"stamp": "", "stamp_error": "x", "sha256": shaOld}, nil, GitExpect{Stamp: stampOld, SHA256: shaOld}, "deleted", true},
		{"stamp matches, sha unreadable", map[string]any{"stamp": stampOld, "sha256_error": "abapGit could not serialise it"}, nil,
			GitExpect{Stamp: stampOld, SHA256: shaOld}, "failed", true},
		{"stamp differs, sha matches", map[string]any{"stamp": stampNew, "sha256": shaOld}, nil, GitExpect{Stamp: stampOld, SHA256: shaOld}, "deleted", true},
		// sha256 decides: a stamp that missed the change never outweighs it.
		{"stamp matches, sha differs", map[string]any{"stamp": stampOld, "sha256": shaNew}, nil, GitExpect{Stamp: stampOld, SHA256: shaOld}, "changed", true},
		// sha256 sees the active version only: unactivated work is never a match.
		{"sha matches, inactive version", map[string]any{"sha256": shaOld, "inactive": true}, nil, GitExpect{SHA256: shaOld}, "changed", true},
		// A ZADT_VSP that does not say whether it is inactive: unknown, never a match.
		{"sha matches, inactive not reported", map[string]any{"sha256": shaOld, "inactive": "absent"}, nil, GitExpect{SHA256: shaOld}, "failed", true},
		{"stamp and sha match, inactive not reported", map[string]any{"stamp": stampOld, "sha256": shaOld, "inactive": "absent"}, nil, GitExpect{Stamp: stampOld, SHA256: shaOld}, "failed", true},
		{"stamp only, inactive not reported", map[string]any{"stamp": stampOld, "inactive": "absent"}, nil, GitExpect{Stamp: stampOld}, "deleted", false},
		{"stamp and sha match, inactive version", map[string]any{"stamp": stampOld, "sha256": shaOld, "inactive": true}, nil, GitExpect{Stamp: stampOld, SHA256: shaOld}, "changed", true},
		{"sha only, matches", map[string]any{"sha256": strings.ToUpper(shaOld)}, nil, GitExpect{SHA256: shaOld}, "deleted", true},
		{"sha only, differs", map[string]any{"sha256": shaNew}, nil, GitExpect{SHA256: shaOld}, "changed", true},
		{"sha only, missing", map[string]any{"sha256": ""}, nil, GitExpect{SHA256: shaOld}, "changed", true},
	}
	for _, c := range cases {
		versions := map[string]map[string]any{}
		if c.answer != nil {
			versions["PROG ZDEMO_A"] = c.answer
		}
		cl, rec, ws, uris := expectFixture(t, versions)
		ws.err = c.wsErr
		exp := c.expect
		res, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZDEMO",
			[]GitDeleteItem{{Type: "PROG", Name: "ZDEMO_A", Expect: &exp}}, GitDeleteOptions{})
		if res == nil || len(res.Objects) != 1 {
			t.Fatalf("%s: %+v %v", c.name, res, err)
		}
		if got := res.Objects[0].Status; got != c.status {
			t.Errorf("%s: %s (%s); want %s", c.name, got, res.Objects[0].Reason, c.status)
		}
		if strings.Contains(c.name, "inactive not reported") && c.status == "failed" && !strings.Contains(res.Objects[0].Reason, "too old") {
			t.Errorf("%s: reason %q; want ZADT_VSP too old", c.name, res.Objects[0].Reason)
		}
		deleted := len(deletedPaths(rec.snapshot())) > 0
		if deleted != (c.status == "deleted") {
			t.Errorf("%s: deleted %t with status %s", c.name, deleted, c.status)
		}
		if c.status != "deleted" && (err == nil || res.PackageDeleted) {
			t.Errorf("%s: err %v, package deleted %t", c.name, err, res.PackageDeleted)
		}
		if s := callsOn(rec.snapshot(), uris["ZDEMO_A"]); c.status != "deleted" && strings.Contains(s, "DELETE") {
			t.Errorf("%s: %s", c.name, s)
		}
		for _, v := range ws.calls() {
			if v.sha != c.wantSHA {
				t.Errorf("%s: sha256 asked %t; want %t", c.name, v.sha, c.wantSHA)
			}
		}
	}
}

// Expectations are checked before anything is read or sent: one that cannot
// be checked refuses the whole call, and the gates still come first.
func TestDeleteGitObjectsExpectRefusedBeforeIO(t *testing.T) {
	bad := []GitExpect{
		{},
		{Stamp: "20261002"},
		{Stamp: "v2:DD02L.DD09L.DD12L.DD02T.DD35L.TDDAT:20261002101500:1:0123456789abcdef"}, // a table's stamp for a program
		{Stamp: "v1:REPOSRC:20261002101500:1"},                                              // a v1 stamp covered less
		{SHA256: "abc"},
	}
	for _, e := range bad {
		cl, rec, ws, _ := expectFixture(t, nil)
		exp := e
		_, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_A", Expect: &exp}}, GitDeleteOptions{})
		if err == nil || len(ws.actions()) != 0 || len(rec.snapshot()) != 0 {
			t.Errorf("%+v: err %v, git %v, adt %d", e, err, ws.actions(), len(rec.snapshot()))
		}
	}
	// A stamp for a type that has none: refused, not ignored.
	cl, _, ws, _ := expectFixture(t, nil)
	_, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "FUGR", Name: "ZDEMO_FG", Expect: &GitExpect{Stamp: stampOld}}}, GitDeleteOptions{})
	if err == nil || !strings.Contains(err.Error(), "has no stamp") || len(ws.actions()) != 0 {
		t.Errorf("FUGR stamp: %v %v", err, ws.actions())
	}
	// Read-only refuses first.
	ro := NewClient("http://sap.invalid", "TESTUSER", "pw", WithReadOnly())
	ws2 := &versionsWS{fakeGitWS: &fakeGitWS{}, rec: &adtRecorder{}}
	if _, err := ro.DeleteGitObjectsWith(context.Background(), ws2, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_A", Expect: &GitExpect{Stamp: stampOld}}}, GitDeleteOptions{}); err == nil || len(ws2.actions()) != 0 || len(ws2.calls()) != 0 {
		t.Errorf("read-only: %v", err)
	}
	// expect_repo only with delete_repo, and with key and name.
	for _, o := range []GitDeleteOptions{
		{ExpectRepo: &GitRepoExpect{Key: "000000000001", Name: "demo"}},
		{DeleteRepo: true, ExpectRepo: &GitRepoExpect{Key: "000000000001"}},
	} {
		cl, _, ws, _ := expectFixture(t, nil)
		if _, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_A"}}, o); err == nil || len(ws.actions()) != 0 {
			t.Errorf("%+v: %v %v", o.ExpectRepo, err, ws.actions())
		}
	}
}

// expect_repo: the repository row is dropped only when it is exactly the
// one expected, and ZADT_VSP is asked to check the same.
func TestDeleteGitObjectsExpectRepo(t *testing.T) {
	run := func(exp *GitRepoExpect, repoErr *WSError) (*GitDeleteResult, *versionsWS) {
		cl, _, ws, _ := expectFixture(t, nil)
		ws.repoErr = repoErr
		res, err := cl.DeleteGitObjectsWith(context.Background(), ws, "$ZDEMO", []GitDeleteItem{
			{Type: "PROG", Name: "ZDEMO_A"}, {Type: "PROG", Name: "ZDEMO_B"}, {Type: "PROG", Name: "ZDEMO_C"},
		}, GitDeleteOptions{DeleteRepo: true, ExpectRepo: exp})
		if err != nil {
			t.Fatalf("%+v: %v", exp, err)
		}
		return res, ws
	}
	for _, other := range []GitRepoExpect{{Key: "000000000001", Name: "other"}, {Key: "000000000009", Name: "demo"}} {
		o := other
		res, ws := run(&o, nil)
		if res.RepoDeleted || res.RepoNote != "kept: registered repository is 000000000001 demo" || res.PackageDeleted {
			t.Errorf("%+v: repo %t %q, package %t", o, res.RepoDeleted, res.RepoNote, res.PackageDeleted)
		}
		if p := ws.params("delete_repo"); p != nil {
			t.Errorf("%+v: delete_repo sent %v", o, p)
		}
	}
	res, ws := run(&GitRepoExpect{Key: "000000000001", Name: "demo"}, nil)
	p := ws.params("delete_repo")
	if !res.RepoDeleted || p == nil || p["key"] != "000000000001" || p["name"] != "demo" {
		t.Errorf("the expected repository: deleted %t, params %v", res.RepoDeleted, p)
	}
	// ZADT_VSP sees another row in its own step: kept, and the package too.
	res, _ = run(&GitRepoExpect{Key: "000000000001", Name: "demo"}, &WSError{Code: "REPO_NAME_MISMATCH", Message: "The repository of $ZDEMO is 000000000001 renamed"})
	if res.RepoDeleted || !strings.HasPrefix(res.RepoNote, "kept: registered repository") || res.PackageDeleted {
		t.Errorf("ZADT_VSP mismatch: repo %t %q, package %t", res.RepoDeleted, res.RepoNote, res.PackageDeleted)
	}
}

func TestParseGitDeleteItemsExpect(t *testing.T) {
	items, err := ParseGitDeleteItems([]any{
		map[string]any{"type": "clas", "name": "zcl_demo", "expect": map[string]any{"stamp": clasStamp, "sha256": strings.ToUpper(shaOld)}},
		map[string]any{"type": "TABL", "name": "ZDEMO_T", "expect": map[string]any{"stamp": "v2:DD02L.DD09L.DD12L.DD02T.DD35L.TDDAT:20261002101500:1:0123456789abcdef"}},
		map[string]any{"type": "FUGR", "name": "ZDEMO_FG", "expect": map[string]any{"sha256": shaOld}},
		"PROG ZDEMO_REPORT",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e := items[0].Expect; e == nil || e.Stamp != clasStamp || e.SHA256 != shaOld {
		t.Errorf("CLAS expect %+v", e)
	}
	if items[1].Expect == nil || items[2].Expect == nil || items[3].Expect != nil {
		t.Errorf("items %+v", items)
	}
	for name, bad := range map[string]any{
		"unknown item key":     map[string]any{"type": "PROG", "name": "A", "expected": map[string]any{"stamp": stampOld}},
		"unknown expect key":   map[string]any{"type": "PROG", "name": "A", "expect": map[string]any{"stamp": stampOld, "md5": "x"}},
		"empty expect":         map[string]any{"type": "PROG", "name": "A", "expect": map[string]any{}},
		"expect not a map":     map[string]any{"type": "PROG", "name": "A", "expect": stampOld},
		"stamp not a string":   map[string]any{"type": "PROG", "name": "A", "expect": map[string]any{"stamp": 1.0}},
		"stamp malformed":      map[string]any{"type": "PROG", "name": "A", "expect": map[string]any{"stamp": "v3:REPOSRC:20261002101500:1"}},
		"stamp v1":             map[string]any{"type": "PROG", "name": "A", "expect": map[string]any{"stamp": "v1:REPOSRC:20261002101500:1"}},
		"stamp of other table": map[string]any{"type": "DTEL", "name": "A", "expect": map[string]any{"stamp": stampOld}},
		"stamp unsupported":    map[string]any{"type": "FUGR", "name": "A", "expect": map[string]any{"stamp": stampOld}},
		"sha256 malformed":     map[string]any{"type": "PROG", "name": "A", "expect": map[string]any{"sha256": "xyz"}},
	} {
		if _, err := ParseGitDeleteItems([]any{bad}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// One object twice: the same expectation, or refused.
	if _, err := ParseGitDeleteItems([]any{
		map[string]any{"type": "PROG", "name": "A", "expect": map[string]any{"stamp": stampOld}}, "PROG A",
	}); err == nil {
		t.Error("the same object with and without expect: accepted")
	}
	if items, err := ParseGitDeleteItems([]any{
		map[string]any{"type": "PROG", "name": "A", "expect": map[string]any{"stamp": stampOld}},
		map[string]any{"type": "PROG", "name": "A", "expect": map[string]any{"stamp": stampOld}},
	}); err != nil || len(items) != 1 || items[0].Expect == nil {
		t.Errorf("the same object twice, same expect: %+v %v", items, err)
	}
}

func TestApplyGitExpectFlags(t *testing.T) {
	items := []GitDeleteItem{{Type: "CLAS", Name: "ZCL_DEMO"}, {Type: "PROG", Name: "ZDEMO"}}
	if err := ApplyGitExpectFlags(items, []string{"clas zcl_demo stamp=" + clasStamp + " sha256=" + shaOld}); err != nil {
		t.Fatal(err)
	}
	if e := items[0].Expect; e == nil || e.Stamp != clasStamp || e.SHA256 != shaOld || items[1].Expect != nil {
		t.Errorf("items %+v", items)
	}
	for _, bad := range []string{
		"PROG ZOTHER stamp=" + stampOld,             // not among the objects
		"PROG ZDEMO",                                // no value
		"PROG ZDEMO stamp",                          // no =
		"PROG ZDEMO md5=" + shaOld,                  // unknown key
		"PROG ZDEMO stamp=" + stampOld + " stamp=x", // twice
		"CLAS ZCL_DEMO sha256=" + shaNew,            // another expect already
	} {
		if err := ApplyGitExpectFlags(items, []string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// GitObjectVersions sends the objects as ZADT_VSP reads them, asks for the
// sha256 only when wanted, and refuses an answer it cannot match up.
func TestGitObjectVersions(t *testing.T) {
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw", WithReadOnly())
	ws := &versionsWS{fakeGitWS: &fakeGitWS{}, rec: &adtRecorder{}, versions: map[string]map[string]any{
		"CLAS ZCL_DEMO": {"stamp": clasStamp, "sha256": shaOld, "files": 2, "inactive": true},
	}}
	vs, err := cl.GitObjectVersions(context.Background(), ws, "$zdemo", []GitDeleteItem{{Type: "CLAS", Name: "ZCL_DEMO"}, {Type: "PROG", Name: "ZGONE"}}, true)
	if err != nil {
		t.Fatal(err) // read-only does not refuse a read
	}
	if len(vs) != 2 || vs[0].Stamp != clasStamp || vs[0].Inactive == nil || !*vs[0].Inactive || vs[1].Inactive == nil || *vs[1].Inactive || vs[0].SHA256 != shaOld || !vs[0].InPackage || vs[1].InPackage || vs[1].Package != "$ZOTHER" {
		t.Errorf("versions %+v", vs)
	}
	if c := ws.calls(); len(c) != 1 || c[0].objects != "CLAS ZCL_DEMO,PROG ZGONE" || !c[0].sha {
		t.Errorf("calls %+v", c)
	}
	// An answer for another object, or a missing one, is an error.
	wrong := &fakeAnswerWS{answer: map[string]any{"objects": []any{map[string]any{"type": "PROG", "name": "ZOTHER", "in_package": true, "devclass": "$ZDEMO", "stamp": stampOld}}}}
	if _, err := cl.GitObjectVersions(context.Background(), wrong, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO"}}, false); err == nil {
		t.Error("an answer for another object accepted")
	}
	short := &fakeAnswerWS{answer: map[string]any{"objects": []any{}}}
	if _, err := cl.GitObjectVersions(context.Background(), short, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO"}}, false); err == nil {
		t.Error("a missing answer accepted")
	}
	foreign := &fakeAnswerWS{answer: map[string]any{"objects": []any{map[string]any{"type": "PROG", "name": "ZDEMO", "in_package": true, "devclass": "$ZOTHER", "stamp": stampOld}}}}
	if _, err := cl.GitObjectVersions(context.Background(), foreign, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO"}}, false); err == nil {
		t.Error("in_package for another package accepted")
	}
	if _, err := cl.GitObjectVersions(context.Background(), short, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "Z,DEMO"}}, false); err == nil {
		t.Error("a name with a comma accepted")
	}
}

type fakeAnswerWS struct{ answer map[string]any }

func (f *fakeAnswerWS) SendDomainRequest(context.Context, string, string, map[string]any, time.Duration) (*WSResponse, error) {
	return gitOK(f.answer)
}
