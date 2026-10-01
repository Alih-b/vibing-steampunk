package mcp

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func sourcePuts(calls []updateSourceWireCall) []string {
	var paths []string
	for _, call := range calls {
		if call.method == http.MethodPut {
			paths = append(paths, call.path)
		}
	}
	return paths
}

// #242: hyperfocused `edit CLAS X` with include=testclasses used to drop the
// include and write the test class into the main source.
func TestHyperfocusedEditClassIncludeWritesTheInclude(t *testing.T) {
	cfg := updateSourceTestConfig()
	cfg.Mode = "hyperfocused"
	server, recorder := newUpdateSourceTestServer(t, cfg, "/sap/bc/adt/oo/classes/ZCL_PROBE", "ZCL_PROBE", "$TMP")

	result, err := server.handleUniversalTool(context.Background(), newRequest(map[string]any{
		"action": "edit",
		"target": "CLAS ZCL_PROBE",
		"params": map[string]any{
			"include": "testclasses",
			"source":  "CLASS ltcl_probe DEFINITION FOR TESTING. ENDCLASS.",
		},
	}))
	if err != nil {
		t.Fatalf("handleUniversalTool: %v", err)
	}
	text := toolResultText(t, result)
	if result.IsError {
		t.Fatalf("edit with include failed: %s", text)
	}

	calls := recorder.snapshot()
	puts := sourcePuts(calls)
	if len(puts) != 1 || puts[0] != "/sap/bc/adt/oo/classes/ZCL_PROBE/includes/testclasses" {
		t.Fatalf("PUTs = %v, want only the testclasses include; calls=%#v", puts, calls)
	}
	if !strings.Contains(text, `"include": "testclasses"`) {
		t.Errorf("result does not say which include was written: %s", text)
	}
}

func TestHyperfocusedEditClassUnknownIncludeIsRefused(t *testing.T) {
	cfg := updateSourceTestConfig()
	cfg.Mode = "hyperfocused"
	server, recorder := newUpdateSourceTestServer(t, cfg, "/sap/bc/adt/oo/classes/ZCL_PROBE", "ZCL_PROBE", "$TMP")

	result, err := server.handleUniversalTool(context.Background(), newRequest(map[string]any{
		"action": "edit",
		"target": "CLAS ZCL_PROBE",
		"params": map[string]any{
			"include": "testclass",
			"source":  "CLASS ltcl_probe DEFINITION FOR TESTING. ENDCLASS.",
		},
	}))
	if err != nil {
		t.Fatalf("handleUniversalTool: %v", err)
	}
	text := toolResultText(t, result)
	if !result.IsError || !strings.Contains(text, "unknown class include") {
		t.Fatalf("unknown include must be refused, got isError=%v: %s", result.IsError, text)
	}
	if calls := recorder.snapshot(); len(calls) != 0 {
		t.Fatalf("a refused include must not reach SAP; calls=%#v", calls)
	}
}

// #242: UPDATE_SOURCE appended /source/main to a class include URL (404), and
// locked the include rather than its class.
func TestHandleUpdateSource_ClassIncludeURL(t *testing.T) {
	tests := []struct {
		name      string
		objectURL string
		classURL  string
		putURL    string
	}{
		{
			name:      "plain class",
			objectURL: "/sap/bc/adt/oo/classes/zcl_probe/includes/testclasses",
			classURL:  "/sap/bc/adt/oo/classes/zcl_probe",
			putURL:    "/sap/bc/adt/oo/classes/zcl_probe/includes/testclasses",
		},
		{
			// #282: the escaped namespace is kept as given, not escaped again.
			name:      "namespaced class",
			objectURL: "/sap/bc/adt/oo/classes/%2FZDEMO%2FCL_PROBE/includes/definitions",
			classURL:  "/sap/bc/adt/oo/classes/%2FZDEMO%2FCL_PROBE",
			putURL:    "/sap/bc/adt/oo/classes/%2FZDEMO%2FCL_PROBE/includes/definitions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := updateSourceTestConfig()
			cfg.AllowedPackages = []string{"$TMP"}
			server, recorder := newUpdateSourceTestServer(t, cfg, tt.classURL, "ZCL_PROBE", "$TMP")

			result := updateSourceCall(t, server, map[string]any{
				"object_url": tt.objectURL,
				"source":     "CLASS ltcl_probe DEFINITION FOR TESTING. ENDCLASS.",
			})
			if result.isError {
				t.Fatalf("include update failed: %s", result.text)
			}

			calls := recorder.snapshot()
			puts := sourcePuts(calls)
			wantPut := strings.ReplaceAll(tt.putURL, "%2F", "/") // recorder keeps the decoded path
			if len(puts) != 1 || puts[0] != wantPut {
				t.Fatalf("PUTs = %v, want only %s; calls=%#v", puts, wantPut, calls)
			}
			wantLock := strings.ReplaceAll(tt.classURL, "%2F", "/")
			lock := indexUpdateSourceCall(calls, func(call updateSourceWireCall) bool { return call.isLock() })
			unlock := indexUpdateSourceCall(calls, func(call updateSourceWireCall) bool { return call.isUnlock() })
			search := indexUpdateSourceCall(calls, isPackageSearch)
			if lock < 0 || unlock < 0 || calls[lock].path != wantLock || calls[unlock].path != wantLock {
				t.Fatalf("want LOCK and UNLOCK on the class %s; calls=%#v", wantLock, calls)
			}
			if search < 0 || search > lock {
				t.Fatalf("package check must run before the lock; calls=%#v", calls)
			}
		})
	}
}

func TestHandleUpdateSource_UnknownClassIncludeIsRefused(t *testing.T) {
	cfg := updateSourceTestConfig()
	server, recorder := newUpdateSourceTestServer(t, cfg, "/sap/bc/adt/oo/classes/zcl_probe", "ZCL_PROBE", "$TMP")

	result := updateSourceCall(t, server, map[string]any{
		"object_url": "/sap/bc/adt/oo/classes/zcl_probe/includes/localtypes",
		"source":     "CLASS lcl DEFINITION. ENDCLASS.",
	})
	if !result.isError || !strings.Contains(result.text, "unknown class include") {
		t.Fatalf("unknown include must be refused, got isError=%v: %s", result.isError, result.text)
	}
	if calls := recorder.snapshot(); len(calls) != 0 {
		t.Fatalf("a refused include must not reach SAP; calls=%#v", calls)
	}
}

// Program includes do live under /source/main and must keep the suffix.
func TestHandleUpdateSource_ProgramIncludeKeepsSourceMain(t *testing.T) {
	const objectURL = "/sap/bc/adt/programs/includes/zdemo_incl"
	server, recorder := newUpdateSourceTestServer(t, updateSourceTestConfig(), objectURL, "ZDEMO_INCL", "$TMP")

	result := updateSourceCall(t, server, map[string]any{
		"object_url": objectURL,
		"source":     "WRITE 'x'.",
	})
	if result.isError {
		t.Fatalf("program include update failed: %s", result.text)
	}
	puts := sourcePuts(recorder.snapshot())
	if len(puts) != 1 || puts[0] != objectURL+"/source/main" {
		t.Fatalf("PUTs = %v, want %s/source/main", puts, objectURL)
	}
}
