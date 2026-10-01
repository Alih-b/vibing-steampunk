package adt

import (
	"encoding/json"
	"strings"
	"testing"
)

// unitRunXML is an ABAP Unit answer in the shape SAP sends it: one class under
// test with two test classes. LTC_CALC has a passing method, a failing one
// whose title is SAP's wrapped "Critical Assertion Error: '...'" sentence, and
// a tolerable warning on another passing method. LTC_SETUP failed in
// CLASS_SETUP, which ABAP Unit files on the class, and has no method results.
const unitRunXML = `<?xml version="1.0" encoding="utf-8"?>` +
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

func parsedUnitRun(t *testing.T) *UnitTestResult {
	t.Helper()
	result, err := parseUnitTestResult([]byte(unitRunXML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return result
}

func TestUnitTestReportCountsTheWholeRun(t *testing.T) {
	report := NewUnitTestReport(parsedUnitRun(t), false)

	want := UnitTestCounts{Classes: 2, Methods: 3, Passed: 2, Failed: 1, ClassFailures: 1, Warnings: 1}
	if report.Counts != want {
		t.Fatalf("counts = %+v, want %+v", report.Counts, want)
	}
	if report.OK {
		t.Fatal("a run with a failed method and a failed CLASS_SETUP is ok")
	}
}

// The full form is a superset of what the tool always answered: the same
// "classes" with every field it had, so a caller reading them still can.
func TestUnitTestReportFullFormKeepsTheExistingShape(t *testing.T) {
	out, _ := json.Marshal(NewUnitTestReport(parsedUnitRun(t), false))
	var got struct {
		OK      *bool           `json:"ok"`
		Counts  *UnitTestCounts `json:"counts"`
		Classes []UnitTestClass `json:"classes"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OK == nil || got.Counts == nil {
		t.Fatalf("ok and counts are missing: %s", out)
	}
	if len(got.Classes) != 2 || got.Classes[0].ParentName != "ZCL_DEMO_CALC" || len(got.Classes[0].TestMethods) != 3 {
		t.Fatalf("classes lost their shape: %s", out)
	}
	if got.Classes[0].TestMethods[1].Alerts[0].Title != "Critical Assertion Error: 'SUBTRACTS: 5 - 3 should be 2'" {
		t.Fatalf("SAP's title was altered: %q", got.Classes[0].TestMethods[1].Alerts[0].Title)
	}
	if len(got.Classes[1].Alerts) != 1 {
		t.Fatalf("the CLASS_SETUP failure filed on the class is missing: %s", out)
	}
}

// only_failures lists the failed method and the class-level failure, with
// names, parent and alerts, and nothing that passed.
func TestUnitTestReportOnlyFailures(t *testing.T) {
	report := NewUnitTestReport(parsedUnitRun(t), true)
	out, _ := json.Marshal(report)
	text := string(out)

	classes, ok := report.Classes.([]UnitTestFailureClass)
	if !ok || len(classes) != 2 {
		t.Fatalf("expected two classes with failures, got %s", text)
	}
	calc := classes[0]
	if calc.Name != "LTC_CALC" || calc.ParentName != "ZCL_DEMO_CALC" {
		t.Fatalf("class name/parent wrong: %+v", calc)
	}
	if len(calc.TestMethods) != 1 || calc.TestMethods[0].Name != "SUBTRACTS" {
		t.Fatalf("only SUBTRACTS failed, got %+v", calc.TestMethods)
	}
	alert := calc.TestMethods[0].Alerts[0]
	if alert.Kind != "failedAssertion" || alert.Severity != "critical" || alert.Title != "Critical Assertion Error: 'SUBTRACTS: 5 - 3 should be 2'" {
		t.Fatalf("alert = %+v", alert)
	}
	if !strings.Contains(alert.At, "Line: <21>") {
		t.Fatalf("where it failed is lost: %q", alert.At)
	}
	setup := classes[1]
	if setup.Name != "LTC_SETUP" || len(setup.Alerts) != 1 || setup.Alerts[0].Kind != "exception" {
		t.Fatalf("the class-level failure is missing: %+v", setup)
	}
	for _, passed := range []string{"ADDS", "ROUNDS", "navigationUri", "stack"} {
		if strings.Contains(text, passed) {
			t.Errorf("only_failures still carries %q: %s", passed, text)
		}
	}
	if report.Counts.Methods != 3 || !report.OnlyFailures {
		t.Fatalf("counts must cover the whole run: %+v", report.Counts)
	}
}

// An all-green run in failures-only form is ok plus counts and an empty list.
func TestUnitTestReportOnlyFailuresOfAGreenRun(t *testing.T) {
	result := &UnitTestResult{Classes: []UnitTestClass{{Name: "LTC_A", ParentName: "ZCL_A", TestMethods: []UnitTestMethod{{Name: "M1"}, {Name: "M2"}}}}}
	report := NewUnitTestReport(result, true)
	if !report.OK || report.Counts.Passed != 2 {
		t.Fatalf("a green run is not ok: %+v", report)
	}
	out, _ := json.Marshal(report)
	if !strings.Contains(string(out), `"classes":[]`) {
		t.Fatalf("expected an empty class list: %s", out)
	}
}

// No test method ran: not ok, and the note says why.
func TestUnitTestReportNothingRanIsNotOK(t *testing.T) {
	for name, result := range map[string]*UnitTestResult{
		"no classes": {Classes: []UnitTestClass{}},
		"risk level": {Classes: []UnitTestClass{{Name: "LTC_X", Alerts: []UnitTestAlert{{Kind: "warning", Severity: "tolerable", Title: "No execution, risk level of test class exceeds upper limit"}}}}},
	} {
		report := NewUnitTestReport(result, false)
		if report.OK || report.Note == "" {
			t.Errorf("%s: a run with no test method was ok (note %q)", name, report.Note)
		}
	}
}

// partialRefusalXML is a run in which ABAP Unit ran one test class and refused
// the other for its risk level: the refused class comes back with no test
// method and only a tolerable warning on the class.
const partialRefusalXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<program adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc" adtcore:type="CLAS/OC" adtcore:name="ZCL_DEMO_CALC">` +
	`<testClasses>` +
	`<testClass adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc/includes/testclasses#type=CLAS%2FOLD;name=LTC_CALC" adtcore:type="CLAS/OLD" adtcore:name="LTC_CALC" durationCategory="short" riskLevel="harmless">` +
	`<testMethods>` +
	`<testMethod adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc/includes/testclasses#type=CLAS%2FOLI;name=LTC_CALC%20%20ADDS" adtcore:type="CLAS/OLI" adtcore:name="ADDS" executionTime="0.001" unit="s"/>` +
	`</testMethods></testClass>` +
	`<testClass adtcore:uri="/sap/bc/adt/oo/classes/zcl_demo_calc/includes/testclasses#type=CLAS%2FOLD;name=LTC_DB" adtcore:type="CLAS/OLD" adtcore:name="LTC_DB" durationCategory="short" riskLevel="dangerous">` +
	`<alerts><alert kind="warning" severity="tolerable"><title>No execution, risk level of test class exceeds upper limit</title></alert></alerts>` +
	`<testMethods/></testClass>` +
	`</testClasses></program></aunit:runResult>`

// One class passed and one was refused: the run is not ok, and the note names
// the refused class. Main used to exit non-zero here.
func TestUnitTestReportPartialRefusalIsNotOK(t *testing.T) {
	result, err := parseUnitTestResult([]byte(partialRefusalXML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, onlyFailures := range []bool{false, true} {
		report := NewUnitTestReport(result, onlyFailures)
		want := UnitTestCounts{Classes: 2, Methods: 1, Passed: 1, Warnings: 1, NotRun: 1}
		if report.Counts != want {
			t.Fatalf("counts = %+v, want %+v", report.Counts, want)
		}
		if report.OK {
			t.Fatalf("onlyFailures=%v: a run with a refused test class is ok", onlyFailures)
		}
		if len(report.NotRunClasses) != 1 || report.NotRunClasses[0] != "LTC_DB" {
			t.Fatalf("notRunClasses = %v", report.NotRunClasses)
		}
		if !strings.Contains(report.Note, "LTC_DB") {
			t.Fatalf("the note does not name the refused class: %q", report.Note)
		}
	}
}

// SAP's titles are full of angle brackets; the JSON keeps them as they are.
func TestIndentJSONKeepsAngleBrackets(t *testing.T) {
	out, err := IndentJSON(map[string]string{"title": "Exception Error <CX_SY_ZERODIVIDE> & more"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Exception Error <CX_SY_ZERODIVIDE> & more") {
		t.Fatalf("escaped: %s", out)
	}
}
