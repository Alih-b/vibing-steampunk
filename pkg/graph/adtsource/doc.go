// Package adtsource reads what the dependency graph is built from out of a SAP
// system: package assignments from TADIR and TFDIR, the TVARVC readers in the
// cross-reference tables, the load rows of D010INC, and the signals a health
// snapshot is made of.
//
// It sits between pkg/adt, which speaks in tables and ADT resources, and
// pkg/graph, which speaks in nodes and edges and stays free of any SAP client
// so its tests run offline. cmd/vsp and internal/mcp both call it, which is the
// point: each of these readers used to exist once in each front end, and a fix
// made in one of them was a fix the other did not get.
//
// It answers in plain data. How a gap is worded, whether a warning goes to
// stderr, and what shape a result is printed in all stay with the callers —
// the CLI and the MCP server word the same failures differently, and this
// package gives each of them what it needs to keep doing so.
package adtsource

import (
	"context"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// Querier runs a free-SQL statement. *adt.Client is one.
type Querier interface {
	RunQuery(ctx context.Context, sql string, maxRows int) (*adt.TableContentsResult, error)
}
