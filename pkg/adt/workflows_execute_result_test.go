package adt

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// wrappedResultAlert is the closing assertion as SAP really sends it: the
// message passed to cl_abap_unit_assert=>fail comes back quoted inside a
// sentence of SAP's own, "Critical Assertion Error: '...'".
func wrappedResultAlert(value string) string {
	return `<alert kind="failedAssertion" severity="critical">` +
		`<title>Critical Assertion Error: 'EXEC_RESULT:` + value + `'</title>` +
		`<details><detail text="Test 'LTC_EXECUTOR-&gt;EXECUTE_PAYLOAD' in Main Program 'ZTEMP_EXEC_1'"/></details>` +
		`<stack><stackEntry adtcore:uri="/sap/bc/adt/programs/programs/ztemp_exec_1/source/main#start=24,0" adtcore:type="PROG/P" adtcore:name="ZTEMP_EXEC_1" adtcore:description="Include: &lt;ZTEMP_EXEC_1&gt; Line: &lt;24&gt; (EXECUTE_PAYLOAD)"/></stack>` +
		`</alert>`
}

// execRunResultXML is an ABAP Unit answer for the wrapper's one test class,
// with the given alerts filed on the class and on its one test method.
func execRunResultXML(classAlerts, methodAlerts string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">`+
		`<program adtcore:uri="/sap/bc/adt/programs/programs/ztemp_exec_1" adtcore:type="PROG/P" adtcore:name="ZTEMP_EXEC_1">`+
		`<testClasses><testClass adtcore:uri="/sap/bc/adt/programs/programs/ztemp_exec_1#testclass=LTC_EXECUTOR" adtcore:type="PROG/PP" adtcore:name="LTC_EXECUTOR" durationCategory="short" riskLevel="harmless">`+
		`<alerts>%s</alerts>`+
		`<testMethods><testMethod adtcore:name="EXECUTE_PAYLOAD" executionTime="0.01"><alerts>%s</alerts></testMethod></testMethods>`+
		`</testClass></testClasses></program></aunit:runResult>`, classAlerts, methodAlerts)
}

func runExecuteAgainst(t *testing.T, runResult string) *ExecuteABAPResult {
	t.Helper()
	srv := &executeServer{
		activation: func(string) (int, string) { return http.StatusOK, "" },
		runResult:  func(string) (int, string) { return http.StatusOK, runResult },
	}
	result, err := srv.start(t).ExecuteABAP(context.Background(), "RETURN_VALUE( 1 ). RETURN_VALUE( 2 ).", nil)
	if err != nil {
		t.Fatalf("ExecuteABAP: %v", err)
	}
	return result
}

// One value is result_text as a string, in full and without SAP's sentence
// around it.
func TestExecuteABAPResultTextIsTheUnwrappedValue(t *testing.T) {
	long := strings.Repeat("0123456789", 300) // 3000 characters, more than any title field SAP pads to
	result := runExecuteAgainst(t, execRunResultXML("", wrappedResultAlert(long)))

	if !result.Success {
		t.Fatalf("a run that returned a value is not a success: %+v", result.Failure)
	}
	if got, ok := result.ResultText.(string); !ok || got != long {
		t.Fatalf("result_text is not the whole unwrapped value: %T of length %d", result.ResultText, len(fmt.Sprint(result.ResultText)))
	}
}

// Several values (RETURN_VALUE( ) called more than once) are an array, in the
// order the code returned them.
func TestExecuteABAPResultTextIsAnArrayForSeveralValues(t *testing.T) {
	result := runExecuteAgainst(t, execRunResultXML("", wrappedResultAlert("first")+wrappedResultAlert("second")))

	want := []string{"first", "second"}
	if !reflect.DeepEqual(result.ResultText, want) {
		t.Fatalf("result_text = %#v, want %#v", result.ResultText, want)
	}
	if !reflect.DeepEqual(result.Output, want) {
		t.Fatalf("output = %#v, want %#v", result.Output, want)
	}
	if !strings.Contains(result.Message, "2 output(s)") {
		t.Fatalf("message does not count the values: %q", result.Message)
	}
}

// ABAP Unit may file the closing assertion on the class rather than on the
// method. The value is still the value, and a run that carried one must never
// be reported as having captured nothing.
func TestExecuteABAPNeverSaysNoOutputWhenAnAlertCarriedOne(t *testing.T) {
	result := runExecuteAgainst(t, execRunResultXML(wrappedResultAlert("42"), ""))

	if strings.Contains(result.Message, "no output captured") {
		t.Fatalf("the value was in the response and the message says otherwise: %q", result.Message)
	}
	if result.ResultText != "42" {
		t.Fatalf("result_text = %#v, want \"42\"", result.ResultText)
	}
}

// Lean drops the raw alerts once a value came back, and keeps them when they
// are the only evidence there is.
func TestExecuteABAPResultLeanKeepsAlertsOnlyWithoutAValue(t *testing.T) {
	withValue := ExecuteABAPResult{Output: []string{"x"}, RawAlerts: []UnitTestAlert{{Title: "t"}}}
	if got := withValue.Lean(); got.RawAlerts != nil {
		t.Fatalf("raw alerts were kept next to a value: %+v", got.RawAlerts)
	}
	without := ExecuteABAPResult{RawAlerts: []UnitTestAlert{{Title: "t"}}}
	if got := without.Lean(); len(got.RawAlerts) != 1 {
		t.Fatal("the only evidence of what happened was dropped")
	}
}

// RETURN_VALUE( ) hands back every value: each one is reported without
// leaving the method (quit = no), because the default quit ends the test
// method and every value after the first was lost.
func TestWrapperReturnsEveryValueWithoutLeavingTheMethod(t *testing.T) {
	source := executeWrapperSource("ZTEMP_EXEC_00000000", "RISK LEVEL HARMLESS", "lv_result", "RETURN_VALUE( 1 ).")
	for _, want := range []string{
		"METHODS return_value IMPORTING value TYPE any.",
		"METHOD return_value.",
		"IF mv_vsp_returned = abap_false OR lv_result IS NOT INITIAL.",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("the wrapper does not contain %q:\n%s", want, source)
		}
	}
	// Each value is emitted inside return_value itself, not collected and
	// emitted after the payload: code after the payload never runs when the
	// payload leaves early (RETURN, CHECK, an exception), and every value
	// collected so far was lost with it.
	_, body, found := strings.Cut(source, "METHOD return_value.")
	if !found {
		t.Fatalf("no return_value method:\n%s", source)
	}
	body, _, _ = strings.Cut(body, "ENDMETHOD.")
	if !strings.Contains(body, "cl_abap_unit_assert=>fail( msg = |"+execResultMarker+"{ lv_vsp_text }| quit = if_aunit_constants=>no )") {
		t.Errorf("return_value does not hand its value back at once:\n%s", body)
	}
	_, afterPayload, _ := strings.Cut(source, "=== USER CODE END ===")
	afterPayload, _, _ = strings.Cut(afterPayload, "ENDMETHOD.")
	if strings.Contains(afterPayload, "LOOP AT") {
		t.Errorf("values are still emitted after the payload:\n%s", afterPayload)
	}
	// The payload still starts on line 18, which the live fixtures elsewhere in
	// these tests were recorded against.
	if got := payloadOffset(source); got != 18 {
		t.Errorf("the payload moved to line %d of the wrapper", got)
	}
}
