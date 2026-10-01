package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// executeSAP stands in for SAP over the whole execute_abap flow and answers the
// test run with runResult. Everything else (create, write, unlock, activate,
// delete) succeeds without a body; the lock hands out a handle.
func executeSAP(t *testing.T, runResult string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-csrf-token", "AbCdEfGhIjKlMnOpQrStUv==")
		switch {
		case strings.Contains(r.URL.Path, "/abapunit/testruns"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(runResult))
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA><LOCK_HANDLE>H1</LOCK_HANDLE></DATA></asx:values></asx:abap>`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func execRunResult(methodAlerts string) string {
	return `<?xml version="1.0" encoding="utf-8"?>` +
		`<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">` +
		`<program adtcore:uri="/sap/bc/adt/programs/programs/ztemp_exec_1" adtcore:type="PROG/P" adtcore:name="ZTEMP_EXEC_1">` +
		`<testClasses><testClass adtcore:name="LTC_EXECUTOR" riskLevel="harmless"><testMethods>` +
		`<testMethod adtcore:name="EXECUTE_PAYLOAD" executionTime="0.01"><alerts>` + methodAlerts + `</alerts></testMethod>` +
		`</testMethods></testClass></testClasses></program></aunit:runResult>`
}

func sapAssertion(value string) string {
	return `<alert kind="failedAssertion" severity="critical">` +
		`<title>Critical Assertion Error: 'EXEC_RESULT:` + value + `'</title>` +
		`<details><detail text="Test 'LTC_EXECUTOR-&gt;EXECUTE_PAYLOAD' in Main Program 'ZTEMP_EXEC_1'"/></details>` +
		`</alert>`
}

// execute_abap answers JSON with result_text: the whole value, unwrapped from
// SAP's "Critical Assertion Error: '...'", and the existing fields alongside.
func TestExecuteABAPAnswersResultText(t *testing.T) {
	long := strings.Repeat("abcdefghij", 500)
	sap := executeSAP(t, execRunResult(sapAssertion(long)))
	s := NewServer(&Config{BaseURL: sap.URL, Username: "TESTUSER", Client: "001", Mode: "expert"})

	res, err := s.handleExecuteABAP(t.Context(), newRequest(map[string]any{"code": "lv_result = 'x'."}))
	if err != nil {
		t.Fatalf("handleExecuteABAP: %v", err)
	}
	text := resultText(res)
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("execute_abap did not answer JSON: %v\n%s", err, text)
	}
	if got["result_text"] != long {
		t.Fatalf("result_text is not the whole unwrapped value (%d chars expected):\n%.300s", len(long), text)
	}
	for _, field := range []string{"success", "programName", "output", "executionTime", "message", "cleanedUp"} {
		if _, ok := got[field]; !ok {
			t.Errorf("existing field %q is missing", field)
		}
	}
	if got["success"] != true {
		t.Errorf("success = %v", got["success"])
	}
	if strings.Contains(text, "Critical Assertion Error") {
		t.Errorf("SAP's wrapping leaked into the answer:\n%.500s", text)
	}
}

// Several RETURN_VALUE( ) values are an array.
func TestExecuteABAPAnswersAnArrayForSeveralValues(t *testing.T) {
	sap := executeSAP(t, execRunResult(sapAssertion("one")+sapAssertion("two")))
	s := NewServer(&Config{BaseURL: sap.URL, Username: "TESTUSER", Client: "001", Mode: "expert"})

	res, err := s.handleExecuteABAP(t.Context(), newRequest(map[string]any{"code": "RETURN_VALUE( 1 ). RETURN_VALUE( 2 )."}))
	if err != nil {
		t.Fatalf("handleExecuteABAP: %v", err)
	}
	var got struct {
		ResultText []string `json:"result_text"`
		Output     []string `json:"output"`
	}
	if err := json.Unmarshal([]byte(resultText(res)), &got); err != nil {
		t.Fatalf("not JSON, or result_text is not an array: %v\n%s", err, resultText(res))
	}
	if strings.Join(got.ResultText, ",") != "one,two" || strings.Join(got.Output, ",") != "one,two" {
		t.Fatalf("result_text %v, output %v; want [one two] for both", got.ResultText, got.Output)
	}
}

// A returned value comes back exactly as the code produced it, angle brackets
// and ampersands included.
func TestExecuteABAPKeepsTheValueVerbatim(t *testing.T) {
	sap := executeSAP(t, execRunResult(sapAssertion("&lt;b&gt;bold&lt;/b&gt; &amp; more")))
	s := NewServer(&Config{BaseURL: sap.URL, Username: "TESTUSER", Client: "001", Mode: "expert"})

	res, err := s.handleExecuteABAP(t.Context(), newRequest(map[string]any{"code": "lv_result = 'x'."}))
	if err != nil {
		t.Fatalf("handleExecuteABAP: %v", err)
	}
	if !strings.Contains(resultText(res), `"result_text": "<b>bold</b> & more"`) {
		t.Fatalf("the value was not passed on verbatim:\n%s", resultText(res))
	}
}
