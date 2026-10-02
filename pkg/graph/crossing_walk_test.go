package graph

import (
	"reflect"
	"strings"
	"testing"
)

func walkScope() *PackageScope {
	pkgs := []string{"$ZROOT", "$ZROOT_01", "$ZROOT_02", "$ZROOT_TEST"}
	s := &PackageScope{RootPackage: "$ZROOT", Packages: pkgs, PackageSet: map[string]bool{}, Hierarchy: map[string]string{}}
	for _, p := range pkgs {
		s.PackageSet[p] = true
		if p != "$ZROOT" {
			s.Hierarchy[p] = "$ZROOT"
		}
	}
	return s
}

func node(g *Graph, id, pkg string) {
	parts := strings.SplitN(id, ":", 2)
	g.AddNode(&Node{ID: id, Name: parts[1], Type: parts[0], Package: pkg})
}

func call(g *Graph, from, to string) { g.AddEdge(&Edge{From: from, To: to, Kind: EdgeCalls}) }

// Two objects share a name and so do their targets. Pairs were deduplicated
// by name, so these two different crossings were one pair, and whichever was
// walked first hid the other — here, a downward violation behind an upward
// call that is fine.
func dupNameGraph() *Graph {
	g := New()
	node(g, "CLAS:Z_DUP", "$ZROOT_01")
	node(g, "CLAS:Z_TARGET", "$ZROOT")
	node(g, "PROG:Z_DUP", "$ZROOT")
	node(g, "PROG:Z_TARGET", "$ZROOT_02")
	call(g, "CLAS:Z_DUP", "CLAS:Z_TARGET")
	call(g, "PROG:Z_DUP", "PROG:Z_TARGET")
	return g
}

func TestCrossingsWithTheSameNamesAreDifferentPairs(t *testing.T) {
	for i := 0; i < orderRuns; i++ {
		r := AnalyzeCrossings(dupNameGraph(), walkScope(), nil)
		if r.Upward != 1 || r.Downward != 1 || len(r.Entries) != 2 {
			t.Fatalf("run %d: upward=%d downward=%d entries=%d, want 1, 1, 2: %+v",
				i, r.Upward, r.Downward, len(r.Entries), r.Entries)
		}
		if r.Entries[0].Direction != CrossDownward || r.Entries[0].SourceType != "PROG" {
			t.Fatalf("run %d: the downward violation should come first: %+v", i, r.Entries)
		}
	}
}

// One pair joined by several edges is one crossing. (The walk keeps the worst
// direction among them, but today a pair's direction is a function of its two
// packages, so its edges cannot disagree; what can be pinned is the count.)
func TestAPairJoinedTwiceIsOneCrossing(t *testing.T) {
	scope := walkScope()
	r := &CrossingReport{}
	g := New()
	node(g, "CLAS:ZCL_A", "$ZROOT_01")
	node(g, "CLAS:ZCL_B", "$ZROOT_02")
	call(g, "CLAS:ZCL_A", "CLAS:ZCL_B")
	g.AddEdge(&Edge{From: "CLAS:ZCL_A", To: "CLAS:ZCL_B", Kind: EdgeReferences})
	g.analyzeCrossingsFrom([]string{"CLAS:ZCL_A"}, scope, defaultCrossingOptions(), r)
	if len(r.Entries) != 1 || r.Sibling != 1 {
		t.Fatalf("want one SIBLING entry for the pair, got %+v (sibling=%d)", r.Entries, r.Sibling)
	}
}

// A test package's call into a sibling is exempt whether the target's package
// was looked up or guessed. The guessed path used to skip the exemption, so the
// verdict depended on whether an earlier edge had already written the guess
// into the graph — that is, on the walk order.
func guessGraph() *Graph {
	g := New()
	node(g, "CLAS:ZCL_ROOT_01_USER", "$ZROOT_01")
	node(g, "CLAS:ZCL_ROOT_TEST_CASE", "$ZROOT_TEST")
	g.AddNode(&Node{ID: "CLAS:ZCL_ROOT_02_THING", Name: "ZCL_ROOT_02_THING", Type: "CLAS"}) // no package: guessed $ZROOT_02
	call(g, "CLAS:ZCL_ROOT_01_USER", "CLAS:ZCL_ROOT_02_THING")
	call(g, "CLAS:ZCL_ROOT_TEST_CASE", "CLAS:ZCL_ROOT_02_THING")
	node(g, "CLAS:Z_DUP", "$ZROOT_01")
	node(g, "CLAS:Z_TARGET", "$ZROOT")
	node(g, "PROG:Z_DUP", "$ZROOT")
	node(g, "PROG:Z_TARGET", "$ZROOT_02")
	call(g, "CLAS:Z_DUP", "CLAS:Z_TARGET")
	call(g, "PROG:Z_DUP", "PROG:Z_TARGET")
	return g
}

// The report must not depend on the order the nodes are walked in. Each graph
// is walked forwards and backwards, fresh each time because the guess is
// written into it, and the two reports must be identical.
func TestCrossingsDoNotDependOnWalkOrder(t *testing.T) {
	ids := []string{"CLAS:ZCL_ROOT_01_USER", "CLAS:ZCL_ROOT_TEST_CASE", "CLAS:Z_DUP", "PROG:Z_DUP"}
	reversed := []string{"PROG:Z_DUP", "CLAS:Z_DUP", "CLAS:ZCL_ROOT_TEST_CASE", "CLAS:ZCL_ROOT_01_USER"}
	walk := func(order []string) *CrossingReport {
		r := &CrossingReport{}
		guessGraph().analyzeCrossingsFrom(order, walkScope(), defaultCrossingOptions(), r)
		return r
	}
	fwd, back := walk(ids), walk(reversed)
	if !reflect.DeepEqual(fwd, back) {
		t.Fatalf("walk order changed the report:\nforwards  %+v\nbackwards %+v", fwd, back)
	}
	for _, e := range fwd.Entries {
		if e.SourceObject == "ZCL_ROOT_TEST_CASE" {
			t.Fatalf("a test package's sibling call was reported: %+v", e)
		}
	}
	if fwd.Sibling != 1 || fwd.Downward != 1 || fwd.Upward != 1 {
		t.Fatalf("want sibling=1 downward=1 upward=1, got %+v", fwd)
	}
}

// The exemption is for test packages, named so at the end. It matched "_TEST"
// anywhere, so a production package such as $ZROOT_TESTING calling into a
// sibling was waved through — on the guessed path too, once that path applied
// the exemption.
func TestOnlyATestPackageIsExemptFromSiblingViolations(t *testing.T) {
	for src, exempt := range map[string]bool{
		"$ZROOT_TEST":    true,
		"$ZROOT_TESTS":   true,
		"$ZROOT_TESTING": false,
		"$ZROOT_TEST_01": false,
	} {
		scope := walkScope()
		scope.Packages = append(scope.Packages, src)
		scope.PackageSet[src] = true
		scope.Hierarchy[src] = "$ZROOT"
		for _, guessed := range []bool{false, true} {
			g := New()
			node(g, "CLAS:ZCL_CALLER", src)
			if guessed {
				g.AddNode(&Node{ID: "CLAS:ZCL_ROOT_02_THING", Name: "ZCL_ROOT_02_THING", Type: "CLAS"})
			} else {
				node(g, "CLAS:ZCL_ROOT_02_THING", "$ZROOT_02")
			}
			call(g, "CLAS:ZCL_CALLER", "CLAS:ZCL_ROOT_02_THING")
			r := AnalyzeCrossings(g, scope, nil)
			if got := r.Sibling == 0; got != exempt {
				t.Errorf("%s (guessed=%v): sibling=%d, exempt want %v", src, guessed, r.Sibling, exempt)
			}
		}
	}
}
