package adtsource

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/graph"
)

// fakeSQL answers the three lookups from maps, records what it was asked, and
// refuses any statement containing one of fail.
type fakeSQL struct {
	tadir   map[string][2]string // name → type, package
	tfdir   map[string]string    // module → main program
	groups  map[string]string    // group → package
	fail    []string
	nilFor  []string
	asked   []string
	maxRows []int
}

var inList = regexp.MustCompile(`IN \(([^)]*)\)`)

func (f *fakeSQL) RunQuery(_ context.Context, sql string, maxRows int) (*adt.TableContentsResult, error) {
	f.asked = append(f.asked, sql)
	f.maxRows = append(f.maxRows, maxRows)
	for _, s := range f.fail {
		if strings.Contains(sql, s) {
			return nil, errors.New("refused: " + s)
		}
	}
	for _, s := range f.nilFor {
		if strings.Contains(sql, s) {
			return nil, nil
		}
	}
	var lits []string
	if m := inList.FindStringSubmatch(sql); m != nil {
		for _, l := range strings.Split(m[1], ",") {
			lits = append(lits, strings.Trim(l, "'"))
		}
	}
	res := &adt.TableContentsResult{}
	for _, l := range lits {
		switch {
		case strings.Contains(sql, "object = 'FUGR'"):
			if pkg, ok := f.groups[l]; ok {
				res.Rows = append(res.Rows, map[string]interface{}{"OBJ_NAME": l, "DEVCLASS": pkg})
			}
		case strings.Contains(sql, "FROM TFDIR"):
			if p, ok := f.tfdir[l]; ok {
				res.Rows = append(res.Rows, map[string]interface{}{"FUNCNAME": l, "PNAME": p})
			}
		default:
			if t, ok := f.tadir[l]; ok {
				res.Rows = append(res.Rows, map[string]interface{}{"OBJECT": t[0], "OBJ_NAME": l, "DEVCLASS": t[1]})
			}
		}
	}
	return res, nil
}

func newFake() *fakeSQL {
	return &fakeSQL{
		tadir:  map[string][2]string{"ZCL_FOREIGN": {"CLAS", "$ZOTHER"}, "ZIF_GUESSED": {"INTF", "$ZOTHER"}},
		tfdir:  map[string]string{"Z_FM_A": "SAPLZGROUP_A", "Z_FM_B": "SAPLZGROUP_B"},
		groups: map[string]string{"ZGROUP_A": "$ZFUNCS"},
	}
}

// graphOf builds a graph with one placed node and the given unplaced targets.
func graphOf(ids ...string) *graph.Graph {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "CLAS:ZCL_HOME", Name: "ZCL_HOME", Type: "CLAS", Package: "$ZHOME"})
	for _, id := range ids {
		typ, name, _ := strings.Cut(id, ":")
		g.AddNode(&graph.Node{ID: id, Name: name, Type: typ})
	}
	return g
}

func objects(us []adt.Unsearched) string {
	var out []string
	for _, u := range us {
		out = append(out, u.Object)
	}
	return strings.Join(out, ",")
}

func TestResolvePackagesPlacesThroughBothPasses(t *testing.T) {
	q := newFake()
	g := graphOf("CLAS:ZCL_FOREIGN", "CLAS:ZIF_GUESSED", "FUGR:Z_FM_A", "FUGR:Z_FM_B", "CLAS:CL_STANDARD", "DYNAMIC:LV_X")
	r := ResolvePackages(context.Background(), q, g, nil)

	for id, want := range map[string]string{
		"CLAS:ZCL_FOREIGN": "CLAS $ZOTHER",
		"CLAS:ZIF_GUESSED": "INTF $ZOTHER", // TADIR corrects the parser's guess
		"FUGR:Z_FM_A":      "FUNC $ZFUNCS", // placed through its group
		"FUGR:Z_FM_B":      "FUGR ",        // its group is in no package TADIR knows: an answer, not a gap
		"CLAS:CL_STANDARD": "CLAS ",        // standard objects are not asked about
	} {
		n := g.GetNode(id)
		if got := n.Type + " " + n.Package; got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
	if len(r.Failures) != 0 || r.Unplaced() != nil || r.FailedLookups() != nil {
		t.Fatalf("nothing failed, yet: %+v", r.Failures)
	}
	for _, sql := range q.asked {
		if strings.Contains(sql, "CL_STANDARD") || strings.Contains(sql, "LV_X") {
			t.Errorf("asked about a standard or dynamic name: %s", sql)
		}
	}
}

func TestNothingToResolveAsksNothing(t *testing.T) {
	q := newFake()
	r := ResolvePackages(context.Background(), q, graphOf("CLAS:CL_STANDARD"), nil)
	if len(q.asked) != 0 || r.Unplaced() != nil || r.FailedLookups() != nil {
		t.Fatalf("asked %v for a graph with nothing to place", q.asked)
	}
}

// The two views differ on purpose, and the difference is pinned here: the CLI
// reports what is still unplaced, MCP every lookup that failed.
func TestTheTwoViewsOfAFailedTADIRBatch(t *testing.T) {
	q := newFake()
	q.fail = []string{"'R3TR' AND obj_name IN"}
	g := graphOf("CLAS:ZCL_FOREIGN", "FUGR:Z_FM_A")
	r := ResolvePackages(context.Background(), q, g, nil)

	// Z_FM_A was rescued by pass two; ZCL_FOREIGN was not.
	if got := objects(r.Unplaced()); got != "ZCL_FOREIGN" {
		t.Errorf("Unplaced = %s, want only what is still without a package", got)
	}
	if got := objects(r.FailedLookups()); got != "ZCL_FOREIGN,Z_FM_A" && got != "Z_FM_A,ZCL_FOREIGN" {
		t.Errorf("FailedLookups = %s, want every name of the failed batch", got)
	}
	if g.GetNode("FUGR:Z_FM_A").Package != "$ZFUNCS" {
		t.Error("pass two did not run after pass one failed")
	}
}

func TestAFailedGroupLookupNamesModulesForOneViewAndGroupsForTheOther(t *testing.T) {
	q := newFake()
	q.fail = []string{"object = 'FUGR'"}
	r := ResolvePackages(context.Background(), q, graphOf("FUGR:Z_FM_B", "FUGR:Z_FM_A"), nil)

	if got := objects(r.Unplaced()); got != "Z_FM_A,Z_FM_B" {
		t.Errorf("Unplaced = %s", got)
	}
	if got := objects(r.FailedLookups()); got != "FUGR ZGROUP_A,FUGR ZGROUP_B" {
		t.Errorf("FailedLookups = %s, want the groups, sorted", got)
	}
	// The statement is the same whatever order the groups came back in.
	last := q.asked[len(q.asked)-1]
	if !strings.Contains(last, "IN ('ZGROUP_A','ZGROUP_B')") {
		t.Errorf("group lookup not sorted: %s", last)
	}
}

func TestAFailedTFDIRBatchIsNamedAsAModule(t *testing.T) {
	q := newFake()
	q.fail = []string{"FROM TFDIR"}
	r := ResolvePackages(context.Background(), q, graphOf("FUGR:Z_FM_A", "CLAS:ZCL_FOREIGN"), nil)
	if got := objects(r.Unplaced()); got != "Z_FM_A" {
		t.Errorf("Unplaced = %s", got)
	}
	if got := objects(r.FailedLookups()); got != "FUNC Z_FM_A" {
		t.Errorf("FailedLookups = %s", got)
	}
}

// A query that answers nothing at all is a failure, not an empty answer. The
// MCP copy of the TADIR pass dereferenced the nil result instead.
func TestANilResultIsAFailureInEveryPass(t *testing.T) {
	for _, stage := range []struct{ marker, node, unplaced, failed string }{
		{"'R3TR' AND obj_name IN", "CLAS:ZCL_FOREIGN", "TADIR query returned nothing at all", "TADIR query returned nothing at all"},
		{"FROM TFDIR", "FUGR:Z_FM_A", "TFDIR query returned nothing at all", "the source came back empty"},
		{"object = 'FUGR'", "FUGR:Z_FM_A", "FUGR TADIR query returned nothing at all", "the source came back empty"},
	} {
		q := newFake()
		q.nilFor = []string{stage.marker}
		r := ResolvePackages(context.Background(), q, graphOf(stage.node), nil)
		u, f := r.Unplaced(), r.FailedLookups()
		if len(u) != 1 || u[0].Reason != stage.unplaced {
			t.Errorf("%s: Unplaced = %+v", stage.marker, u)
		}
		if len(f) == 0 || f[len(f)-1].Reason != stage.failed {
			t.Errorf("%s: FailedLookups = %+v", stage.marker, f)
		}
	}
}

func TestBatchesAreFiveNamesAndTADIRAsksForAHundredRows(t *testing.T) {
	q := newFake()
	var ids []string
	for i := 0; i < 7; i++ {
		ids = append(ids, fmt.Sprintf("CLAS:ZCL_N%d", i))
	}
	ResolvePackages(context.Background(), q, graphOf(ids...), nil)
	var tadir []int
	for i, sql := range q.asked {
		if strings.Contains(sql, "'R3TR' AND obj_name IN") {
			if n := strings.Count(sql, "'ZCL_N"); n > 5 {
				t.Errorf("a batch of %d names: %s", n, sql)
			}
			tadir = append(tadir, q.maxRows[i])
		}
	}
	if len(tadir) != 2 || tadir[0] != 100 || tadir[1] != 100 {
		t.Errorf("TADIR row limits = %v, want two batches of 100", tadir)
	}
}

// The same name reached through two nodes is one gap, not two.
func TestUnplacedNamesEachObjectOnce(t *testing.T) {
	q := newFake()
	q.fail = []string{"'R3TR' AND obj_name IN", "FROM TFDIR"}
	r := ResolvePackages(context.Background(), q, graphOf("CLAS:ZCL_DUP", "INTF:ZCL_DUP", "CLAS:ZCL_OTHER"), nil)
	if got := objects(r.Unplaced()); got != "ZCL_DUP,ZCL_OTHER" {
		t.Errorf("Unplaced = %s", got)
	}
}

func TestOnFailureHearsEachFailureAsItHappens(t *testing.T) {
	q := newFake()
	q.fail = []string{"'R3TR' AND obj_name IN", "object = 'FUGR'"}
	var heard []string
	r := ResolvePackages(context.Background(), q, graphOf("FUGR:Z_FM_A"), func(f Failure) {
		heard = append(heard, f.Stage.String())
	})
	if strings.Join(heard, ",") != "TADIR,FUGR TADIR" || len(r.Failures) != 2 {
		t.Errorf("heard %v, recorded %+v", heard, r.Failures)
	}
}
