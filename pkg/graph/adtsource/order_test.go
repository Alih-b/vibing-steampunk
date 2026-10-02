package adtsource

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/graph"
)

// The part after the slash says what the object is, so it is mapped, not cut
// off: an include is not a program, a function module is not its group.
func TestSourceKind(t *testing.T) {
	for in, want := range map[string]string{
		"CLAS/OC": "CLAS",
		"clas/oc": "CLAS",
		"CLAS":    "CLAS",
		"INTF/OI": "INTF",
		"PROG/P":  "PROG",
		"PROG/I":  "INCL",
		" FUGR/F": "FUGR",
		"FUGR/FF": "FUNC",
		"FUGR/I":  "",
		"CLAS/OL": "",
		"TABL/DT": "",
		"TABL/DS": "",
		"DEVC/K":  "",
		"":        "",
	} {
		if got := SourceKind(in); got != want {
			t.Errorf("SourceKind(%q) = %q, want %q", in, got, want)
		}
	}
}

// GrepObject reports a source it could not read in its result, with a nil
// error. That has to count as a failure, not as "read, and not there".
func TestGrepFailure(t *testing.T) {
	cases := []struct {
		name   string
		res    *adt.GrepObjectResult
		err    error
		failed bool
		reason string
	}{
		{"error", nil, errors.New("boom"), true, "boom"},
		{"no result", nil, nil, true, "the grep returned no result"},
		{"unreadable source", &adt.GrepObjectResult{Message: "Failed to read source: 404"}, nil, true, "Failed to read source: 404"},
		{"unreadable, no message", &adt.GrepObjectResult{}, nil, true, "the source could not be read"},
		{"read, no match", &adt.GrepObjectResult{Success: true, Message: "No matches found"}, nil, false, ""},
		{"read, match", &adt.GrepObjectResult{Success: true, Matches: []adt.GrepMatch{{LineNumber: 1}}}, nil, false, ""},
	}
	for _, c := range cases {
		reason, failed := GrepFailure(c.res, c.err)
		if failed != c.failed || reason != c.reason {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, reason, failed, c.reason, c.failed)
		}
	}
}

// FailedLookups is in lookup order — TADIR, TFDIR, function groups — and by
// name within each. The names come from the graph's map, so before they were
// sorted the list, and the five a note names out of it, changed between runs.
func TestFailedLookupsAreOrderedByStageThenName(t *testing.T) {
	want := []string{
		"ZCL_A", "ZCL_B", "ZCL_C", "ZCL_D", "ZCL_E", "ZCL_F", "ZCL_G",
		"Z_FM_A", "Z_FM_B",
		"FUGR ZFG_A", "FUGR ZFG_B",
	}
	for i := 0; i < 30; i++ {
		g := graph.New()
		g.AddNode(&graph.Node{ID: "CLAS:ZCL_ROOT", Name: "ZCL_ROOT", Type: "CLAS", Package: "$ZROOT"})
		for _, n := range []string{"ZCL_G", "Z_FM_B", "ZCL_C", "ZCL_A", "ZCL_F", "Z_FM_A", "ZCL_E", "ZCL_B", "ZCL_D"} {
			g.AddNode(&graph.Node{ID: "CLAS:" + n, Name: n, Type: "CLAS"})
		}
		q := &fakeSQL{
			tfdir: map[string]string{"Z_FM_A": "SAPLZFG_A", "Z_FM_B": "SAPLZFG_B"},
			fail:  []string{"'R3TR' AND obj_name IN", "AND object = 'FUGR'"},
		}
		var got []string
		for _, u := range ResolvePackages(context.Background(), q, g, nil).FailedLookups() {
			got = append(got, u.Object)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d:\n got %q\nwant %q", i, got, want)
		}
	}
}

// Only a short list of codes is excused from a scan; the rest are either read
// or reported.
func TestIsNonSourceType(t *testing.T) {
	for in, want := range map[string]bool{
		"TABL/DT": true, "tabl/ds": true, "DTEL/DE": true, "DEVC/K": true, "SAPC": true, "SAMC": true,
		"CLAS/OC": false, "PROG/X": false, "DDLS/DF": false, "FUGR/I": false, "": false,
	} {
		if got := IsNonSourceType(in); got != want {
			t.Errorf("IsNonSourceType(%q) = %v, want %v", in, got, want)
		}
	}
}
