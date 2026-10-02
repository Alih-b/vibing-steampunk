package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// --block-free-sql governs every statement the data preview is sent, not
// only the ones that arrive as action="query" target="SQL". A table read that
// carries a statement sends it in the body of /datapreview/ddic, where SAP
// runs it as given, so query target="T000", target="TABL T000" and
// target="TABL_CONTENTS T000" with params.sql are free SQL too.

func blockFreeSQLServer(t *testing.T, block bool) (*Server, *int64) {
	t.Helper()
	var n int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&n, 1)
		w.Header().Set("X-CSRF-Token", "TOKEN")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	s := NewServer(&Config{
		BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN",
		Mode: "hyperfocused", BlockFreeSQL: block,
	})
	return s, &n
}

func TestQueryTableWithSQLRefusedUnderBlockFreeSQL(t *testing.T) {
	for _, target := range []string{"T000", "TABL T000", "TABL_CONTENTS T000"} {
		t.Run(target, func(t *testing.T) {
			s, sent := blockFreeSQLServer(t, true)
			res, err := s.handleUniversalTool(context.Background(), newRequest(map[string]any{
				"action": "query", "target": target,
				"params": map[string]any{"sql": "SELECT * FROM t000 WHERE mandt = '000'"},
			}))
			if err != nil {
				t.Fatal(err)
			}
			text := resultText(res)
			if !res.IsError || !strings.Contains(strings.ToLower(text), "safety configuration") {
				t.Errorf("want a refusal from the safety configuration, got: %s", text)
			}
			if got := atomic.LoadInt64(sent); got != 0 {
				t.Errorf("%d request(s) reached SAP; --block-free-sql must refuse before anything is sent", got)
			}
		})
	}
}

// A plain table read sends no statement and stays allowed.
func TestQueryTableWithoutSQLAllowedUnderBlockFreeSQL(t *testing.T) {
	for _, target := range []string{"T000", "TABL T000", "TABL_CONTENTS T000"} {
		t.Run(target, func(t *testing.T) {
			s, sent := blockFreeSQLServer(t, true)
			res, err := s.handleUniversalTool(context.Background(), newRequest(map[string]any{
				"action": "query", "target": target,
			}))
			if err != nil {
				t.Fatal(err)
			}
			if text := resultText(res); strings.Contains(strings.ToLower(text), "safety configuration") {
				t.Errorf("a table read without a statement was refused: %s", text)
			}
			if atomic.LoadInt64(sent) == 0 {
				t.Error("a table read without a statement sent nothing")
			}
		})
	}
}
