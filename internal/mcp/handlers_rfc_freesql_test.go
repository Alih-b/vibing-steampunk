package mcp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func rfcFreeSQLServer(t *testing.T, block bool) *Server {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	return NewServer(&Config{
		BaseURL:      "http://127.0.0.1:1",
		Username:     "u",
		Password:     "p",
		Client:       "001",
		Language:     "EN",
		BlockFreeSQL: block,
	})
}

// A caller's WHERE on read_table is a free query; --block-free-sql refuses it
// before the gateway is dialled.
func TestRFCReadTable_CallerWhereRefusedUnderBlockFreeSQL(t *testing.T) {
	s := rfcFreeSQLServer(t, true)
	for _, op := range []string{"read_table", "read-table", "table"} {
		t.Run(op, func(t *testing.T) {
			port, dials := fakeGateway(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, handled, err := s.routeRFCAction(ctx, "rfc", "USR02", "", rfcParams(t, port, map[string]any{"op": op, "where": "BNAME = 'TESTUSER'"}))
			if !handled {
				t.Fatal("rfc action not handled")
			}
			if err == nil || !strings.Contains(err.Error(), "blocked by safety configuration") || !strings.Contains(err.Error(), "type F") {
				t.Fatalf("want a free-SQL refusal, got %v", err)
			}
			if n := dials(); n != 0 {
				t.Errorf("refused read_table still dialled the gateway %d time(s)", n)
			}
		})
	}
}

// No caller WHERE, search's own TFDIR filter, or free SQL allowed: all reach
// the gateway.
func TestRFCReadTable_OtherReadsStillReachGateway(t *testing.T) {
	cases := map[string]struct {
		block  bool
		target string
		extra  map[string]any
	}{
		"no where":             {true, "T000", map[string]any{"op": "read_table", "top": 1.0}},
		"blank where":          {true, "T000", map[string]any{"op": "read_table", "where": "  "}},
		"search":               {true, "BAPI_USER_*", map[string]any{"op": "search"}},
		"where, sql unblocked": {false, "T000", map[string]any{"op": "read_table", "where": "MANDT = '001'"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := rfcFreeSQLServer(t, tc.block)
			port, dials := fakeGateway(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _, err := s.routeRFCAction(ctx, "rfc", tc.target, "", rfcParams(t, port, tc.extra))
			if err != nil && strings.Contains(err.Error(), "blocked") {
				t.Fatalf("refused: %v", err)
			}
			if dials() == 0 {
				t.Errorf("never reached the gateway (err %v)", err)
			}
		})
	}
}
