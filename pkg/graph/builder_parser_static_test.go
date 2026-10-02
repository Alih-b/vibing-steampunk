package graph

import (
	"strings"
	"testing"
)

// Measured, not guessed: on one real class this parser found six dependencies
// where the other parser in this repo found nine, and the three it missed were
// these three shapes. Two capabilities reach the two parsers and are supposed
// to agree about the same object.

func depNames(src string) map[string]bool {
	out := map[string]bool{}
	for _, e := range ExtractDepsFromSource(src, "CLAS:ZCL_DEMO") {
		if p := strings.SplitN(e.To, ":", 2); len(p) == 2 {
			out[p[1]] = true
		}
	}
	return out
}

func TestAStaticCallInAnAssignmentIsADependency(t *testing.T) {
	// The functional style, which is how modern ABAP is written and which this
	// parser saw none of: only a bare CALL was classified as a Call.
	got := depNames(`lt_types = zcl_demo_objects=>supported_list( ).`)
	if !got["ZCL_DEMO_OBJECTS"] {
		t.Errorf("the class on the left of => is named by this statement, got %v", got)
	}
}

func TestAChainedStaticCallNamesOnlyTheClass(t *testing.T) {
	// zcl_a=>get( )->use( ): the -> half is an instance, not an object name,
	// and inventing one from a variable is the defect class this repo ranks
	// worst.
	got := depNames(`DATA(ls) = zcl_demo_factory=>get_tadir( )->read_single( iv_x = 1 ).`)
	if !got["ZCL_DEMO_FACTORY"] {
		t.Errorf("the static half names a class, got %v", got)
	}
	for name := range got {
		if strings.Contains(name, "READ_SINGLE") || strings.Contains(name, "GET_TADIR") {
			t.Errorf("a method name is not an object: %v", got)
		}
	}
}

func TestCaughtExceptionsAreDependencies(t *testing.T) {
	got := depNames(`CATCH zcx_demo_exception cx_root INTO DATA(lx).`)
	for _, want := range []string{"ZCX_DEMO_EXCEPTION", "CX_ROOT"} {
		if !got[want] {
			t.Errorf("%s is handled here and must be a dependency, got %v", want, got)
		}
	}
	if got["LX"] {
		t.Errorf("the variable after INTO is not an exception class: %v", got)
	}
}

func TestADeclaredExceptionIsADependencyEvenIfNothingRaisesIt(t *testing.T) {
	got := depNames(`METHODS run RAISING zcx_demo_exception.`)
	if !got["ZCX_DEMO_EXCEPTION"] {
		t.Errorf("the signature names it, so the class depends on it, got %v", got)
	}
}

func TestAnInstanceCallDoesNotInventAClass(t *testing.T) {
	// lo_ref->method( ): lo_ref is a variable. Reading it as an object name
	// would put a name nothing can resolve into the graph, and from there into
	// a boundary verdict.
	got := depNames(`lv_x = lo_ref->do_something( ).`)
	if got["LO_REF"] {
		t.Errorf("a variable is not an object: %v", got)
	}
}

func TestAddSourceDepsAddsEdgesAndTargets(t *testing.T) {
	g := New()
	from := NodeID("PROG", "ZDEMO_REPORT")
	g.AddNode(&Node{ID: from, Name: "ZDEMO_REPORT", Type: "PROG", Package: "$ZDEMO"})
	g.AddSourceDeps(from, "CALL FUNCTION 'Z_DEMO_FM'.\nCALL FUNCTION lv_fm.\nDATA lo TYPE REF TO zcl_demo_other.")

	if n := g.GetNode(from); n.Package != "$ZDEMO" {
		t.Errorf("the source node lost its package: %+v", n)
	}
	for id, typ := range map[string]string{"FUGR:Z_DEMO_FM": "FUGR", "CLAS:ZCL_DEMO_OTHER": "CLAS", "DYNAMIC:lv_fm": "DYNAMIC"} {
		n := g.GetNode(id)
		if n == nil || n.Type != typ || n.Package != "" {
			t.Errorf("target %s = %+v", id, n)
		}
	}
	if len(g.Edges()) != 3 {
		t.Errorf("edges = %d, want 3", len(g.Edges()))
	}
}
