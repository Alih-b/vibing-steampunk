package adt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// What SAP really answers when the wrapped payload does not compile, taken from
// a live run of the reported repro. Two things in it are the whole bug:
// the status was 200, and the only "no" in the exchange is the type="E" message
// below. Nothing later in the run mentions it again — the test run that follows
// is an empty <aunit:runResult/>, which is exactly what a program with no tests
// looks like.
//
// The generated program name is a timestamp, so it names nobody. SAP spells it
// in lower case in the href and in upper case in objDescr, and the fixture keeps
// that inconsistency because the code has to survive it.
func refusedActivationXML(program string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist">`+
		`<chkl:properties checkExecuted="true" activationExecuted="false" generationExecuted="false"/>`+
		`<msg objDescr="" type="W" line="0" href="">`+
		`<shortText><txt>Activation was cancelled.</txt><txt>"Editing canceled" (EU 202)</txt></shortText></msg>`+
		`<msg objDescr="Program %s" type="E" line="1" href="/sap/bc/adt/programs/programs/%s/source/main#start=18,18" forceSupported="true">`+
		`<shortText><txt>Tables with headers are no longer supported in the OO context.</txt></shortText></msg>`+
		`</chkl:messages>`, strings.ToUpper(program), strings.ToLower(program))
}

// The empty answer that used to be read as success. There is no program element,
// no test class and no alert in it — ABAP Unit has nothing to say about a
// program that was never generated.
const emptyRunResultXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit"/>`

var activationNameRE = regexp.MustCompile(`adtcore:name="([^"]+)"`)

// executeServer stands in for SAP over the whole ExecuteABAP flow: create, lock,
// write, unlock, activate, run tests, delete. activation and runResult are what
// those last two answer, and both are given the generated program name because
// the line translation depends on recognising it.
type executeServer struct {
	activation func(program string) (int, string)
	runResult  func(program string) (int, string)

	mu      sync.Mutex
	program string
	called  []string
}

func (s *executeServer) start(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/discovery"):
			w.Header().Set("X-CSRF-Token", "TOKEN")
			w.WriteHeader(http.StatusOK)

		case strings.Contains(path, "/activation"):
			body, _ := io.ReadAll(r.Body)
			program := ""
			if m := activationNameRE.FindSubmatch(body); m != nil {
				program = string(m[1])
			}
			s.record("activate", program)
			status, xml := s.activation(program)
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(xml))

		case strings.Contains(path, "/abapunit/testruns"):
			s.record("testrun", "")
			status, xml := s.runResult(s.programName())
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(xml))

		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			s.record("lock", "")
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>
<LOCK_HANDLE>HANDLE-1</LOCK_HANDLE><MODIFICATION_SUPPORT>Modification</MODIFICATION_SUPPORT>
</DATA></asx:values></asx:abap>`))

		case r.Method == http.MethodDelete:
			s.record("delete", "")
			w.WriteHeader(http.StatusOK)

		default:
			s.record(r.Method+" "+path, "")
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := NewConfig(srv.URL, "TESTUSER", "secret")
	return NewClientWithTransport(cfg, NewTransport(cfg))
}

func (s *executeServer) record(what, program string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.called = append(s.called, what)
	if program != "" {
		s.program = program
	}
}

func (s *executeServer) programName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.program
}

func (s *executeServer) saw(what string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.called {
		if c == what {
			return true
		}
	}
	return false
}

// The reported bug, end to end: a payload that does not compile came back as
// "Executed successfully", exit 0.
func TestExecuteABAPReportsCodeThatDoesNotCompile(t *testing.T) {
	srv := &executeServer{
		activation: func(program string) (int, string) {
			return http.StatusOK, refusedActivationXML(program)
		},
		runResult: func(string) (int, string) { return http.StatusOK, emptyRunResultXML },
	}
	client := srv.start(t)

	result, err := client.ExecuteABAP(context.Background(), "DATA: BEGIN OF lt OCCURS 0,\n  f TYPE i,\nEND OF lt.", nil)
	if err != nil {
		t.Fatalf("ExecuteABAP: %v", err)
	}
	if result.Success {
		t.Fatal("code that SAP refused to activate was reported as a successful run")
	}
	if result.Failure == nil {
		t.Fatal("no failure was reported for a program that never compiled")
	}
	if result.Failure.Kind != ExecuteFailureSyntax {
		t.Fatalf("wrong kind of failure: %q", result.Failure.Kind)
	}
	// SAP's own words, not ours. Whoever wrote the payload can act on this one.
	if !strings.Contains(result.Failure.Title, "Tables with headers") {
		t.Fatalf("SAP's message did not survive into the failure: %q", result.Failure.Title)
	}
	// The error is on wrapper line 18, which is the first line of the payload.
	if result.Failure.Line != 1 {
		t.Fatalf("wrapper line 18 should be line 1 of the caller's code, got %d", result.Failure.Line)
	}
	if !strings.Contains(result.Message, "did not compile") {
		t.Fatalf("the summary does not say what happened: %q", result.Message)
	}
}

// Running the tests of a program that was never generated is where the silence
// came from, so the fix has to stop before it, not merely report afterwards.
func TestExecuteABAPDoesNotRunTestsAfterARefusedActivation(t *testing.T) {
	srv := &executeServer{
		activation: func(program string) (int, string) {
			return http.StatusOK, refusedActivationXML(program)
		},
		runResult: func(string) (int, string) {
			t.Error("ABAP Unit was asked about a program that failed to activate")
			return http.StatusOK, emptyRunResultXML
		},
	}
	client := srv.start(t)

	if _, err := client.ExecuteABAP(context.Background(), "this is not ABAP", nil); err != nil {
		t.Fatalf("ExecuteABAP: %v", err)
	}
	// The throwaway program still has to go, refused or not.
	if !srv.saw("delete") {
		t.Fatal("the temp program was left behind after a failed activation")
	}
}

// The house rule, in the one place it was broken hardest: an empty test run is
// not a passing test run. This is the case where activation says yes and ABAP
// Unit then reports nothing at all — we cannot say the code ran, so we do not.
func TestExecuteABAPRefusesToCallAnEmptyTestRunASuccess(t *testing.T) {
	srv := &executeServer{
		activation: func(string) (int, string) { return http.StatusOK, "" },
		runResult:  func(string) (int, string) { return http.StatusOK, emptyRunResultXML },
	}
	client := srv.start(t)

	result, err := client.ExecuteABAP(context.Background(), "lv_result = 'ok'.", nil)
	if err != nil {
		t.Fatalf("ExecuteABAP: %v", err)
	}
	if result.Success {
		t.Fatal("a run that ABAP Unit never reported was called a success")
	}
	if result.Failure == nil || result.Failure.Kind != ExecuteFailureNotRun {
		t.Fatalf("expected a notRun failure, got %+v", result.Failure)
	}
}

// The activation checklist puts the real position in the href fragment and
// leaves `line` saying 1, so a reader that trusts the attribute reports every
// syntax error against the first line of the payload.
func TestActivationMessageSourceLinePrefersTheHref(t *testing.T) {
	m := ActivationResultMessage{
		Type: "E",
		Line: 1,
		Href: "/sap/bc/adt/programs/programs/ztemp_exec_11021029/source/main#start=18,18",
	}
	if got := m.SourceLine(); got != 18 {
		t.Fatalf("the href says line 18, got %d", got)
	}
}

// A message with no href at all still has whatever the attribute says, and that
// is better than nothing.
func TestActivationMessageSourceLineFallsBackToTheAttribute(t *testing.T) {
	if got := (ActivationResultMessage{Type: "E", Line: 7}).SourceLine(); got != 7 {
		t.Fatalf("expected the attribute's line 7, got %d", got)
	}
}

// One bad statement produces several messages; the first is the headline and the
// others are kept, because the second often explains the first.
func TestCompileFailureKeepsTheLaterMessagesAsDetails(t *testing.T) {
	activation := &ActivationResult{
		Success: false,
		Messages: []ActivationResultMessage{
			{Type: "W", ShortText: "Activation was cancelled."},
			{Type: "E", ShortText: "Field LT is unknown.", Href: "/x/ztemp_exec_1/source/main#start=19,0"},
			{Type: "E", ShortText: "Statement is not accessible.", Href: "/x/ztemp_exec_1/source/main#start=21,0"},
		},
	}
	failure := compileFailure(activation, "ZTEMP_EXEC_1", 18)
	if failure == nil {
		t.Fatal("a refused activation produced no failure")
	}
	if failure.Title != "Field LT is unknown." {
		t.Fatalf("wrong headline: %q", failure.Title)
	}
	if failure.Line != 2 {
		t.Fatalf("wrapper line 19 with the payload starting at 18 is the caller's line 2, got %d", failure.Line)
	}
	if len(failure.Details) != 1 || !strings.Contains(failure.Details[0], "line 4") {
		t.Fatalf("the second error was lost or mislocated: %v", failure.Details)
	}
}

// Activation reports on everything it touched, and a message about another
// object carries a line number that belongs to that object's source. Reporting
// it as the caller's line sends them somewhere they never wrote.
func TestCompileFailureRefusesToGuessFromAnotherProgram(t *testing.T) {
	activation := &ActivationResult{
		Success: false,
		Messages: []ActivationResultMessage{
			{Type: "E", ShortText: "Something in another object", Href: "/x/zsomething_else/source/main#start=40,0"},
		},
	}
	failure := compileFailure(activation, "ZTEMP_EXEC_1", 18)
	if failure == nil {
		t.Fatal("a refused activation produced no failure")
	}
	if failure.Line != 0 {
		t.Fatalf("a line from another program was reported as the caller's line %d", failure.Line)
	}
}

// A refusal that names no reason is still a refusal, and the failure must say
// so — silently succeeding here is the defect this whole change is about.
func TestCompileFailureSpeaksUpWhenSAPGivesNoReason(t *testing.T) {
	failure := compileFailure(&ActivationResult{Success: false}, "ZTEMP_EXEC_1", 18)
	if failure == nil {
		t.Fatal("a refused activation with no messages was treated as a success")
	}
	if len(failure.Details) == 0 {
		t.Fatal("the failure says nothing about a refusal that explained nothing")
	}
}

// An activation that worked must not be turned into a failure by any of this.
func TestCompileFailureLeavesASuccessfulActivationAlone(t *testing.T) {
	activation := &ActivationResult{
		Success:  true,
		Messages: []ActivationResultMessage{{Type: "W", ShortText: "Program is not assigned to a package"}},
	}
	if failure := compileFailure(activation, "ZTEMP_EXEC_1", 18); failure != nil {
		t.Fatalf("a warning was read as a compile failure: %+v", failure)
	}
}

// What ABAP Unit answers when the test class's risk level is above what the
// system allows: the class, one tolerable warning, and no test method at all.
const riskLevelRefusedRunResultXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<program adtcore:uri="/sap/bc/adt/programs/programs/ztemp_exec_1" adtcore:type="PROG/P" adtcore:name="ZTEMP_EXEC_1">` +
	`<testClasses><testClass adtcore:uri="/sap/bc/adt/programs/programs/ztemp_exec_1#type=PROG%2FPLL;name=LTC_EXECUTOR" adtcore:type="PROG/OLL" adtcore:name="LTC_EXECUTOR" durationCategory="short" riskLevel="dangerous">` +
	`<alerts><alert kind="warning" severity="tolerable"><title>No execution, risk level of test class exceeds upper limit</title>` +
	`<details><detail text="You can find further information in document &lt;HY&gt; &lt;CHAPSAUNIT_TEST_PROPS&gt;"/></details></alert></alerts>` +
	`<testMethods/></testClass></testClasses></program></aunit:runResult>`

// A test class the system refuses to run for its risk level leaves only a
// tolerable warning behind. That is a run that never happened, not a success
// with nothing to say.
func TestExecuteABAPReportsARunRefusedForItsRiskLevel(t *testing.T) {
	srv := &executeServer{
		activation: func(string) (int, string) { return http.StatusOK, "" },
		runResult:  func(string) (int, string) { return http.StatusOK, riskLevelRefusedRunResultXML },
	}
	client := srv.start(t)

	result, err := client.ExecuteABAP(context.Background(), "lv_result = 'ok'.", &ExecuteABAPOptions{RiskLevel: "dangerous"})
	if err != nil {
		t.Fatalf("ExecuteABAP: %v", err)
	}
	if result.Success {
		t.Fatalf("a run ABAP Unit refused to execute was called a success: %q", result.Message)
	}
	if result.Failure == nil || result.Failure.Kind != ExecuteFailureNotRun {
		t.Fatalf("expected a notRun failure, got %+v", result.Failure)
	}
	if !strings.Contains(result.Failure.Title, "risk level") || !strings.Contains(result.Message, "risk level") {
		t.Errorf("SAP's reason did not reach the caller: title %q, message %q", result.Failure.Title, result.Message)
	}
}

// A payload that leaves the test method early (CHECK, RETURN, or EXIT outside a
// loop) never reaches the closing assertion, but ABAP Unit still lists the
// method it executed. The code ran; reporting it as not run would invite a
// caller to run side-effecting code a second time.
const methodWithoutAlertsRunResultXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<program adtcore:uri="/sap/bc/adt/programs/programs/ztemp_exec_1" adtcore:type="PROG/P" adtcore:name="ZTEMP_EXEC_1">` +
	`<testClasses><testClass adtcore:uri="/sap/bc/adt/programs/programs/ztemp_exec_1#type=PROG%2FPLL;name=LTC_EXECUTOR" adtcore:type="PROG/OLL" adtcore:name="LTC_EXECUTOR" durationCategory="short" riskLevel="dangerous">` +
	`<testMethods><testMethod adtcore:name="EXECUTE_PAYLOAD" executionTime="0.01"/></testMethods>` +
	`</testClass></testClasses></program></aunit:runResult>`

func TestExecuteABAPAnEarlyReturnIsARun(t *testing.T) {
	srv := &executeServer{
		activation: func(string) (int, string) { return http.StatusOK, "" },
		runResult:  func(string) (int, string) { return http.StatusOK, methodWithoutAlertsRunResultXML },
	}
	client := srv.start(t)

	result, err := client.ExecuteABAP(context.Background(), "CHECK 1 = 2.", &ExecuteABAPOptions{RiskLevel: "dangerous"})
	if err != nil {
		t.Fatalf("ExecuteABAP: %v", err)
	}
	if result.Failure != nil && result.Failure.Kind == ExecuteFailureNotRun {
		t.Fatalf("a test method that ran was reported as not run: %q", result.Message)
	}
	if !result.Success {
		t.Fatalf("a run that left early without a failure was not a success: %+v", result.Failure)
	}
}
