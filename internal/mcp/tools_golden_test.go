package mcp

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
)

var updateToolsGolden = flag.Bool("update-tools-golden", false, "rewrite testdata/tools_golden/*.json from the live registry")

// toolsGoldenCases are the registry shapes the golden snapshots pin: every
// mode and the documented --disabled-groups example. (Read-only does not
// change registration; it is enforced at call time.)
var toolsGoldenCases = []struct {
	file string
	cfg  Config
}{
	{"focused.json", Config{Mode: "focused"}},
	{"expert.json", Config{Mode: "expert"}},
	{"hyperfocused.json", Config{Mode: "hyperfocused"}},
	{"focused_disabled_5THD.json", Config{Mode: "focused", DisabledGroups: "5THD"}},
	{"expert_disabled_5THD.json", Config{Mode: "expert", DisabledGroups: "5THD"}},
}

// toolsSnapshot renders the full registry: each tool's complete schema plus
// the handler it routes to, sorted by name (tools/list order).
func toolsSnapshot(t *testing.T, cfg Config) []byte {
	t.Helper()
	cfg.BaseURL = "https://example.invalid"
	cfg.Username = "tester"
	cfg.Password = "unused"
	cfg.Client = "000"
	cfg.Language = "EN"
	s := NewServer(&cfg)

	type entry struct {
		Tool    any    `json:"tool"`
		Handler string `json:"handler"`
	}
	registered := s.mcpServer.ListTools()
	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]entry, 0, len(names))
	for _, name := range names {
		st := registered[name]
		h := runtime.FuncForPC(reflect.ValueOf(st.Handler).Pointer()).Name()
		out = append(out, entry{Tool: st.Tool, Handler: h})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestToolRegistryGolden pins every registered tool's schema and handler so
// the registry cannot drift silently. After an intended change, regenerate
// with: go test ./internal/mcp -run TestToolRegistryGolden -update-tools-golden
func TestToolRegistryGolden(t *testing.T) {
	for _, tc := range toolsGoldenCases {
		t.Run(tc.file, func(t *testing.T) {
			got := toolsSnapshot(t, tc.cfg)
			path := filepath.Join("testdata", "tools_golden", tc.file)
			if *updateToolsGolden {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update-tools-golden to create it)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("tool registry for %s differs from %s; if intended, rerun with -update-tools-golden", tc.file, path)
			}
		})
	}
}
