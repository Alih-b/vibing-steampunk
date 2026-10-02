package graph

import (
	"reflect"
	"strings"
	"testing"
)

// These pin the order reports come out in. The graph keeps its nodes in maps,
// so before the order was chosen it changed from run to run; each test builds
// its graph afresh many times so that a map-ordered result would not survive.

const orderRuns = 30

func crossingGraph() *Graph {
	g := New()
	add := func(id, pkg string) {
		parts := strings.SplitN(id, ":", 2)
		g.AddNode(&Node{ID: id, Name: parts[1], Type: parts[0], Package: pkg})
	}
	add("CLAS:ZCL_A", "$ZLLM_01")
	add("CLAS:ZCL_B", "$ZLLM_02")
	add("CLAS:ZCL_C", "$ZLLM")
	add("CLAS:ZCL_D", "$ZLLM_00")
	add("CLAS:ZCL_E", "$ZLLM_04")
	add("CLAS:ZCL_X", "$ZOTHER")
	edge := func(from, to string) { g.AddEdge(&Edge{From: from, To: to, Kind: EdgeCalls}) }
	edge("CLAS:ZCL_A", "CLAS:ZCL_C") // upward
	edge("CLAS:ZCL_A", "CLAS:ZCL_D") // common
	edge("CLAS:ZCL_A", "CLAS:ZCL_X") // external
	edge("CLAS:ZCL_A", "CLAS:ZCL_B") // sibling
	edge("CLAS:ZCL_B", "CLAS:ZCL_A") // sibling, the other way: circular
	edge("CLAS:ZCL_E", "CLAS:ZCL_B") // sibling
	edge("CLAS:ZCL_E", "CLAS:ZCL_A") // sibling
	edge("CLAS:ZCL_C", "CLAS:ZCL_A") // downward
	return g
}

func TestCrossingsAreOrderedBySeverityThenName(t *testing.T) {
	want := []string{
		"SIBLING $ZLLM_01 ZCL_A → $ZLLM_02 ZCL_B",
		"SIBLING $ZLLM_02 ZCL_B → $ZLLM_01 ZCL_A",
		"SIBLING $ZLLM_04 ZCL_E → $ZLLM_01 ZCL_A",
		"SIBLING $ZLLM_04 ZCL_E → $ZLLM_02 ZCL_B",
		"DOWNWARD $ZLLM ZCL_C → $ZLLM_01 ZCL_A",
		"EXTERNAL $ZLLM_01 ZCL_A → $ZOTHER ZCL_X",
		"UPWARD $ZLLM_01 ZCL_A → $ZLLM ZCL_C",
		"COMMON $ZLLM_01 ZCL_A → $ZLLM_00 ZCL_D",
	}
	for i := 0; i < orderRuns; i++ {
		r := AnalyzeCrossings(crossingGraph(), testScope(), nil)
		var got []string
		for _, e := range r.Entries {
			got = append(got, string(e.Direction)+" "+e.SourcePackage+" "+e.SourceObject+" → "+e.TargetPackage+" "+e.TargetObject)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d:\n got %q\nwant %q", i, got, want)
		}
		if !reflect.DeepEqual(r.Circular, []string{"$ZLLM_01 <-> $ZLLM_02"}) {
			t.Fatalf("run %d: circular %q, want the pair once, smaller package first", i, r.Circular)
		}
	}
}

func boundaryGraph() *Graph {
	g := New()
	g.AddNode(&Node{ID: "CLAS:ZCL_ROOT", Name: "ZCL_ROOT", Type: "CLAS", Package: "$ZROOT"})
	g.AddNode(&Node{ID: "CLAS:ZCL_ROOT2", Name: "ZCL_ROOT2", Type: "CLAS", Package: "$ZROOT"})
	for _, p := range []string{"$ZB", "$ZA", "$ZC", "$ZD"} {
		id := "CLAS:ZCL_" + strings.TrimPrefix(p, "$Z")
		g.AddNode(&Node{ID: id, Name: strings.TrimPrefix(id, "CLAS:"), Type: "CLAS", Package: p})
	}
	g.AddNode(&Node{ID: "CLAS:ZCL_NOWHERE", Name: "ZCL_NOWHERE", Type: "CLAS"})
	edge := func(from, to string) { g.AddEdge(&Edge{From: from, To: to, Kind: EdgeCalls}) }
	edge("CLAS:ZCL_ROOT2", "CLAS:ZCL_B")
	edge("CLAS:ZCL_ROOT", "CLAS:ZCL_C")
	edge("CLAS:ZCL_ROOT", "CLAS:ZCL_A")
	edge("CLAS:ZCL_ROOT2", "CLAS:ZCL_C")
	edge("CLAS:ZCL_ROOT", "CLAS:ZCL_D")
	edge("CLAS:ZCL_ROOT", "CLAS:ZCL_NOWHERE")
	edge("CLAS:ZCL_ROOT", "CLAS:ZCL_ROOT2")
	return g
}

func TestPackagesCrossedAreOrderedByRefsThenName(t *testing.T) {
	wantBlock := "  Packages crossed:\n" +
		"    $ZC — 2 refs\n" +
		"    $ZA — 1 refs\n" +
		"    $ZB — 1 refs\n" +
		"    $ZD — 1 refs\n"
	for i := 0; i < orderRuns; i++ {
		text := boundaryGraph().CheckBoundaries("$ZROOT", nil).FormatText()
		if !strings.Contains(text, wantBlock) {
			t.Fatalf("run %d: want\n%s\nin\n%s", i, wantBlock, text)
		}
	}
}

func TestBoundaryEntriesAreOrderedByVerdictThenObject(t *testing.T) {
	want := []string{
		"VIOLATION CLAS:ZCL_ROOT → CLAS:ZCL_C",
		"VIOLATION CLAS:ZCL_ROOT → CLAS:ZCL_A",
		"VIOLATION CLAS:ZCL_ROOT → CLAS:ZCL_D",
		"VIOLATION CLAS:ZCL_ROOT2 → CLAS:ZCL_B",
		"VIOLATION CLAS:ZCL_ROOT2 → CLAS:ZCL_C",
		"UNKNOWN CLAS:ZCL_ROOT → CLAS:ZCL_NOWHERE",
		"SAME_PACKAGE CLAS:ZCL_ROOT → CLAS:ZCL_ROOT2",
	}
	for i := 0; i < orderRuns; i++ {
		var got []string
		for _, e := range boundaryGraph().CheckBoundaries("$ZROOT", nil).Entries {
			got = append(got, string(e.Verdict)+" "+e.From.ID+" → "+e.To.ID)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d:\n got %q\nwant %q", i, got, want)
		}
	}
}

// The unattributed line used to format the *Node itself, which prints as a Go
// struct literal: "&{CLAS:ZCL_ROOT ZCL_ROOT CLAS $ZROOT [] map[]}".
func TestUnattributedReferencesAreNamed(t *testing.T) {
	text := boundaryGraph().CheckBoundaries("$ZROOT", nil).FormatText()
	if strings.Contains(text, "&{") {
		t.Fatalf("a node printed as a Go struct:\n%s", text)
	}
	if !strings.Contains(text, "    unattributed: CLAS:ZCL_ROOT → CLAS:ZCL_NOWHERE (CALLS)\n") {
		t.Fatalf("unattributed reference not named by ID:\n%s", text)
	}
}
