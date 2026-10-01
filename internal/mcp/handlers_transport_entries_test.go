package mcp

import (
	"reflect"
	"testing"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

func TestTransportEntries_AllForms(t *testing.T) {
	got, err := transportEntries(map[string]any{
		"objects": []any{
			"LIMU REPT ZDEMO",
			map[string]any{"object": "R3TR TABU ZDEMO_CONF", "keys": []any{"100KEY1"}},
		},
		"object": "TABU ZDEMO_OTHER",
		"keys":   "100*",
	})
	if err != nil {
		t.Fatalf("transportEntries: %v", err)
	}
	want := []adt.TransportEntry{
		{TransportObjectKey: adt.TransportObjectKey{PgmID: "LIMU", Object: "REPT", Name: "ZDEMO"}},
		{TransportObjectKey: adt.TransportObjectKey{PgmID: "R3TR", Object: "TABU", Name: "ZDEMO_CONF"}, Keys: []string{"100KEY1"}},
		{TransportObjectKey: adt.TransportObjectKey{PgmID: "R3TR", Object: "TABU", Name: "ZDEMO_OTHER"}, Keys: []string{"100*"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestTransportEntries_Refusals(t *testing.T) {
	for name, args := range map[string]map[string]any{
		"nothing":        {},
		"bad object":     {"objects": []any{"ZDEMO"}},
		"bad item":       {"objects": []any{42.0}},
		"keys not listy": {"object": "TABU ZDEMO_CONF", "keys": 5.0},
	} {
		if _, err := transportEntries(args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
