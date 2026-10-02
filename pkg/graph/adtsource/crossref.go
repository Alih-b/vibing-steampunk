package adtsource

import (
	"context"
	"fmt"
	"strings"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/graph"
)

// ObjectRef names a repository object.
type ObjectRef struct {
	Type string
	Name string
}

// TVARVCReaders lists the objects whose code touches the TVARVC table, one
// entry per object in the order the tables returned them.
//
// Two tables are asked because neither covers both kinds of code: WBCROSSGT
// with OTYPE 'TY' records object-oriented code, CROSS with TYPE 'S' classic
// procedural code. CROSS.TYPE is C(1); the 'DA' once asked of it is a
// two-character WBCROSSGT code for a data object, and SAP answers it with 400.
//
// Each table can fail on its own, and its error is returned on its own: one
// failing still finds real readers, both failing is no answer at all, and the
// caller decides how to say which.
func TVARVCReaders(ctx context.Context, q Querier) (readers []ObjectRef, wbErr, crossErr error) {
	seen := make(map[string]bool)
	collect := func(label, query string) error {
		res, err := q.RunQuery(ctx, query, 500)
		if err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		if res == nil {
			return nil
		}
		for _, row := range res.Rows {
			include := strings.TrimSpace(fmt.Sprintf("%v", row["INCLUDE"]))
			if include == "" {
				continue
			}
			_, objType, objName := graph.NormalizeInclude(include)
			key := objType + ":" + objName
			if !seen[key] {
				seen[key] = true
				readers = append(readers, ObjectRef{Type: objType, Name: objName})
			}
		}
		return nil
	}
	wbErr = collect("WBCROSSGT", "SELECT INCLUDE FROM WBCROSSGT WHERE OTYPE = 'TY' AND NAME = 'TVARVC'")
	crossErr = collect("CROSS", "SELECT INCLUDE FROM CROSS WHERE TYPE = 'S' AND NAME = 'TVARVC'")
	return readers, wbErr, crossErr
}

// D010INCRows converts load rows as the client read them into the rows the
// load-graph builder takes. The two types exist because the client speaks in
// table columns and the graph in edges; this is the one place they meet, so
// pkg/adt need not know what a graph is and pkg/graph need not know a client.
func D010INCRows(rows []adt.LoadRow) []graph.D010INCRow {
	out := make([]graph.D010INCRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, graph.D010INCRow{
			Master:            r.Master,
			Include:           r.Include,
			ObsoleteInVersion: r.ObsoleteInVersion,
		})
	}
	return out
}

// GrepFailure says whether a grep of one object's source failed, and why.
//
// adt.Client.GrepObject reports a source it could not read in the result, not
// in the error: Success stays false and Message carries the reason. A caller
// that looks only at the error and at the matches files that object as read
// and not matching, which is the opposite fact, and the one someone deletes a
// variable on.
func GrepFailure(res *adt.GrepObjectResult, err error) (reason string, failed bool) {
	switch {
	case err != nil:
		return err.Error(), true
	case res == nil:
		return "the grep returned no result", true
	case !res.Success:
		if res.Message == "" {
			return "the source could not be read", true
		}
		return res.Message, true
	}
	return "", false
}

// TVARVCTableGaps names the cross-reference tables TVARVCReaders could not
// read, in the words both front ends report them in.
func TVARVCTableGaps(wbErr, crossErr error) []adt.Unsearched {
	var gaps []adt.Unsearched
	if wbErr != nil {
		gaps = append(gaps, adt.Unsearched{Object: "WBCROSSGT (object-oriented code)", Reason: wbErr.Error()})
	}
	if crossErr != nil {
		gaps = append(gaps, adt.Unsearched{Object: "CROSS (classic procedural code)", Reason: crossErr.Error()})
	}
	return gaps
}

// ConfigGapNote renders the gaps of a TVARVC where-used answer as a note, or
// "" when there are none. The total is every candidate plus every table that
// could not be asked: a candidate whose source could not be read is both a
// reader row and a gap, and is counted once.
func ConfigGapNote(refs []graph.TVARVCReference, gaps []adt.Unsearched) string {
	total := len(refs)
	candidate := make(map[string]bool, len(refs))
	for _, r := range refs {
		candidate[r.ObjectType+" "+r.ObjectName] = true
	}
	for _, g := range gaps {
		if !candidate[g.Object] {
			total++
		}
	}
	return adt.UnsearchedNote(gaps, total, "object")
}
