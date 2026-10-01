package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// SAP(action="read", target="DEVC X", params={"inventory": true}) answers
// with the inventory; without the flag the package read is unchanged.
func TestReadPackageInventory(t *testing.T) {
	var freestyle int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "t")
		w.Header().Set("Content-Type", "application/xml")
		if strings.Contains(r.URL.Path, "freestyle") {
			freestyle++
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview"/>`))
			return
		}
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA><TREE_CONTENT>` +
			`<SEU_ADT_REPOSITORY_OBJ_NODE><OBJECT_TYPE>PROG/P</OBJECT_TYPE><OBJECT_NAME>ZDEMO_REPORT</OBJECT_NAME></SEU_ADT_REPOSITORY_OBJ_NODE>` +
			`</TREE_CONTENT></DATA></asx:values></asx:abap>`))
	}))
	defer ts.Close()

	for _, blocked := range []bool{false, true} {
		s := NewServer(&Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN", BlockFreeSQL: blocked})
		freestyle = 0
		res, err := s.handleUniversalTool(context.Background(), newRequest(map[string]any{
			"action": "read", "target": "DEVC $ZDEMO", "params": map[string]any{"inventory": true},
		}))
		if err != nil || res.IsError {
			t.Fatalf("blocked=%v: %v %s", blocked, err, resultText(res))
		}
		var inv struct {
			Package     string           `json:"package"`
			Objects     []map[string]any `json:"objects"`
			Subpackages []map[string]any `json:"subpackages"`
			Skipped     []string         `json:"skipped"`
		}
		if err := json.Unmarshal([]byte(resultText(res)), &inv); err != nil {
			t.Fatalf("blocked=%v: not an inventory: %v\n%s", blocked, err, resultText(res))
		}
		if inv.Package != "$ZDEMO" || inv.Objects == nil || inv.Subpackages == nil {
			t.Fatalf("blocked=%v: %s", blocked, resultText(res))
		}
		if blocked && (freestyle != 0 || len(inv.Skipped) == 0) {
			t.Fatalf("--block-free-sql: %d free SQL requests, skipped=%v", freestyle, inv.Skipped)
		}
		if !blocked && freestyle == 0 {
			t.Fatal("without --block-free-sql the inventory reads TADIR")
		}
	}

	s := NewServer(&Config{BaseURL: ts.URL, Username: "u", Password: "p", Client: "001", Language: "EN"})
	res, _ := s.handleUniversalTool(context.Background(), newRequest(map[string]any{"action": "read", "target": "DEVC $ZDEMO"}))
	if text := resultText(res); strings.Contains(text, `"abapgit_repos"`) || !strings.Contains(text, "ZDEMO_REPORT") {
		t.Fatalf("plain package read changed: %s", text)
	}
}
