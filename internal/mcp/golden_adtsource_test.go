package mcp

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/internal/fakesap"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/graph"
)

// The golden files under testdata/golden/adtsource pin what the handlers built
// on the shared ADT/SQL source (pkg/graph/adtsource) answer against a scripted
// SAP — the same scripted SAP cmd/vsp's goldens are captured against. They were
// captured before that package existed, so a diff here is a change in what an
// agent is told, not a refactoring detail.
//
//	go test ./internal/mcp -run TestGoldenADTSource -update-adtsource-golden

var updateADTSourceGolden = flag.Bool("update-adtsource-golden", false, "rewrite testdata/golden/adtsource")

func adtsourceGoldenCompare(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", "adtsource", name+".golden")
	if *updateADTSourceGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-adtsource-golden to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s changed.\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func TestGoldenADTSource(t *testing.T) {
	gold, narrow := fakesap.Gold, fakesap.Narrow
	cases := []struct {
		name    string
		world   func() fakesap.World
		handler func(*Server) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		args    map[string]any
	}{
		{"check_boundaries_gold_package", gold, (*Server).checkBoundaries, map[string]any{"package": "$ZGOLD"}},
		{"check_boundaries_gold_package_full", gold, (*Server).checkBoundaries, map[string]any{"package": "$ZGOLD", "format": "full"}},
		{"check_boundaries_gold_object", gold, (*Server).checkBoundaries, map[string]any{"object": "ZGOLD_REPORT", "package": "$ZGOLD_SUB"}},
		{"check_boundaries_gold_source", gold, (*Server).checkBoundaries, map[string]any{"source": "CALL FUNCTION 'Z_GOLD_FM'.\nDATA lo TYPE REF TO zcl_foreign.", "package": "$ZGOLD"}},
		{"check_boundaries_narrow_package", narrow, (*Server).checkBoundaries, map[string]any{"package": "$ZNARROW"}},
		{"check_boundaries_narrow_tadir_down", fakesap.NarrowTADIRDown, (*Server).checkBoundaries, map[string]any{"package": "$ZNARROW"}},
		{"check_boundaries_narrow_tfdir_down", fakesap.NarrowTFDIRDown, (*Server).checkBoundaries, map[string]any{"package": "$ZNARROW"}},
		{"check_boundaries_narrow_fugr_down", fakesap.NarrowFUGRDown, (*Server).checkBoundaries, map[string]any{"package": "$ZNARROW"}},
		{"check_boundaries_narrow_all_down", fakesap.NarrowAllDown, (*Server).checkBoundaries, map[string]any{"package": "$ZNARROW"}},
		{"check_boundaries_narrow_all_down_object", fakesap.NarrowAllDown, (*Server).checkBoundaries, map[string]any{"object": "ZCL_NARROW", "package": "$ZNARROW"}},

		{"graph_stats_gold_package", gold, (*Server).graphStats, map[string]any{"package": "$ZGOLD"}},
		{"graph_stats_gold_object", gold, (*Server).graphStats, map[string]any{"object_type": "CLAS", "object_name": "ZCL_GOLD_A"}},

		{"health_gold_package", gold, (*Server).health, map[string]any{"package": "$ZGOLD"}},
		{"health_gold_object", gold, (*Server).health, map[string]any{"object_type": "CLAS", "object_name": "ZCL_GOLD_A"}},
		{"health_gold_object_prog", gold, (*Server).health, map[string]any{"object_type": "PROG", "object_name": "ZGOLD_REPORT"}},
		{"health_narrow_package_tadir_down", fakesap.NarrowTADIRDown, (*Server).health, map[string]any{"package": "$ZNARROW"}},
		{"health_narrow_object_tadir_down", fakesap.NarrowTADIRDown, (*Server).health, map[string]any{"object_type": "CLAS", "object_name": "ZCL_NARROW"}},
		{"health_narrow_object_tfdir_down", fakesap.NarrowTFDIRDown, (*Server).health, map[string]any{"object_type": "CLAS", "object_name": "ZCL_NARROW"}},
		{"health_narrow_object_fugr_down", fakesap.NarrowFUGRDown, (*Server).health, map[string]any{"object_type": "CLAS", "object_name": "ZCL_NARROW"}},
		{"health_narrow_object_all_down", fakesap.NarrowAllDown, (*Server).health, map[string]any{"object_type": "CLAS", "object_name": "ZCL_NARROW"}},
		{"health_narrow_package_all_down", fakesap.NarrowAllDown, (*Server).health, map[string]any{"package": "$ZNARROW"}},

		{"where_used_config_gold", gold, (*Server).whereUsedConfig, map[string]any{"variable": "ZGOLD_VAR"}},
		{"where_used_config_gold_nogrep", gold, (*Server).whereUsedConfig, map[string]any{"variable": "ZGOLD_VAR", "grep": false}},
		{"where_used_config_wbcrossgt_down", func() fakesap.World { return fakesap.CrossDown(false) }, (*Server).whereUsedConfig, map[string]any{"variable": "ZGOLD_VAR"}},
		{"where_used_config_both_down", func() fakesap.World { return fakesap.CrossDown(true) }, (*Server).whereUsedConfig, map[string]any{"variable": "ZGOLD_VAR"}},

		{"loads_gold_both", gold, (*Server).loads, map[string]any{"object_name": "ZCL_GOLD_A", "direction": "both"}},
		{"loads_report", gold, (*Server).loads, map[string]any{"object_name": "ZGOLD_REPORT"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := fakesap.New(t, c.world())
			s := &Server{adtClient: adt.NewClient(srv.URL, "TESTUSER", "secret")}
			var req mcp.CallToolRequest
			req.Params.Arguments = c.args
			result, err := c.handler(s)(context.Background(), req)
			var b strings.Builder
			if err != nil {
				fmt.Fprintf(&b, "=== error\n%v\n", err)
			} else {
				fmt.Fprintf(&b, "=== isError\n%v\n=== text\n%s\n", result.IsError, toolResultText(t, result))
			}
			fmt.Fprintf(&b, "=== requests\n%s\n", strings.Join(srv.Log(), "\n"))
			adtsourceGoldenCompare(t, "mcp_"+c.name, fakesap.Normalise(b.String()))
		})
	}
}

// The package resolver on its own, against each way the lookups can fail.
func TestGoldenADTSourceResolvePackages(t *testing.T) {
	worlds := map[string]func() fakesap.World{
		"narrow":            fakesap.Narrow,
		"narrow_tadir_down": fakesap.NarrowTADIRDown,
		"narrow_tfdir_down": fakesap.NarrowTFDIRDown,
		"narrow_fugr_down":  fakesap.NarrowFUGRDown,
		"narrow_all_down":   fakesap.NarrowAllDown,
		"gold":              fakesap.Gold,
	}
	for name, world := range worlds {
		t.Run(name, func(t *testing.T) {
			srv := fakesap.New(t, world())
			s := &Server{adtClient: adt.NewClient(srv.URL, "TESTUSER", "secret")}
			g := graph.New()
			g.AddNode(&graph.Node{ID: "CLAS:ZCL_NARROW", Name: "ZCL_NARROW", Type: "CLAS", Package: "$ZNARROW"})
			for _, id := range []string{"CLAS:ZCL_FOREIGN", "FUGR:Z_GOLD_FM", "FUGR:Z_LOST_FM", "CLAS:ZCL_GOLD_A", "CLAS:CL_STANDARD", "DYNAMIC:LV_FM"} {
				parts := strings.SplitN(id, ":", 2)
				g.AddNode(&graph.Node{ID: id, Name: parts[1], Type: parts[0]})
			}
			missed := s.resolvePackages(context.Background(), g)
			// Not sorted here: FailedLookups promises an order (stage, then
			// name), and this pins it.
			var missedLines []string
			for _, m := range missed {
				missedLines = append(missedLines, fmt.Sprintf("%s: %s\n", m.Object, m.Reason))
			}
			var nodes []string
			for _, n := range g.Nodes() {
				nodes = append(nodes, fmt.Sprintf("%s type=%s package=%s\n", n.ID, n.Type, n.Package))
			}
			sort.Strings(nodes)
			adtsourceGoldenCompare(t, "mcp_resolve_"+name, fakesap.Normalise(fmt.Sprintf(
				"=== nodes\n%s=== missed (%d)\n%s=== requests\n%s\n",
				strings.Join(nodes, ""), len(missed), strings.Join(missedLines, ""), strings.Join(srv.Log(), "\n"))))
		})
	}
}

// Method expressions for the handlers, so the table above reads as data.
func (s *Server) checkBoundaries() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.handleCheckBoundaries
}
func (s *Server) graphStats() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.handleGraphStats
}
func (s *Server) health() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.handleHealth
}
func (s *Server) whereUsedConfig() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.handleWhereUsedConfig
}
func (s *Server) loads() func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return s.handleLoads
}
