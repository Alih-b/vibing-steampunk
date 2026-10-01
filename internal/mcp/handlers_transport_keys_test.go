package mcp

import (
	"strings"
	"testing"
)

// Table keys are taken as strings only: a JSON number would lose its leading
// zeroes and name another row.
func TestTransportEntries_KeysMustBeStrings(t *testing.T) {
	_, err := transportEntries(map[string]any{"object": "R3TR TABU ZDEMO_CONF", "keys": []any{"001KEY", float64(1)}})
	if err == nil || !strings.Contains(err.Error(), "not a string") {
		t.Fatalf("a numeric key: err = %v", err)
	}
	got, err := transportEntries(map[string]any{"object": "R3TR TABU ZDEMO_CONF", "keys": []any{"001KEY", "002*"}})
	if err != nil || len(got) != 1 || len(got[0].Keys) != 2 || got[0].Keys[0] != "001KEY" {
		t.Errorf("string keys: %+v %v", got, err)
	}
}
