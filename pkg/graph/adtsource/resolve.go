package adtsource

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/graph"
)

// Stage names one of the three lookups ResolvePackages makes.
type Stage int

const (
	// StageTADIR is pass one: R3TR objects by name.
	StageTADIR Stage = iota
	// StageTFDIR is pass two, first half: function module to main program.
	StageTFDIR
	// StageFUGR is pass two, second half: function group to package.
	StageFUGR
)

func (s Stage) String() string {
	switch s {
	case StageTADIR:
		return "TADIR"
	case StageTFDIR:
		return "TFDIR"
	case StageFUGR:
		return "FUGR TADIR"
	}
	return fmt.Sprintf("Stage(%d)", int(s))
}

// Failure is one lookup that did not answer.
type Failure struct {
	Stage Stage
	// Names are the objects the lookup was for. For StageFUGR they are the
	// function modules behind the groups, sorted.
	Names []string
	// Groups are the function groups a StageFUGR lookup asked for, sorted.
	Groups []string
	// Err is what the query returned. Nil means it returned no result at all.
	Err error
}

// Resolution is what ResolvePackages could not do. The packages it could find
// are already written into the graph.
type Resolution struct {
	// Failures are the lookups that did not answer, in the order they were made.
	Failures []Failure

	names       []string
	nodesByName map[string][]*graph.Node
}

// Batch sizes and row limits.
//
// Five names per IN-list: the freestyle data preview has a limit of about 255
// characters for an IN clause's literals.
//
// TADIR asks for 100 rows a batch. A name can be filed under several R3TR
// types (a program and a transaction, a domain and a data element), so the
// rows are not one per name, and a batch of five that came back cut short
// would drop placements without saying so. The CLI asked for three per name
// (15) since the batch was 50 names wide, and MCP for the client default
// (100); the narrower limit was the one that could truncate.
const (
	batchSize         = 5
	tadirRowsPerBatch = 100
)

// ResolvePackages fills in the package — and, where TADIR knows better than
// the parser, the type — of every node that has none, and reports the lookups
// that failed on the way.
//
// Two passes. TADIR places every R3TR object by name. What is still unplaced
// may be a function module, which is not an R3TR object: TFDIR names its main
// program (SAPL<group>), and TADIR then places the group. A module placed this
// way becomes type FUNC.
//
// A failure is not an empty answer, and the difference is the reason this
// reports them. graph.classify sends a node with no package to Unknown, never
// to Violation, and AnalyzeCrossings drops an edge whose source has no package
// — so a boundary report built on lookups that failed comes back with fewer
// findings, and reads as cleaner, than the truth. A name the lookups reached
// and did not find (a local class, a standard name) is an answer, not a gap.
//
// onFailure, when not nil, is called as each failure happens.
func ResolvePackages(ctx context.Context, q Querier, g *graph.Graph, onFailure func(Failure)) *Resolution {
	r := &Resolution{nodesByName: make(map[string][]*graph.Node)}
	for _, n := range g.Nodes() {
		if n.Package == "" && !graph.IsStandardObject(n.Name) && !strings.HasPrefix(n.ID, "DYNAMIC:") {
			r.names = append(r.names, n.Name)
			key := strings.ToUpper(n.Name)
			r.nodesByName[key] = append(r.nodesByName[key], n)
		}
	}
	if len(r.names) == 0 {
		return r
	}
	// g.Nodes() is in map order. Sorted, the batches, the statements sent and
	// the order failures are recorded in are the same on every run.
	sort.Strings(r.names)
	fail := func(f Failure) {
		r.Failures = append(r.Failures, f)
		if onFailure != nil {
			onFailure(f)
		}
	}

	r.resolveTADIR(ctx, q, fail)

	var unresolved []string
	for _, n := range r.names {
		for _, node := range r.nodesByName[strings.ToUpper(n)] {
			if node.Package == "" {
				unresolved = append(unresolved, strings.ToUpper(n))
				break
			}
		}
	}
	if len(unresolved) > 0 {
		r.resolveFMviaTFDIR(ctx, q, unresolved, fail)
	}
	return r
}

func (r *Resolution) resolveTADIR(ctx context.Context, q Querier, fail func(Failure)) {
	for start := 0; start < len(r.names); start += batchSize {
		batch := r.names[start:min(start+batchSize, len(r.names))]
		upper := make([]string, len(batch))
		for i, n := range batch {
			upper[i] = strings.ToUpper(n)
		}
		query := fmt.Sprintf("SELECT object, obj_name, devclass FROM tadir WHERE pgmid = 'R3TR' AND obj_name IN (%s)", quoteList(upper))
		result, err := q.RunQuery(ctx, query, tadirRowsPerBatch)
		if err != nil || result == nil {
			fail(Failure{Stage: StageTADIR, Names: upper, Err: err})
			continue
		}
		for _, row := range result.Rows {
			objType := cell(row, "OBJECT")
			objName := cell(row, "OBJ_NAME")
			devclass := cell(row, "DEVCLASS")
			for _, n := range r.nodesByName[objName] {
				n.Package = devclass
				if objType != "" && n.Type != objType {
					n.Type = objType
				}
			}
		}
	}
}

func (r *Resolution) resolveFMviaTFDIR(ctx context.Context, q Querier, fmNames []string, fail func(Failure)) {
	fmToFugr := make(map[string]string)
	fugrSet := make(map[string]bool)
	for start := 0; start < len(fmNames); start += batchSize {
		batch := fmNames[start:min(start+batchSize, len(fmNames))]
		query := fmt.Sprintf("SELECT FUNCNAME, PNAME FROM TFDIR WHERE FUNCNAME IN (%s)", quoteList(batch))
		result, err := q.RunQuery(ctx, query, len(batch)*2)
		if err != nil || result == nil {
			// A module whose group cannot be looked up keeps its node and loses
			// its containment, and a node belonging to nothing is one a
			// boundary check cannot judge.
			fail(Failure{Stage: StageTFDIR, Names: append([]string(nil), batch...), Err: err})
			continue
		}
		for _, row := range result.Rows {
			funcName := cell(row, "FUNCNAME")
			pname := cell(row, "PNAME")
			fugrName := strings.TrimPrefix(pname, "SAPL")
			if fugrName != "" {
				fmToFugr[funcName] = fugrName
				fugrSet[fugrName] = true
			}
		}
	}
	if len(fugrSet) == 0 {
		return
	}

	// Sorted, so the statement sent is the same on every run.
	groups := make([]string, 0, len(fugrSet))
	for fg := range fugrSet {
		groups = append(groups, fg)
	}
	sort.Strings(groups)
	query := fmt.Sprintf("SELECT obj_name, devclass FROM tadir WHERE pgmid = 'R3TR' AND object = 'FUGR' AND obj_name IN (%s)", quoteList(groups))
	result, err := q.RunQuery(ctx, query, len(groups)*2)
	if err != nil || result == nil {
		// The groups were found and their packages were not: every module
		// behind them keeps a group and loses a package.
		modules := make([]string, 0, len(fmToFugr))
		for fm := range fmToFugr {
			modules = append(modules, fm)
		}
		sort.Strings(modules)
		fail(Failure{Stage: StageFUGR, Names: modules, Groups: groups, Err: err})
		return
	}

	fugrPkg := make(map[string]string)
	for _, row := range result.Rows {
		fugrPkg[cell(row, "OBJ_NAME")] = cell(row, "DEVCLASS")
	}
	for fmName, fugrName := range fmToFugr {
		devclass, ok := fugrPkg[fugrName]
		if !ok {
			continue
		}
		for _, n := range r.nodesByName[fmName] {
			n.Package = devclass
			n.Type = "FUNC"
		}
	}
}

// Unplaced lists the objects that are still without a package because a
// lookup failed, one entry per object, sorted. An object a later lookup placed
// after all, and one the lookups reached and did not find, are not in it.
//
// This is the view vsp's CLI reports. Where a query returned no result at all
// it says so in those words.
func (r *Resolution) Unplaced() []adt.Unsearched {
	if r == nil || len(r.Failures) == 0 {
		return nil
	}
	// Pass two can rescue a name pass one could not reach, so its verdict
	// wins; and a module its second half placed is no longer a gap.
	failed := map[string]string{}
	pass2 := map[string]string{}
	for _, f := range r.Failures {
		switch f.Stage {
		case StageTADIR:
			for _, n := range f.Names {
				failed[n] = reasonOr(f.Err, "TADIR query returned nothing at all")
			}
		case StageTFDIR:
			for _, n := range f.Names {
				pass2[n] = reasonOr(f.Err, "TFDIR query returned nothing at all")
			}
		case StageFUGR:
			for _, n := range f.Names {
				pass2[n] = reasonOr(f.Err, "FUGR TADIR query returned nothing at all")
			}
		}
	}
	for n, reason := range pass2 {
		failed[n] = reason
	}

	var missed []adt.Unsearched
	for _, n := range r.names {
		key := strings.ToUpper(n)
		reason, everFailed := failed[key]
		if !everFailed {
			continue
		}
		for _, node := range r.nodesByName[key] {
			if node.Package == "" {
				missed = append(missed, adt.Unsearched{Object: key, Reason: reason})
				break
			}
		}
	}
	sort.Slice(missed, func(i, j int) bool { return missed[i].Object < missed[j].Object })
	return dedupe(missed)
}

// FailedLookups lists every lookup that failed, named by what it asked for: a
// TADIR failure by object name, a TFDIR failure as "FUNC <module>", a function
// group lookup as "FUGR <group>". An object a later lookup placed after all is
// still listed, because its first lookup did fail.
//
// This is the view the MCP server reports. Where a query returned no result at
// all, a TFDIR or group lookup says "the source came back empty", as it always
// has there.
//
// The list is in the order the lookups are made — TADIR, then TFDIR, then the
// function groups — and by name within each. It used to follow the graph's map
// order, so the five a note names out of a longer list changed between runs.
func (r *Resolution) FailedLookups() []adt.Unsearched {
	if r == nil {
		return nil
	}
	type lookup struct {
		stage Stage
		adt.Unsearched
	}
	var all []lookup
	add := func(st Stage, object, reason string) {
		all = append(all, lookup{st, adt.Unsearched{Object: object, Reason: reason}})
	}
	for _, f := range r.Failures {
		switch f.Stage {
		case StageTADIR:
			for _, n := range f.Names {
				add(f.Stage, n, reasonOr(f.Err, "TADIR query returned nothing at all"))
			}
		case StageTFDIR:
			for _, n := range f.Names {
				add(f.Stage, "FUNC "+n, reasonOr(f.Err, "the source came back empty"))
			}
		case StageFUGR:
			for _, fg := range f.Groups {
				add(f.Stage, "FUGR "+fg, reasonOr(f.Err, "the source came back empty"))
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].stage != all[j].stage {
			return all[i].stage < all[j].stage
		}
		return all[i].Object < all[j].Object
	})
	var out []adt.Unsearched
	for _, l := range all {
		out = append(out, l.Unsearched)
	}
	return out
}

func reasonOr(err error, none string) string {
	if err == nil {
		return none
	}
	return err.Error()
}

// dedupe keeps one entry per object. The same class can be reached through
// several edges, and a caveat that names it four times reads as four holes.
func dedupe(in []adt.Unsearched) []adt.Unsearched {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, u := range in {
		if seen[u.Object] {
			continue
		}
		seen[u.Object] = true
		out = append(out, u)
	}
	return out
}

func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "'" + n + "'"
	}
	return strings.Join(quoted, ",")
}

// cell reads one column of a data-preview row as a trimmed, upper-case string.
func cell(row map[string]interface{}, col string) string {
	return strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", row[col])))
}
