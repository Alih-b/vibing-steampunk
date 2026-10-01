package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

func TestFunctionGroupOf(t *testing.T) {
	for pool, want := range map[string]string{
		"SAPLW61V":     "W61V",
		"saplzdemo ":   "ZDEMO",
		"/NS/SAPLDEMO": "/NS/DEMO",
	} {
		if got := functionGroupOf(pool); got != want {
			t.Errorf("%q: %q, want %q", pool, got, want)
		}
	}
}

func TestEnhancedObjectURL_ByName(t *testing.T) {
	s := &Server{}
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"url as given":   {map[string]any{"object_url": "/sap/bc/adt/functions/groups/zdemo"}, "/sap/bc/adt/functions/groups/zdemo"},
		"function group": {map[string]any{"function_group": "/NS/DEMO"}, "/sap/bc/adt/functions/groups/%2Fns%2Fdemo"},
		"program":        {map[string]any{"program": "ZREPORT"}, "/sap/bc/adt/programs/programs/zreport"},
		"class":          {map[string]any{"class": "ZCL_DEMO"}, "/sap/bc/adt/oo/classes/zcl_demo"},
	} {
		got, err := s.enhancedObjectURL(context.Background(), tc.args)
		if err != nil || got != tc.want {
			t.Errorf("%s: %q %v, want %q", name, got, err, tc.want)
		}
	}
	if _, err := s.enhancedObjectURL(context.Background(), map[string]any{}); err == nil {
		t.Error("no object accepted")
	}
}

// Without the list of enhancement options neither the option nor its mode is
// known: the create stops there instead of making a plug-in of a guessed mode.
func TestCreateSourceCodePlugin_OptionsLookupFailureStops(t *testing.T) {
	var mu sync.Mutex
	posted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "TOKEN")
		switch {
		case strings.HasSuffix(r.URL.Path, "/enhancements/options"):
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/enhoxhh"):
			mu.Lock()
			posted = true
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	s := &Server{adtClient: adt.NewClient(srv.URL, "TESTUSER", "pw")}

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"name": "ZENH_DEMO", "description": "Demo", "package": "$TMP",
		"function_group": "ZDEMO", "option": `\FU:Z_DEMO\SE:BEGIN\EI`,
	}
	res, err := s.handleCreateSourceCodePlugin(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Errorf("a create without the options list is not an error: %+v", res.Content)
	}
	mu.Lock()
	defer mu.Unlock()
	if posted {
		t.Error("the plug-in was created although its option could not be checked")
	}
}
