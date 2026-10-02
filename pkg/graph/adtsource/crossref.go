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
