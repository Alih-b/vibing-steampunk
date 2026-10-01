package mcp

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

func TestReportParams_TheObjectForm(t *testing.T) {
	got, err := reportParams(map[string]any{
		"P_WERKS": "1000",
		"P_TEST":  true,
		"P_COUNT": float64(5),
		"S_MATNR": []any{"M1", "M2"},
	})
	if err != nil {
		t.Fatalf("reportParams: %v", err)
	}
	want := []saprfc.ReportParam{
		{Name: "P_COUNT", Kind: "P", Low: "5"},
		{Name: "P_TEST", Kind: "P", Low: "X"},
		{Name: "P_WERKS", Kind: "P", Low: "1000"},
		{Name: "S_MATNR", Kind: "S", Low: "M1"},
		{Name: "S_MATNR", Kind: "S", Low: "M2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestReportParams_TheRowForm(t *testing.T) {
	got, err := reportParams([]any{
		map[string]any{"name": "S_DATUM", "option": "BT", "low": "20260101", "high": "20260131"},
		map[string]any{"SELNAME": "P_WERKS", "LOW": "1000"},
		map[string]any{"name": "S_MATNR", "kind": "S", "sign": "E", "low": "M9"},
	})
	if err != nil {
		t.Fatalf("reportParams: %v", err)
	}
	want := []saprfc.ReportParam{
		{Name: "S_DATUM", Kind: "S", Option: "BT", Low: "20260101", High: "20260131"},
		{Name: "P_WERKS", Low: "1000"},
		{Name: "S_MATNR", Kind: "S", Sign: "E", Low: "M9"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestReportParams_Refusals(t *testing.T) {
	for name, in := range map[string]any{
		"range as an object": map[string]any{"S_DATUM": map[string]any{"low": "1"}},
		"row without a name": []any{map[string]any{"low": "1"}},
		"row not an object":  []any{"P_WERKS=1000"},
		"a bare string":      "P_WERKS=1000",
	} {
		if _, err := reportParams(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if got, err := reportParams(nil); err != nil || got != nil {
		t.Errorf("no params: %v, %v", got, err)
	}
}

// run starts a background job that does whatever the report does, so the
// safety configuration refuses it like ExecuteABAP, before any connection.
func TestRouteRFCAction_RunIsRefusedUnderTheSafetyConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"read-only", Config{ReadOnly: true}},
		{"workflow ops disallowed", Config{DisallowedOps: "W"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.BaseURL, cfg.Username, cfg.Password, cfg.Client = "https://unreachable.invalid:44300", "u", "p", "100"
			server := NewServer(&cfg)
			_, handled, err := server.routeRFCAction(context.Background(), "rfc", "ZREPORT", "", map[string]any{"op": "run"})
			if !handled {
				t.Fatal("rfc action not handled")
			}
			if err == nil || !strings.Contains(err.Error(), "blocked by safety configuration") {
				t.Fatalf("want run refused by the safety configuration, got %v", err)
			}
		})
	}
}
