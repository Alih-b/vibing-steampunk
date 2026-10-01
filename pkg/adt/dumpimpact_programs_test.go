package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// #281. A standalone program has no container but its package, so the
// where-used list files it under a DEVC row. Judging rows by their container
// dropped every program and every program include from the answer. These rows
// are shaped on a live answer for BAPI_USER_GET_DETAIL; the Z names are synthetic.
func programCallerRows() []UsageReference {
	return []UsageReference{
		{URI: "/sap/bc/adt/packages/%24zdemo", Name: "$ZDEMO", Type: "DEVC/K"},
		{
			URI:              "/sap/bc/adt/programs/programs/zdemo_report",
			ParentURI:        "/sap/bc/adt/packages/%24zdemo",
			Name:             "ZDEMO_REPORT",
			Type:             "PROG/P",
			UsageInformation: "gradeDirect,includeProductive",
			PackageName:      "$ZDEMO",
		},
		{URI: "/sap/bc/adt/packages/susr_cua_tools", Name: "SUSR_CUA_TOOLS", Type: "DEVC/K"},
		{
			URI:              "/sap/bc/adt/programs/includes/rscua_user_compare_init",
			ParentURI:        "/sap/bc/adt/packages/susr_cua_tools",
			Name:             "RSCUA_USER_COMPARE_INIT",
			Type:             "PROG/I",
			UsageInformation: "gradeDirect,includeProductive",
		},
		{URI: "/sap/bc/adt/functions/groups/ups_c", Name: "UPS_C", Type: "FUGR/F", PackageName: "UPS"},
		{
			URI:              "/sap/bc/adt/functions/groups/ups_c/fmodules/ups_send_start_mail",
			ParentURI:        "/sap/bc/adt/functions/groups/ups_c",
			Name:             "UPS_SEND_START_MAIL",
			Type:             "FUGR/FF",
			UsageInformation: "gradeDirect,includeProductive",
			PackageName:      "UPS",
		},
		{URI: "/sap/bc/adt/functions/groups/prgn_conversion", Name: "PRGN_CONVERSION", Type: "FUGR/F", PackageName: "S_PROFGEN"},
		{
			URI:              "/sap/bc/adt/programs/includes/lprgn_conversionf01",
			ParentURI:        "/sap/bc/adt/functions/groups/prgn_conversion",
			Name:             "LPRGN_CONVERSIONF01",
			Type:             "PROG/I",
			UsageInformation: "gradeDirect,includeProductive",
		},
		// A package interface is still not a caller, whatever its container.
		{
			URI:              "/sap/bc/adt/vit/wb/object_type/pinfki/object_name/SUSR_PUBLIC",
			ParentURI:        "/sap/bc/adt/packages/susr_cua_tools",
			Name:             "SUSR_PUBLIC",
			Type:             "PINF/KI",
			UsageInformation: "gradeDirect,includeProductive",
		},
	}
}

func callerNamed(callers []ExposedCaller, name string) (ExposedCaller, bool) {
	for _, c := range callers {
		if c.Name == name {
			return c, true
		}
	}
	return ExposedCaller{}, false
}

func TestExposedCallersKeepsProgramsFiledUnderAPackage(t *testing.T) {
	callers := exposedCallers(programCallerRows(), "BAPI_USER_GET_DETAIL")

	prog, ok := callerNamed(callers, "ZDEMO_REPORT")
	if !ok {
		t.Fatalf("the program under a package was dropped: %+v", callers)
	}
	if prog.Type != "PROG/P" || prog.URI != "/sap/bc/adt/programs/programs/zdemo_report" || prog.Package != "$ZDEMO" {
		t.Errorf("the program should be reported as itself, got %+v", prog)
	}

	incl, ok := callerNamed(callers, "RSCUA_USER_COMPARE_INIT")
	if !ok {
		t.Fatalf("the program include under a package was dropped: %+v", callers)
	}
	if incl.Type != "PROG/I" || incl.Package != "SUSR_CUA_TOOLS" {
		t.Errorf("the include should carry its own type and its package, got %+v", incl)
	}

	for _, c := range callers {
		if c.Name == "$ZDEMO" || c.Name == "SUSR_CUA_TOOLS" || c.Name == "SUSR_PUBLIC" {
			t.Errorf("a package or package interface was reported as a caller: %+v", c)
		}
	}
}

// A module has its own address and its own where-used list; reporting its
// group instead turns an answer into a search.
func TestExposedCallersNamesTheFunctionModule(t *testing.T) {
	callers := exposedCallers(programCallerRows(), "BAPI_USER_GET_DETAIL")

	fm, ok := callerNamed(callers, "UPS_SEND_START_MAIL")
	if !ok {
		t.Fatalf("the function module was not reported as a caller: %+v", callers)
	}
	if fm.Type != "FUGR/FF" || !strings.HasSuffix(fm.URI, "/fmodules/ups_send_start_mail") || fm.Package != "UPS" {
		t.Errorf("the module should be reported as itself, got %+v", fm)
	}
	if _, ok := callerNamed(callers, "UPS_C"); ok {
		t.Errorf("the group was reported for a reference that sits in one of its modules: %+v", callers)
	}

	// An include of a group is the group's code, and the group is its program.
	grp, ok := callerNamed(callers, "PRGN_CONVERSION")
	if !ok || grp.Type != "FUGR/F" || grp.Component != "LPRGN_CONVERSIONF01" {
		t.Errorf("a function group include should be reported as its group, got %+v (%v)", grp, ok)
	}
}

// A module that is on the dump's stack is the route the dump took, and must be
// recognised as such now that the module, not its group, is the caller.
func TestRankExposureRecognisesAModuleOnTheStack(t *testing.T) {
	dump := Dump{Program: "SAPLSU_USER"}
	stack := []DumpFrame{
		{Position: 2, Type: "FUNCTION", Program: "SAPLSU_USER", Name: "BAPI_USER_GET_DETAIL"},
		{Position: 1, Type: "FUNCTION", Program: "SAPLUPS_C", Name: "UPS_SEND_START_MAIL"},
	}
	units := []ImpactUnit{{Object: "BAPI_USER_GET_DETAIL", Callers: []ExposedCaller{
		{Name: "UPS_SEND_START_MAIL", Type: "FUGR/FF"},
		{Name: "ZDEMO_REPORT", Type: "PROG/P"},
	}}}

	exposed, onPath := rankExposure(units, dump, stack)
	if len(onPath) != 1 || onPath[0].Name != "UPS_SEND_START_MAIL" {
		t.Errorf("onPath = %+v, want the module that is on the stack", onPath)
	}
	if len(exposed) != 1 || exposed[0].Name != "ZDEMO_REPORT" {
		t.Errorf("exposed = %+v, want only the program", exposed)
	}
}

const programCallersXML = `<?xml version="1.0" encoding="utf-8"?>
<usageReferences:usageReferenceResult xmlns:usageReferences="http://www.sap.com/adt/ris/usageReferences" xmlns:adtcore="http://www.sap.com/adt/core">
<usageReferences:referencedObjects>
<usageReferences:referencedObject usageReferences:uri="/sap/bc/adt/packages/susr_cua_tools" usageReferences:isResult="false" usageReferences:canHaveChildren="true">
  <usageReferences:adtObject adtcore:name="SUSR_CUA_TOOLS" adtcore:type="DEVC/K"/>
</usageReferences:referencedObject>
<usageReferences:referencedObject usageReferences:uri="/sap/bc/adt/programs/includes/rscua_user_compare_init" usageReferences:parentUri="/sap/bc/adt/packages/susr_cua_tools" usageReferences:isResult="true" usageReferences:usageInformation="gradeDirect,includeProductive">
  <usageReferences:adtObject adtcore:name="RSCUA_USER_COMPARE_INIT" adtcore:type="PROG/I"><adtcore:packageRef adtcore:name="SUSR_CUA_TOOLS"/></usageReferences:adtObject>
</usageReferences:referencedObject>
<usageReferences:referencedObject usageReferences:uri="/sap/bc/adt/programs/includes/rscua_user_compare_exc_us_ini" usageReferences:parentUri="/sap/bc/adt/packages/susr_cua_tools" usageReferences:isResult="true" usageReferences:usageInformation="gradeDirect,includeProductive">
  <usageReferences:adtObject adtcore:name="RSCUA_USER_COMPARE_EXC_US_INI" adtcore:type="PROG/I"><adtcore:packageRef adtcore:name="SUSR_CUA_TOOLS"/></usageReferences:adtObject>
</usageReferences:referencedObject>
<usageReferences:referencedObject usageReferences:uri="/sap/bc/adt/programs/includes/zdemo_orphan_incl" usageReferences:parentUri="/sap/bc/adt/packages/susr_cua_tools" usageReferences:isResult="true" usageReferences:usageInformation="gradeDirect,includeProductive">
  <usageReferences:adtObject adtcore:name="ZDEMO_ORPHAN_INCL" adtcore:type="PROG/I"><adtcore:packageRef adtcore:name="SUSR_CUA_TOOLS"/></usageReferences:adtObject>
</usageReferences:referencedObject>
<usageReferences:referencedObject usageReferences:uri="/sap/bc/adt/programs/programs/zdemo_report" usageReferences:parentUri="/sap/bc/adt/packages/susr_cua_tools" usageReferences:isResult="true" usageReferences:usageInformation="gradeDirect,includeProductive">
  <usageReferences:adtObject adtcore:name="ZDEMO_REPORT" adtcore:type="PROG/P"><adtcore:packageRef adtcore:name="$ZDEMO"/></usageReferences:adtObject>
</usageReferences:referencedObject>
</usageReferences:referencedObjects>
</usageReferences:usageReferenceResult>`

const compareMainProgramXML = `<?xml version="1.0" encoding="utf-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core"><adtcore:objectReference adtcore:uri="/sap/bc/adt/programs/programs/rscua_user_compare" adtcore:type="PROG/P" adtcore:name="RSCUA_USER_COMPARE"/></adtcore:objectReferences>`

// Through the client, the way graph, explain, the MCP tool and dumps --impact
// all reach it: includes come back as the program that runs them.
func TestWhereUsedResolvesIncludesToTheirMainProgram(t *testing.T) {
	var mu sync.Mutex
	var lookups []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-csrf-token", "test-token")
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "usageReferences"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(programCallersXML))
		case strings.HasSuffix(r.URL.Path, "/mainprograms"):
			mu.Lock()
			lookups = append(lookups, r.URL.Path)
			mu.Unlock()
			// What the live system does with any other Accept header.
			if r.Header.Get("Accept") != includeMainProgramsAccept {
				w.WriteHeader(http.StatusNotAcceptable)
				return
			}
			if strings.Contains(r.URL.Path, "zdemo_orphan_incl") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(compareMainProgramXML))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "user", "pass")
	callers, unresolved, err := client.WhereUsed(context.Background(), "/sap/bc/adt/functions/groups/su_user/fmodules/bapi_user_get_detail")
	if err != nil {
		t.Fatalf("WhereUsed: %v", err)
	}

	// The 404 is a gap: the include is listed, and the answer says why.
	if len(unresolved) != 1 || unresolved[0].Object != "ZDEMO_ORPHAN_INCL" {
		t.Errorf("unresolved = %+v, want the include whose lookup 404'd", unresolved)
	}
	if len(lookups) != 3 {
		t.Errorf("want one main-program lookup per include, got %v", lookups)
	}

	main, ok := callerNamed(callers, "RSCUA_USER_COMPARE")
	if !ok {
		t.Fatalf("the includes were not resolved to their program: %+v", callers)
	}
	if main.Type != "PROG/P" || main.URI != "/sap/bc/adt/programs/programs/rscua_user_compare" || main.Package != "SUSR_CUA_TOOLS" {
		t.Errorf("resolved program = %+v", main)
	}
	// Two includes of one program are one caller, with both includes named.
	if main.Component != "RSCUA_USER_COMPARE_EXC_US_INI, RSCUA_USER_COMPARE_INIT" &&
		main.Component != "RSCUA_USER_COMPARE_INIT, RSCUA_USER_COMPARE_EXC_US_INI" {
		t.Errorf("component = %q, want both includes", main.Component)
	}
	if _, ok := callerNamed(callers, "RSCUA_USER_COMPARE_INIT"); ok {
		t.Errorf("a resolved include was still listed as itself: %+v", callers)
	}

	// An include whose program cannot be read stays, as itself.
	orphan, ok := callerNamed(callers, "ZDEMO_ORPHAN_INCL")
	if !ok || orphan.Type != "PROG/I" {
		t.Errorf("an unresolvable include was dropped or mislabelled: %+v", callers)
	}
	if _, ok := callerNamed(callers, "ZDEMO_REPORT"); !ok {
		t.Errorf("the program under a package was dropped: %+v", callers)
	}
	if len(callers) != 3 {
		t.Errorf("got %d callers, want 3 (RSCUA_USER_COMPARE, ZDEMO_REPORT, ZDEMO_ORPHAN_INCL): %+v", len(callers), callers)
	}
}
