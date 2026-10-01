package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// Callers reach usage examples through the where-used list. Since #281 a
// function module arrives as itself, namespaced ones escaped in the URI, and
// an include whose program could not be read arrives as an include. Neither
// may be dropped or renamed on the way to a source read.
func TestUsageTypeNameFromURIReadsModulesAndIncludes(t *testing.T) {
	cases := []struct {
		uri, name                   string
		wantType, wantName, wantGrp string
	}{
		{
			uri:      "/sap/bc/adt/functions/groups/%2fsdf%2fewa/fmodules/%2fsdf%2fewa_sdccn",
			name:     "/SDF/EWA_SDCCN",
			wantType: "FUNC", wantName: "/SDF/EWA_SDCCN", wantGrp: "/SDF/EWA",
		},
		{
			uri:      "/sap/bc/adt/functions/groups/ups_c/fmodules/ups_send_start_mail",
			name:     "UPS_SEND_START_MAIL",
			wantType: "FUNC", wantName: "UPS_SEND_START_MAIL", wantGrp: "UPS_C",
		},
		{
			uri:      "/sap/bc/adt/programs/includes/zdemo_orphan_incl",
			name:     "ZDEMO_ORPHAN_INCL",
			wantType: "INCL", wantName: "ZDEMO_ORPHAN_INCL",
		},
		{
			uri:      "/sap/bc/adt/programs/programs/zdemo_report",
			name:     "ZDEMO_REPORT",
			wantType: "PROG", wantName: "ZDEMO_REPORT",
		},
	}
	for _, tc := range cases {
		typ, name, grp := usageTypeNameFromURI(tc.uri, tc.name)
		if typ != tc.wantType || name != tc.wantName || grp != tc.wantGrp {
			t.Errorf("%s: got (%q, %q, %q), want (%q, %q, %q)",
				tc.uri, typ, name, grp, tc.wantType, tc.wantName, tc.wantGrp)
		}
	}
}

// An include whose main program could not be read is in the callers list as
// itself, and the answer says so beside it rather than reading as whole.
func TestCallersAnswerSaysWhichIncludesStayedUnresolved(t *testing.T) {
	srv := unresolvedIncludeServer(t)
	defer srv.Close()

	s := &Server{adtClient: adt.NewClient(srv.URL, "user", "pass")}
	var req mcp.CallToolRequest
	req.Params.Arguments = map[string]any{"object_uri": "/sap/bc/adt/functions/groups/zdemo_fg/fmodules/zdemo_fm"}
	answer, err := s.callGraphAnswer(context.Background(), req, "callers")
	if err != nil {
		t.Fatalf("callGraphAnswer: %v", err)
	}
	if answer["total"] != 1 {
		t.Errorf("total = %v, want the include counted as a caller", answer["total"])
	}
	gap, _ := answer["gap"].(string)
	if !strings.Contains(gap, "ZDEMO_INCL") {
		t.Errorf("gap = %q, want the unresolved include named", gap)
	}
	if u, _ := answer["unresolved_includes"].([]adt.Unsearched); len(u) != 1 {
		t.Errorf("unresolved_includes = %v", answer["unresolved_includes"])
	}
}

// unresolvedIncludeServer answers a where-used list holding one program
// include, and refuses every lookup of its main program with a 500.
func unresolvedIncludeServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-csrf-token", "test-token")
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "usageReferences"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>` +
				`<usageReferences:usageReferenceResult xmlns:usageReferences="http://www.sap.com/adt/ris/usageReferences" xmlns:adtcore="http://www.sap.com/adt/core"><usageReferences:referencedObjects>` +
				`<usageReferences:referencedObject usageReferences:uri="/sap/bc/adt/packages/%24zdemo" usageReferences:isResult="false"><usageReferences:adtObject adtcore:name="$ZDEMO" adtcore:type="DEVC/K"/></usageReferences:referencedObject>` +
				`<usageReferences:referencedObject usageReferences:uri="/sap/bc/adt/programs/includes/zdemo_incl" usageReferences:parentUri="/sap/bc/adt/packages/%24zdemo" usageReferences:isResult="true" usageReferences:usageInformation="gradeDirect,includeProductive">` +
				`<usageReferences:adtObject adtcore:name="ZDEMO_INCL" adtcore:type="PROG/I"/></usageReferences:referencedObject>` +
				`</usageReferences:referencedObjects></usageReferences:usageReferenceResult>`))
		case strings.HasSuffix(r.URL.Path, "/mainprograms"):
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
}

// analyze type=call_graph is the other route to the same callers, and it
// must carry the same gap rather than show the include as a plain edge.
func TestAnalyzeCallGraphSaysWhichIncludesStayedUnresolved(t *testing.T) {
	srv := unresolvedIncludeServer(t)
	defer srv.Close()

	s := &Server{adtClient: adt.NewClient(srv.URL, "user", "pass")}
	var req mcp.CallToolRequest
	req.Params.Arguments = map[string]any{
		"object_uri": "/sap/bc/adt/functions/groups/zdemo_fg/fmodules/zdemo_fm",
		"direction":  "callers",
	}
	result, err := s.handleAnalyzeCallGraph(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAnalyzeCallGraph: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(toolResultText(t, result)), &out); err != nil {
		t.Fatalf("the answer should be JSON: %v", err)
	}
	if gap, _ := out["gap"].(string); !strings.Contains(gap, "ZDEMO_INCL") || !strings.Contains(gap, "program includes are listed as themselves") {
		t.Errorf("gap = %v, want the unresolved include named", out["gap"])
	}
	if u, _ := out["unsearched"].([]any); len(u) != 1 {
		t.Errorf("unsearched = %v, want the one include", out["unsearched"])
	}
}

// A dump impact answer whose units hold unresolved includes says so at the
// top, beside the notes a reader reads first.
func TestImpactNotesNameUnitsWithUnresolvedIncludes(t *testing.T) {
	result := &adt.DumpImpactResult{Units: []adt.ImpactUnit{
		{Object: "ZDEMO_FM", Unresolved: []adt.Unsearched{{Object: "ZDEMO_INCL", Reason: "status 500"}}},
		{Object: "ZDEMO_OTHER"},
	}}
	joined := strings.Join(impactNotes(result), "\n")
	if !strings.Contains(joined, "1 of 2 units") || !strings.Contains(joined, "ZDEMO_FM (1)") {
		t.Errorf("notes do not name the unit with unresolved includes:\n%s", joined)
	}
	if strings.Contains(joined, "ZDEMO_OTHER") {
		t.Errorf("a unit with nothing unresolved was named:\n%s", joined)
	}
}
