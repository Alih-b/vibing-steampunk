package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The same ABAP Unit answer pkg/adt's report tests use: LTC_CALC with a
// passing, a failing (SAP's wrapped "Critical Assertion Error: '...'" title)
// and a warned method; LTC_SETUP failed in CLASS_SETUP.
const unitRunFixtureXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<program adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc" adtcore:type="CLAS/OC" adtcore:name="ZCL_DEMO_CALC">` +
	`<testClasses>` +
	`<testClass adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc/includes/testclasses#type=CLAS%2FOLD;name=LTC_CALC" adtcore:type="CLAS/OLD" adtcore:name="LTC_CALC" uriType="semantic" navigationUri="/sap/bc/adt/oo/classes/zcl_demo_calc/includes/testclasses#type=CLAS%2FOLD;name=LTC_CALC" durationCategory="short" riskLevel="harmless">` +
	`<testMethods>` +
	`<testMethod adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc/includes/testclasses#type=CLAS%2FOLI;name=LTC_CALC%20%20ADDS" adtcore:type="CLAS/OLI" adtcore:name="ADDS" executionTime="0.001" uriType="semantic" unit="s"/>` +
	`<testMethod adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc/includes/testclasses#type=CLAS%2FOLI;name=LTC_CALC%20%20SUBTRACTS" adtcore:type="CLAS/OLI" adtcore:name="SUBTRACTS" executionTime="0.002" uriType="semantic" unit="s">` +
	`<alerts><alert kind="failedAssertion" severity="critical"><title>Critical Assertion Error: 'SUBTRACTS: 5 - 3 should be 2'</title>` +
	`<details><detail text="Expected [2] Actual [8]"/><detail text="Test 'LTC_CALC-&gt;SUBTRACTS' in Main Program 'ZCL_DEMO_CALC=================CP'."/></details>` +
	`<stack><stackEntry adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc/includes/testclasses#start=21,0" adtcore:type="CLAS/OCN/testclasses" adtcore:name="ZCL_DEMO_CALC" adtcore:description="Include: &lt;ZCL_DEMO_CALC=================CCAU&gt; Line: &lt;21&gt; (SUBTRACTS)"/></stack></alert></alerts>` +
	`</testMethod>` +
	`<testMethod adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc/includes/testclasses#type=CLAS%2FOLI;name=LTC_CALC%20%20ROUNDS" adtcore:type="CLAS/OLI" adtcore:name="ROUNDS" executionTime="0.001" uriType="semantic" unit="s">` +
	`<alerts><alert kind="warning" severity="tolerable"><title>Test method uses a deprecated assertion</title></alert></alerts>` +
	`</testMethod>` +
	`</testMethods></testClass>` +
	`<testClass adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc/includes/testclasses#type=CLAS%2FOLD;name=LTC_SETUP" adtcore:type="CLAS/OLD" adtcore:name="LTC_SETUP" durationCategory="short" riskLevel="harmless">` +
	`<alerts><alert kind="exception" severity="critical"><title>Exception Error &lt;CX_SY_ZERODIVIDE&gt;</title>` +
	`<details><detail text="Division by zero"/><detail text="Test 'LTC_SETUP-&gt;CLASS_SETUP' in Main Program 'ZCL_DEMO_CALC=================CP'."/></details>` +
	`<stack><stackEntry adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc/includes/testclasses#start=40,0" adtcore:type="CLAS/OCN/testclasses" adtcore:name="ZCL_DEMO_CALC" adtcore:description="Include: &lt;ZCL_DEMO_CALC=================CCAU&gt; Line: &lt;40&gt; (CLASS_SETUP)"/></stack></alert></alerts>` +
	`<testMethods/></testClass>` +
	`</testClasses></program></aunit:runResult>`

func unitTestSAP(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-csrf-token", "AbCdEfGhIjKlMnOpQrStUv==")
		if strings.Contains(r.URL.Path, "/abapunit/testruns") {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(unitRunFixtureXML))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// SAP(action="test") answers ok and counts on top of the classes it always
// answered.
func TestRunUnitTestsAnswersOKAndCounts(t *testing.T) {
	s := NewServer(&Config{BaseURL: unitTestSAP(t).URL, Username: "TESTUSER", Client: "001", Mode: "expert"})
	res, handled, err := s.routeDevToolsAction(t.Context(), "test", "", "", map[string]any{"object_url": "/sap/bc/adt/oo/classes/zcl_demo_calc"})
	if err != nil || !handled {
		t.Fatalf("not handled: %v", err)
	}
	var got struct {
		OK      *bool `json:"ok"`
		Counts  map[string]int
		Classes []struct {
			Name        string
			ParentName  string
			Alerts      []map[string]any
			TestMethods []map[string]any
		}
	}
	text := resultText(res)
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if got.OK == nil || *got.OK {
		t.Fatalf("ok is missing or true for a failing run:\n%.400s", text)
	}
	if got.Counts["failed"] != 1 || got.Counts["passed"] != 2 || got.Counts["classFailures"] != 1 {
		t.Fatalf("counts = %v", got.Counts)
	}
	if len(got.Classes) != 2 || got.Classes[0].ParentName != "ZCL_DEMO_CALC" || len(got.Classes[0].TestMethods) != 3 || len(got.Classes[1].Alerts) != 1 {
		t.Fatalf("classes lost their shape:\n%s", text)
	}
}

// only_failures reaches the handler through the router and leaves out what
// passed.
func TestRunUnitTestsOnlyFailures(t *testing.T) {
	s := NewServer(&Config{BaseURL: unitTestSAP(t).URL, Username: "TESTUSER", Client: "001", Mode: "expert"})
	res, handled, err := s.routeDevToolsAction(t.Context(), "test", "", "", map[string]any{"object_url": "/sap/bc/adt/oo/classes/zcl_demo_calc", "only_failures": true})
	if err != nil || !handled {
		t.Fatalf("not handled: %v", err)
	}
	text := resultText(res)
	if !strings.Contains(text, `"onlyFailures": true`) || !strings.Contains(text, `"SUBTRACTS"`) || !strings.Contains(text, "CX_SY_ZERODIVIDE") {
		t.Fatalf("the failures are missing:\n%s", text)
	}
	for _, passed := range []string{`"ADDS"`, `"ROUNDS"`, "navigationUri", "stack"} {
		if strings.Contains(text, passed) {
			t.Errorf("only_failures still carries %s:\n%s", passed, text)
		}
	}
}
