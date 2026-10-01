package adt

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type includeWireCall struct {
	method string
	path   string // escaped, exactly as sent
	action string
	body   string
}

// includeServer answers like ADT for one class whose include writes are
// recorded. A PUT stores the body and a GET of the same path returns it.
type includeServer struct {
	missingTestInclude bool   // the testclasses include does not exist until created
	missingStatus      int    // what a PUT to the missing include answers
	missingBody        string // and with which body
	createFails        bool   // POST .../includes (create the test include) answers 500
	activationBody     string // the activation response; empty means success
	packageName        string // the package the repository search reports
	initial            map[string]string
	opts               []Option
}

// newIncludeWriteServer is the plain server: missingTestInclude makes the
// first PUT to includes/testclasses answer 404 until the include is created.
func newIncludeWriteServer(t *testing.T, missingTestInclude bool) (*Client, func() []includeWireCall) {
	return includeServer{missingTestInclude: missingTestInclude, missingStatus: http.StatusNotFound}.start(t)
}

func newIncludeWriteServerAnswering(t *testing.T, missingTestInclude bool, missingStatus int, missingBody string) (*Client, func() []includeWireCall) {
	return includeServer{missingTestInclude: missingTestInclude, missingStatus: missingStatus, missingBody: missingBody}.start(t)
}

// missingIncludeED170 is what a 7.58 answers a PUT to the testclasses include
// of a class that has none: a 500, not a 404.
const missingIncludeED170 = `<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="ExceptionResourceSaveFailure"/><message lang="EN">ZCL_PROBE=====================CCAU does not have any inactive version</message><properties><entry key="T100KEY-ID">ED</entry><entry key="T100KEY-NO">170</entry><entry key="T100KEY-V1">ZCL_PROBE=====================CCAU</entry></properties></exc:exception>`

func (cfg includeServer) start(t *testing.T) (*Client, func() []includeWireCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []includeWireCall
	testIncludeExists := !cfg.missingTestInclude
	stored := map[string]string{}
	for k, v := range cfg.initial {
		stored[k] = v
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, includeWireCall{
			method: r.Method,
			path:   r.URL.EscapedPath(),
			action: r.URL.Query().Get("_action"),
			body:   string(body),
		})
		w.Header().Set("X-CSRF-Token", "TOKEN")
		isTestInclude := strings.HasSuffix(r.URL.Path, "/includes/testclasses")
		switch {
		case r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>
<LOCK_HANDLE>HANDLE-1</LOCK_HANDLE><IS_LOCAL>X</IS_LOCAL>
</DATA></asx:values></asx:abap>`)
		case r.URL.Query().Get("_action") == "UNLOCK":
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:objectReference adtcore:uri="/sap/bc/adt/oo/classes/zcl_probe" adtcore:type="CLAS/OC" adtcore:name="ZCL_PROBE" adtcore:packageName="`+cfg.packageName+`"/>
</adtcore:objectReferences>`)
		case strings.Contains(r.URL.Path, "/checkruns"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><chkrun:checkRunReports xmlns:chkrun="http://www.sap.com/adt/checkrun"/>`)
		case strings.HasSuffix(r.URL.Path, "/activation"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, cfg.activationBody)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/includes"):
			if cfg.createFails {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, "create refused")
				return
			}
			testIncludeExists = true
			w.WriteHeader(http.StatusOK)
		case isTestInclude && !testIncludeExists:
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(cfg.missingStatus)
			_, _ = io.WriteString(w, cfg.missingBody)
		case r.Method == http.MethodPut:
			stored[r.URL.EscapedPath()] = string(body)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, stored[r.URL.EscapedPath()])
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	snapshot := func() []includeWireCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]includeWireCall(nil), calls...)
	}
	return NewClient(srv.URL, "TESTUSER", "pw", cfg.opts...), snapshot
}

func putPaths(calls []includeWireCall) []string {
	var paths []string
	for _, c := range calls {
		if c.method == http.MethodPut {
			paths = append(paths, c.path)
		}
	}
	return paths
}

// #242: WriteSource with an include must PUT to that include and never to
// the main source.
func TestWriteSourceClassIncludeWritesIncludeNotMain(t *testing.T) {
	for _, include := range []string{"testclasses", "definitions", "implementations", "macros", "TestClasses"} {
		t.Run(include, func(t *testing.T) {
			c, snapshot := newIncludeWriteServer(t, false)
			const src = "CLASS ltcl_probe DEFINITION FOR TESTING. ENDCLASS."
			result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", src, &WriteSourceOptions{Include: include})
			if err != nil {
				t.Fatalf("WriteSource: %v", err)
			}
			if !result.Success {
				t.Fatalf("WriteSource failed: %s", result.Message)
			}
			want := "/sap/bc/adt/oo/classes/ZCL_PROBE/includes/" + strings.ToLower(include)
			puts := putPaths(snapshot())
			if len(puts) != 1 || puts[0] != want {
				t.Fatalf("PUTs = %v, want exactly [%s]", puts, want)
			}
			if result.Include != strings.ToLower(include) {
				t.Errorf("result.Include = %q", result.Include)
			}
			var locked, unlocked, activated bool
			for _, call := range snapshot() {
				switch {
				case call.action == "LOCK":
					locked = call.path == "/sap/bc/adt/oo/classes/ZCL_PROBE"
				case call.action == "UNLOCK":
					unlocked = call.path == "/sap/bc/adt/oo/classes/ZCL_PROBE"
				case strings.HasSuffix(call.path, "/activation"):
					activated = strings.Contains(call.body, `adtcore:uri="/sap/bc/adt/oo/classes/ZCL_PROBE"`)
				}
			}
			if !locked || !unlocked || !activated {
				t.Errorf("want LOCK, UNLOCK and activation on the class URL: lock=%v unlock=%v activate=%v calls=%+v", locked, unlocked, activated, snapshot())
			}
		})
	}
}

func TestWriteSourceClassIncludeMainKeepsMainPath(t *testing.T) {
	for _, include := range []string{"main", "MAIN"} {
		c, snapshot := newIncludeWriteServer(t, false)
		result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", "CLASS zcl_probe DEFINITION PUBLIC. ENDCLASS. CLASS zcl_probe IMPLEMENTATION. ENDCLASS.", &WriteSourceOptions{Include: include, Mode: WriteModeUpdate})
		if err != nil {
			t.Fatalf("WriteSource: %v", err)
		}
		if !result.Success {
			t.Fatalf("WriteSource failed: %s", result.Message)
		}
		puts := putPaths(snapshot())
		if len(puts) != 1 || puts[0] != "/sap/bc/adt/oo/classes/ZCL_PROBE/source/main" {
			t.Fatalf("include=%s PUTs = %v, want the main source", include, puts)
		}
	}
}

func TestWriteSourceClassIncludeRefusesWithoutWriting(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		opts WriteSourceOptions
		want string
	}{
		{"unknown include", "CLAS", WriteSourceOptions{Include: "testclass"}, "unknown class include"},
		{"locals_def is not an ADT include", "CLAS", WriteSourceOptions{Include: "locals_def"}, "unknown class include"},
		{"not a class", "PROG", WriteSourceOptions{Include: "testclasses"}, "only valid for CLAS"},
		{"with method", "CLAS", WriteSourceOptions{Include: "testclasses", Method: "RUN"}, "method and include"},
		{"with test_source", "CLAS", WriteSourceOptions{Include: "testclasses", TestSource: "x"}, "test_source and include"},
		{"create mode", "CLAS", WriteSourceOptions{Include: "testclasses", Mode: WriteModeCreate}, "mode=create"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, snapshot := newIncludeWriteServer(t, false)
			opts := tt.opts
			result, err := c.WriteSource(context.Background(), tt.typ, "ZCL_PROBE", "CLASS ltcl DEFINITION. ENDCLASS.", &opts)
			if err != nil {
				t.Fatalf("WriteSource: %v", err)
			}
			if result.Success || !strings.Contains(result.Message, tt.want) {
				t.Fatalf("result = success=%v message=%q, want a refusal containing %q", result.Success, result.Message, tt.want)
			}
			if calls := snapshot(); len(calls) != 0 {
				t.Fatalf("a refused include write must not reach SAP; calls=%+v", calls)
			}
		})
	}
}

func TestWriteSourceClassIncludeCreatesMissingTestInclude(t *testing.T) {
	t.Run("404", func(t *testing.T) {
		testCreatesMissingTestInclude(t, http.StatusNotFound, "")
	})
	t.Run("500 ED 170, as a 7.58 answers", func(t *testing.T) {
		testCreatesMissingTestInclude(t, http.StatusInternalServerError, missingIncludeED170)
	})
}

func TestWriteSourceClassIncludeOtherFailureDoesNotCreate(t *testing.T) {
	c, snapshot := newIncludeWriteServerAnswering(t, true, http.StatusInternalServerError, "some other save failure")
	result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", "CLASS ltcl DEFINITION FOR TESTING. ENDCLASS.", &WriteSourceOptions{Include: "testclasses"})
	if err != nil {
		t.Fatalf("WriteSource: %v", err)
	}
	if result.Success {
		t.Fatalf("WriteSource succeeded on a failed PUT: %s", result.Message)
	}
	var unlocked bool
	for _, call := range snapshot() {
		if call.method == http.MethodPost && strings.HasSuffix(call.path, "/includes") {
			t.Fatalf("an unrelated failure must not create the include; calls=%+v", snapshot())
		}
		if call.action == "UNLOCK" {
			unlocked = true
		}
	}
	if !unlocked {
		t.Fatalf("a failed write must release the class lock; calls=%+v", snapshot())
	}
}

func testCreatesMissingTestInclude(t *testing.T, status int, body string) {
	c, snapshot := newIncludeWriteServerAnswering(t, true, status, body)
	result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", "CLASS ltcl DEFINITION FOR TESTING. ENDCLASS.", &WriteSourceOptions{Include: "testclasses"})
	if err != nil {
		t.Fatalf("WriteSource: %v", err)
	}
	if !result.Success || !strings.Contains(result.Message, "created") {
		t.Fatalf("result = success=%v message=%q, want the include created and written", result.Success, result.Message)
	}
	puts := putPaths(snapshot())
	if len(puts) != 2 || puts[1] != "/sap/bc/adt/oo/classes/ZCL_PROBE/includes/testclasses" {
		t.Fatalf("PUTs = %v, want the 404 attempt and the retry on the include", puts)
	}
}

// #282: a namespaced class is escaped exactly once, whether the name arrives
// raw or already escaped.
func TestWriteSourceClassIncludeNamespacedEscapedOnce(t *testing.T) {
	for _, name := range []string{"/ZDEMO/CL_PROBE", "%2FZDEMO%2FCL_PROBE"} {
		c, snapshot := newIncludeWriteServer(t, false)
		result, err := c.WriteSource(context.Background(), "CLAS", name, "CLASS ltcl DEFINITION FOR TESTING. ENDCLASS.", &WriteSourceOptions{Include: "testclasses"})
		if err != nil {
			t.Fatalf("WriteSource(%s): %v", name, err)
		}
		if !result.Success {
			t.Fatalf("WriteSource(%s) failed: %s", name, result.Message)
		}
		puts := putPaths(snapshot())
		if len(puts) != 1 || puts[0] != "/sap/bc/adt/oo/classes/%2FZDEMO%2FCL_PROBE/includes/testclasses" {
			t.Fatalf("name %s: PUTs = %v, want the include escaped once", name, puts)
		}
		for _, call := range snapshot() {
			if strings.Contains(call.path, "%25") {
				t.Fatalf("name %s: double-escaped request %s", name, call.path)
			}
		}
	}
}

func TestSplitClassIncludeURL(t *testing.T) {
	tests := []struct {
		in, class, source string
		ok, err           bool
	}{
		{"/sap/bc/adt/oo/classes/zcl_x/includes/testclasses", "/sap/bc/adt/oo/classes/zcl_x", "/sap/bc/adt/oo/classes/zcl_x/includes/testclasses", true, false},
		{"/sap/bc/adt/oo/classes/zcl_x/includes/Definitions/", "/sap/bc/adt/oo/classes/zcl_x", "/sap/bc/adt/oo/classes/zcl_x/includes/definitions", true, false},
		{"/sap/bc/adt/oo/classes/%2Fzdemo%2Fcl_x/includes/macros", "/sap/bc/adt/oo/classes/%2Fzdemo%2Fcl_x", "/sap/bc/adt/oo/classes/%2Fzdemo%2Fcl_x/includes/macros", true, false},
		{"/sap/bc/adt/oo/classes/zcl_x/includes/main", "/sap/bc/adt/oo/classes/zcl_x", "/sap/bc/adt/oo/classes/zcl_x/source/main", true, false},
		{"/sap/bc/adt/oo/classes/zcl_x/includes/localtypes", "", "", true, true},
		{"/sap/bc/adt/oo/classes/zcl_x/includes", "", "", true, true},
		{"/sap/bc/adt/oo/classes/zcl_x/includes/testclasses/source/main", "", "", true, true},
		{"/sap/bc/adt/oo/classes/zcl_x", "", "", false, false},
		{"/sap/bc/adt/oo/classes/zcl_x/source/main", "", "", false, false},
		{"/sap/bc/adt/programs/includes/zinclude", "", "", false, false},
	}
	for _, tt := range tests {
		class, source, ok, err := SplitClassIncludeURL(tt.in)
		if ok != tt.ok || (err != nil) != tt.err {
			t.Errorf("%s: ok=%v err=%v, want ok=%v err=%v", tt.in, ok, err, tt.ok, tt.err)
			continue
		}
		if !tt.err && (class != tt.class || source != tt.source) {
			t.Errorf("%s: got (%s, %s), want (%s, %s)", tt.in, class, source, tt.class, tt.source)
		}
	}
}

func countCalls(calls []includeWireCall, pred func(includeWireCall) bool) int {
	n := 0
	for _, c := range calls {
		if pred(c) {
			n++
		}
	}
	return n
}

func isCreateInclude(c includeWireCall) bool {
	return c.method == http.MethodPost && strings.HasSuffix(c.path, "/includes")
}

func isIncludeUnlock(c includeWireCall) bool { return c.action == "UNLOCK" }

const probeTestSource = "CLASS ltcl DEFINITION FOR TESTING. ENDCLASS."

// The expected_source_hash precondition reads the include before the PUT. That
// read answering 404 must not create the include: the write is refused as
// drift anyway, and the create would leave an empty include behind.
func TestWriteSourceClassIncludePreconditionReadDoesNotCreate(t *testing.T) {
	c, snapshot := includeServer{missingTestInclude: true, missingStatus: http.StatusInternalServerError, missingBody: missingIncludeED170}.start(t)
	result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", probeTestSource, &WriteSourceOptions{
		Include: "testclasses", ExpectedSourceHash: SourceHash("CLASS ltcl_old DEFINITION FOR TESTING. ENDCLASS."),
	})
	if err != nil {
		t.Fatalf("WriteSource: %v", err)
	}
	if result.Success {
		t.Fatalf("a failed precondition read must fail the write: %s", result.Message)
	}
	calls := snapshot()
	if n := countCalls(calls, isCreateInclude); n != 0 {
		t.Fatalf("the precondition read created the include (%d POST .../includes); calls=%+v", n, calls)
	}
	if puts := putPaths(calls); len(puts) != 0 {
		t.Fatalf("PUTs = %v, want none", puts)
	}
	if countCalls(calls, isIncludeUnlock) != 1 {
		t.Fatalf("the class lock must be released; calls=%+v", calls)
	}
}

// When the include had to be created, every outcome says so, the failures
// included: the caller is left with an include it did not have before.
func TestWriteSourceClassIncludeReportsCreatedOnFailure(t *testing.T) {
	c, _ := includeServer{
		missingTestInclude: true, missingStatus: http.StatusNotFound,
		activationBody: activationErrorXML,
	}.start(t)
	result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", probeTestSource, &WriteSourceOptions{Include: "testclasses"})
	if err != nil {
		t.Fatalf("WriteSource: %v", err)
	}
	if result.Success || !strings.Contains(result.Message, "was created") {
		t.Fatalf("result = success=%v message=%q, want a failure that says the include was created", result.Success, result.Message)
	}
}

const activationErrorXML = `<?xml version="1.0" encoding="utf-8"?><chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist"><msg objDescr="Class ZCL_PROBE" type="E" line="1"><shortText><txt>LTCL is unknown</txt></shortText></msg></chkl:messages>`

func TestWriteSourceClassIncludeReportsActivationFailure(t *testing.T) {
	c, _ := includeServer{activationBody: activationErrorXML}.start(t)
	result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", probeTestSource, &WriteSourceOptions{Include: "testclasses"})
	if err != nil {
		t.Fatalf("WriteSource: %v", err)
	}
	if result.Success {
		t.Fatalf("a failed activation was reported as success: %s", result.Message)
	}
	if !strings.Contains(result.Message, "activation failed") || result.Activation == nil || len(result.Activation.Messages) == 0 {
		t.Fatalf("result = message=%q activation=%+v, want the activation failure and its messages", result.Message, result.Activation)
	}
}

func TestWriteSourceClassIncludeReleasesLockWhenCreateFails(t *testing.T) {
	c, snapshot := includeServer{missingTestInclude: true, missingStatus: http.StatusNotFound, createFails: true}.start(t)
	result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", probeTestSource, &WriteSourceOptions{Include: "testclasses"})
	if err != nil {
		t.Fatalf("WriteSource: %v", err)
	}
	if result.Success || !strings.Contains(result.Message, "creating the testclasses include also failed") {
		t.Fatalf("result = success=%v message=%q, want the create failure reported", result.Success, result.Message)
	}
	calls := snapshot()
	if countCalls(calls, isCreateInclude) != 1 {
		t.Fatalf("want one create attempt; calls=%+v", calls)
	}
	unlock := -1
	for i, call := range calls {
		if isIncludeUnlock(call) {
			unlock = i
		}
	}
	if unlock < 0 || calls[unlock].path != "/sap/bc/adt/oo/classes/ZCL_PROBE" {
		t.Fatalf("the class lock must be released after the create fails; calls=%+v", calls)
	}
}

// expected_source_hash is checked on, and verified against, the include's own
// source, never the main source.
func TestWriteSourceClassIncludeExpectedSourceHash(t *testing.T) {
	const includePath = "/sap/bc/adt/oo/classes/ZCL_PROBE/includes/testclasses"
	const current = "CLASS ltcl_old DEFINITION FOR TESTING. ENDCLASS."
	const mainSource = "CLASS zcl_probe DEFINITION PUBLIC. ENDCLASS. CLASS zcl_probe IMPLEMENTATION. ENDCLASS."
	initial := map[string]string{
		includePath: current,
		"/sap/bc/adt/oo/classes/ZCL_PROBE/source/main": mainSource,
	}

	t.Run("matching hash writes and verifies the include", func(t *testing.T) {
		c, snapshot := includeServer{initial: initial}.start(t)
		result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", probeTestSource, &WriteSourceOptions{
			Include: "testclasses", ExpectedSourceHash: SourceHash(current),
		})
		if err != nil {
			t.Fatalf("WriteSource: %v", err)
		}
		if !result.Success {
			t.Fatalf("WriteSource failed: %s", result.Message)
		}
		if result.VerifiedSourceHash != SourceHash(probeTestSource) {
			t.Fatalf("verifiedSourceHash = %s, want the hash of the written include", result.VerifiedSourceHash)
		}
		calls := snapshot()
		gets := countCalls(calls, func(c includeWireCall) bool { return c.method == http.MethodGet && c.path == includePath })
		if gets != 2 {
			t.Fatalf("want the include read twice (precondition, verification), got %d; calls=%+v", gets, calls)
		}
		if n := countCalls(calls, func(c includeWireCall) bool {
			return c.method == http.MethodGet && strings.HasSuffix(c.path, "/source/main")
		}); n != 0 {
			t.Fatalf("the main source was read %d time(s) for an include write; calls=%+v", n, calls)
		}
	})

	t.Run("drifted include is not written", func(t *testing.T) {
		c, snapshot := includeServer{initial: initial}.start(t)
		result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", probeTestSource, &WriteSourceOptions{
			// The main source's hash: right for main, wrong for the include.
			Include: "testclasses", ExpectedSourceHash: SourceHash(mainSource),
		})
		if err != nil {
			t.Fatalf("WriteSource: %v", err)
		}
		if result.Success {
			t.Fatalf("a drifted include was written: %s", result.Message)
		}
		if puts := putPaths(snapshot()); len(puts) != 0 {
			t.Fatalf("PUTs = %v, want none after drift", puts)
		}
	})
}

// The gate (read-only, operation type, package) runs before any LOCK.
func TestWriteSourceClassIncludeGateRunsBeforeLock(t *testing.T) {
	t.Run("allowed package: search precedes the lock", func(t *testing.T) {
		c, snapshot := includeServer{packageName: "$TMP", opts: []Option{WithAllowedPackages("$TMP")}}.start(t)
		result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", probeTestSource, &WriteSourceOptions{Include: "testclasses"})
		if err != nil || !result.Success {
			t.Fatalf("WriteSource: err=%v result=%+v", err, result)
		}
		calls := snapshot()
		search, lock := -1, -1
		for i, call := range calls {
			if search < 0 && strings.Contains(call.path, "informationsystem/search") {
				search = i
			}
			if lock < 0 && call.action == "LOCK" {
				lock = i
			}
		}
		if search < 0 || lock < 0 || search > lock {
			t.Fatalf("package search must come before the LOCK: search=%d lock=%d calls=%+v", search, lock, calls)
		}
		for _, call := range calls[lock:] {
			if strings.Contains(call.path, "informationsystem/search") {
				t.Fatalf("a package lookup inside the lock window kills the handle (#91); calls=%+v", calls)
			}
		}
	})

	for _, tt := range []struct {
		name string
		cfg  includeServer
	}{
		{"package not allowed", includeServer{packageName: "ZOTHER", opts: []Option{WithAllowedPackages("$TMP")}}},
		{"read only", includeServer{opts: []Option{WithReadOnly()}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, snapshot := tt.cfg.start(t)
			result, err := c.WriteSource(context.Background(), "CLAS", "ZCL_PROBE", probeTestSource, &WriteSourceOptions{Include: "testclasses"})
			if err == nil && result != nil && result.Success {
				t.Fatalf("the gate let the write through: %+v", result)
			}
			for _, call := range snapshot() {
				if call.action == "LOCK" || call.method == http.MethodPut {
					t.Fatalf("a refused write reached %s %s", call.method, call.path)
				}
			}
		})
	}
}

func TestTestIncludeMissing(t *testing.T) {
	ccau := "ZCL_PROBE" + strings.Repeat("=", 21) + "CCAU"
	other := "ZCL_OTHER" + strings.Repeat("=", 21) + "CCAU"
	keys := func(v1 string) string {
		return `<properties><entry key="T100KEY-ID">ED</entry><entry key="T100KEY-NO">170</entry><entry key="T100KEY-V1">` + v1 + `</entry></properties>`
	}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"404", &APIError{StatusCode: 404, Message: "not found"}, true},
		{"500 ED 170 for this class", &APIError{StatusCode: 500, Message: "<message>" + ccau + " does not have any inactive version</message>" + keys(ccau)}, true},
		{"500 ED 170, German logon", &APIError{StatusCode: 500, Message: "<message>" + ccau + " hat keine inaktive Version</message>" + keys(ccau)}, true},
		{"500 English text for this class, no keys", &APIError{StatusCode: 500, Message: ccau + " does not have any inactive version"}, true},
		{"500 bare English text", &APIError{StatusCode: 500, Message: "does not have any inactive version"}, false},
		{"500 ED 170 for another class", &APIError{StatusCode: 500, Message: "<message>" + other + " does not have any inactive version</message>" + keys(other)}, false},
		{"500 this class's include, another message", &APIError{StatusCode: 500, Message: "<message>" + ccau + " is locked</message>"}, false},
		{"400 ED 170 for this class", &APIError{StatusCode: 400, Message: ccau + " does not have any inactive version" + keys(ccau)}, false},
		{"not an API error", errors.New(ccau + " does not have any inactive version"), false},
	}
	for _, tt := range tests {
		if got := testIncludeMissing(tt.err, "ZCL_PROBE"); got != tt.want {
			t.Errorf("%s: testIncludeMissing = %v, want %v", tt.name, got, tt.want)
		}
	}
}
