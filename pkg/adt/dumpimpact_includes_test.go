package adt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// includeRowsXML is a where-used answer of n program includes filed under a
// package, named ZDEMO_INCL_000 upward.
func includeRowsXML(n int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` +
		`<usageReferences:usageReferenceResult xmlns:usageReferences="http://www.sap.com/adt/ris/usageReferences" xmlns:adtcore="http://www.sap.com/adt/core">` +
		`<usageReferences:referencedObjects>` +
		`<usageReferences:referencedObject usageReferences:uri="/sap/bc/adt/packages/%24zdemo" usageReferences:isResult="false">` +
		`<usageReferences:adtObject adtcore:name="$ZDEMO" adtcore:type="DEVC/K"/></usageReferences:referencedObject>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<usageReferences:referencedObject usageReferences:uri="/sap/bc/adt/programs/includes/zdemo_incl_%03d" `+
			`usageReferences:parentUri="/sap/bc/adt/packages/%%24zdemo" usageReferences:isResult="true" usageReferences:usageInformation="gradeDirect,includeProductive">`+
			`<usageReferences:adtObject adtcore:name="ZDEMO_INCL_%03d" adtcore:type="PROG/I"><adtcore:packageRef adtcore:name="$ZDEMO"/></usageReferences:adtObject>`+
			`</usageReferences:referencedObject>`, i, i)
	}
	b.WriteString(`</usageReferences:referencedObjects></usageReferences:usageReferenceResult>`)
	return b.String()
}

// mainProgramXML answers a mainprograms lookup with the given programs.
func mainProgramXML(names ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">`)
	for _, n := range names {
		fmt.Fprintf(&b, `<adtcore:objectReference adtcore:uri="/sap/bc/adt/programs/programs/%s" adtcore:type="PROG/P" adtcore:name="%s"/>`,
			strings.ToLower(n), n)
	}
	b.WriteString(`</adtcore:objectReferences>`)
	return b.String()
}

// mainProgramsServer answers the where-used POST with refs and every mainprograms
// GET with lookup(include name in lower case).
func mainProgramsServer(t *testing.T, refs string, lookup func(w http.ResponseWriter, include string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-csrf-token", "test-token")
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "usageReferences"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(refs))
		case strings.HasSuffix(r.URL.Path, "/mainprograms"):
			include := strings.TrimSuffix(r.URL.Path, "/mainprograms")
			include = include[strings.LastIndex(include, "/")+1:]
			lookup(w, include)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
}

const fmURI = "/sap/bc/adt/functions/groups/zdemo_fg/fmodules/zdemo_fm"

// Past the cap an include stays as itself, and the answer says which ones and
// why. Before, they were passed on silently and the answer read as whole.
func TestWhereUsedNamesTheIncludesLeftOverByTheCap(t *testing.T) {
	var lookups atomic.Int32
	srv := mainProgramsServer(t, includeRowsXML(maxIncludeLookups+2), func(w http.ResponseWriter, include string) {
		lookups.Add(1)
		_, _ = w.Write([]byte(mainProgramXML("ZDEMO_PROG_" + strings.ToUpper(include[len(include)-3:]))))
	})
	defer srv.Close()

	callers, unresolved, err := NewClient(srv.URL, "user", "pass").WhereUsed(context.Background(), fmURI)
	if err != nil {
		t.Fatalf("WhereUsed: %v", err)
	}
	if got := lookups.Load(); got != maxIncludeLookups {
		t.Errorf("made %d lookups, want the cap of %d", got, maxIncludeLookups)
	}
	if len(unresolved) != 2 {
		t.Fatalf("unresolved = %+v, want the two includes past the cap", unresolved)
	}
	for _, u := range unresolved {
		if !strings.Contains(u.Reason, "cap") {
			t.Errorf("reason %q does not say the cap was reached", u.Reason)
		}
		if _, ok := callerNamed(callers, u.Object); !ok {
			t.Errorf("%s was named as unresolved but dropped from the callers", u.Object)
		}
	}
	if len(callers) != maxIncludeLookups+2 {
		t.Errorf("got %d callers, want %d", len(callers), maxIncludeLookups+2)
	}
	if note := UnresolvedIncludesNote(unresolved); !strings.Contains(note, "2 program includes") {
		t.Errorf("note = %q", note)
	}
}

// A failed lookup leaves the include as itself and says so.
func TestWhereUsedReportsAFailedIncludeLookup(t *testing.T) {
	srv := mainProgramsServer(t, includeRowsXML(2), func(w http.ResponseWriter, include string) {
		if include == "zdemo_incl_001" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		_, _ = w.Write([]byte(mainProgramXML("ZDEMO_PROG")))
	})
	defer srv.Close()

	callers, unresolved, err := NewClient(srv.URL, "user", "pass").WhereUsed(context.Background(), fmURI)
	if err != nil {
		t.Fatalf("a failed include lookup is a gap, not a failed answer: %v", err)
	}
	if len(unresolved) != 1 || unresolved[0].Object != "ZDEMO_INCL_001" || !strings.Contains(unresolved[0].Reason, "500") {
		t.Fatalf("unresolved = %+v, want ZDEMO_INCL_001 with the 500", unresolved)
	}
	if c, ok := callerNamed(callers, "ZDEMO_INCL_001"); !ok || c.Type != "PROG/I" {
		t.Errorf("the include whose lookup failed should stay, as itself: %+v", callers)
	}
	if _, ok := callerNamed(callers, "ZDEMO_PROG"); !ok {
		t.Errorf("the include that did resolve is missing its program: %+v", callers)
	}
	if note := UnresolvedIncludesNote(unresolved); !strings.Contains(note, "ZDEMO_INCL_001") {
		t.Errorf("note = %q, want the include named", note)
	}
}

// Out of time is a failed answer, not a gap: a run that timed out must not
// read as a complete one.
func TestWhereUsedReturnsTheContextErrorWhenCancelledMidLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := mainProgramsServer(t, includeRowsXML(3), func(w http.ResponseWriter, include string) {
		cancel()
		_, _ = w.Write([]byte(mainProgramXML("ZDEMO_PROG")))
	})
	defer srv.Close()

	callers, _, err := NewClient(srv.URL, "user", "pass").WhereUsed(ctx, fmURI)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if callers != nil {
		t.Errorf("a cancelled answer returned callers as if it were whole: %+v", callers)
	}
}

// The same cancellation on the dump path stops the whole query rather than
// filing it under one unit.
func TestDumpImpactStopsOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := mainProgramsServer(t, includeRowsXML(1), func(w http.ResponseWriter, include string) {
		cancel()
		_, _ = w.Write([]byte(mainProgramXML("ZDEMO_PROG")))
	})
	defer srv.Close()

	_, err := NewClient(srv.URL, "user", "pass").DumpImpact(ctx, Dump{Program: "ZDEMO_REPORT"}, DumpImpactOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// On the dump path the gap lands on the unit, beside its callers, and does not
// mark the unit as unasked.
func TestDumpImpactPutsUnresolvedIncludesOnTheUnit(t *testing.T) {
	srv := mainProgramsServer(t, includeRowsXML(1), func(w http.ResponseWriter, include string) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer srv.Close()

	res, err := NewClient(srv.URL, "user", "pass").DumpImpact(context.Background(), Dump{Program: "ZDEMO_REPORT"}, DumpImpactOptions{})
	if err != nil {
		t.Fatalf("DumpImpact: %v", err)
	}
	u := res.Units[0]
	if len(u.Unresolved) != 1 || !strings.Contains(u.Gap, "ZDEMO_INCL_000") {
		t.Errorf("unit = %+v, want the unresolved include named in Gap", u)
	}
	if u.Note != "" || !res.Answerable() {
		t.Errorf("a unit with an unresolved include was still asked and answered: %+v", u)
	}
}

// Lookups run in parallel, but no more than includeLookupWorkers at once.
func TestIncludeLookupsAreBoundedInParallel(t *testing.T) {
	var inFlight, peak atomic.Int32
	srv := mainProgramsServer(t, includeRowsXML(12), func(w http.ResponseWriter, include string) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		_, _ = w.Write([]byte(mainProgramXML("ZDEMO_PROG")))
	})
	defer srv.Close()

	if _, _, err := NewClient(srv.URL, "user", "pass").WhereUsed(context.Background(), fmURI); err != nil {
		t.Fatalf("WhereUsed: %v", err)
	}
	if p := peak.Load(); p < 2 || p > includeLookupWorkers {
		t.Errorf("peak concurrency %d, want between 2 and %d", p, includeLookupWorkers)
	}
}

// Units of one dump that share an include ask about it once.
func TestIncludeResolverAsksAboutAnIncludeOnce(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]int{}
	srv := mainProgramsServer(t, "", func(w http.ResponseWriter, include string) {
		mu.Lock()
		asked[include]++
		mu.Unlock()
		_, _ = w.Write([]byte(mainProgramXML("ZDEMO_PROG")))
	})
	defer srv.Close()

	r := NewClient(srv.URL, "user", "pass").newIncludeResolver()
	incl := []ExposedCaller{{Name: "ZDEMO_INCL", Type: "PROG/I", URI: "/sap/bc/adt/programs/includes/zdemo_incl"}}
	for i := 0; i < 2; i++ {
		got, gaps, err := r.resolve(context.Background(), incl, "ZDEMO_TARGET")
		if err != nil || len(gaps) != 0 || len(got) != 1 || got[0].Name != "ZDEMO_PROG" {
			t.Fatalf("round %d: %+v %+v %v", i, got, gaps, err)
		}
	}
	if asked["zdemo_incl"] != 1 {
		t.Errorf("the include was asked about %d times, want once", asked["zdemo_incl"])
	}
}

// An include in several programs is a caller through each of them.
func TestAnIncludeInSeveralProgramsListsThemAll(t *testing.T) {
	srv := mainProgramsServer(t, includeRowsXML(1), func(w http.ResponseWriter, include string) {
		_, _ = w.Write([]byte(mainProgramXML("ZDEMO_PROG_A", "ZDEMO_PROG_B")))
	})
	defer srv.Close()

	callers, _, err := NewClient(srv.URL, "user", "pass").WhereUsed(context.Background(), fmURI)
	if err != nil {
		t.Fatalf("WhereUsed: %v", err)
	}
	if len(callers) != 2 || callers[0].Name != "ZDEMO_PROG_A" || callers[1].Name != "ZDEMO_PROG_B" {
		t.Fatalf("callers = %+v, want both programs, in order", callers)
	}
	for _, c := range callers {
		if c.Component != "ZDEMO_INCL_000" {
			t.Errorf("%s should name the include as its component, got %q", c.Name, c.Component)
		}
	}
}

// An object with productive and test references is productive exposure,
// whichever row came first.
func TestMergedCallerIsATestOnlyIfEveryRowIs(t *testing.T) {
	rows := func(first, second string) []UsageReference {
		return []UsageReference{
			{URI: "/sap/bc/adt/oo/classes/zcl_demo_caller", Name: "ZCL_DEMO_CALLER", Type: "CLAS/OC"},
			{
				URI: "/sap/bc/adt/oo/classes/zcl_demo_caller/source/main#name=A", ParentURI: "/sap/bc/adt/oo/classes/zcl_demo_caller",
				Name: "A", UsageInformation: "gradeDirect," + first,
			},
			{
				URI: "/sap/bc/adt/oo/classes/zcl_demo_caller/source/main#name=B", ParentURI: "/sap/bc/adt/oo/classes/zcl_demo_caller",
				Name: "B", UsageInformation: "gradeDirect," + second,
			},
		}
	}
	for _, tc := range []struct {
		first, second string
		wantTest      bool
	}{
		{"includeTest", "includeProductive", false},
		{"includeProductive", "includeTest", false},
		{"includeTest", "includeTest", true},
	} {
		got := exposedCallers(rows(tc.first, tc.second), "ZCL_DEMO_TARGET")
		if len(got) != 1 || got[0].IsTest != tc.wantTest {
			t.Errorf("%s then %s: got %+v, want one caller with IsTest=%v", tc.first, tc.second, got, tc.wantTest)
		}
	}
}

// A row that is its own caller carries its address, not a position in it.
func TestAStandaloneCallerDropsTheFragmentFromItsURI(t *testing.T) {
	if got := addressOf("/sap/bc/adt/programs/programs/zdemo_report/source/main#start=10,2"); got != "/sap/bc/adt/programs/programs/zdemo_report/source/main" {
		t.Errorf("addressOf = %q", got)
	}
	got := exposedCallers([]UsageReference{
		{URI: "/sap/bc/adt/packages/%24zdemo", Name: "$ZDEMO", Type: "DEVC/K"},
		{
			URI:       "/sap/bc/adt/programs/programs/zdemo_report#start=10,2",
			ParentURI: "/sap/bc/adt/packages/%24zdemo", Name: "ZDEMO_REPORT", Type: "PROG/P",
			UsageInformation: "gradeDirect,includeProductive",
		},
	}, "ZDEMO_TARGET")
	if len(got) != 1 || got[0].URI != "/sap/bc/adt/programs/programs/zdemo_report" {
		t.Errorf("callers = %+v, want the program's URI without its fragment", got)
	}
}

// A package interface that arrives without a type is still recognised by its
// address, and is not a caller.
func TestAnUntypedPackageInterfaceIsDroppedByItsAddress(t *testing.T) {
	got := exposedCallers([]UsageReference{
		{URI: "/sap/bc/adt/packages/zdemo", Name: "ZDEMO", Type: "DEVC/K"},
		{
			URI:       "/sap/bc/adt/vit/wb/object_type/pinfki/object_name/ZDEMO_PUBLIC",
			ParentURI: "/sap/bc/adt/packages/zdemo", Name: "ZDEMO_PUBLIC",
			UsageInformation: "gradeDirect,includeProductive",
		},
	}, "ZDEMO_TARGET")
	if len(got) != 0 {
		t.Errorf("an untyped package interface was reported as a caller: %+v", got)
	}
}
