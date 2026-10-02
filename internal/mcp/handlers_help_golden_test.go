package mcp

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Help output is pinned by a snapshot of hashes, so that a change to any help
// text, overview or no-handler answer is deliberate. After an intended edit:
//
//	go test ./internal/mcp -run TestHelpGolden -update-help-golden
//
// and commit testdata/help_golden.sha256 with it.
var updateHelpGolden = flag.Bool("update-help-golden", false, "rewrite testdata/help_golden.sha256")

const helpGoldenPath = "testdata/help_golden.sha256"

// helpGoldenOutputs is everything handlers_help.go answers, by case name.
func helpGoldenOutputs(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	topics := []string{
		"read", "edit", "create", "delete", "search", "query", "test", "info",
		"rfc", "i18n", "revisions", "history", "lint", "grep", "debug",
		"analyze", "system", "tips", "best_practices", "workflows", "best",
		"", "help", "unknown-topic", "  READ  ", "Tips", "\tDelete\n",
	}
	for _, topic := range topics {
		raw, err := json.Marshal(handleHelp(topic))
		if err != nil {
			t.Fatalf("marshal help %q: %v", topic, err)
		}
		out[fmt.Sprintf("help %q", topic)] = string(raw)
	}
	actions := strings.Split(strings.TrimSuffix(strings.TrimPrefix(validActionsLine, "Valid actions: "), "\n"), ", ")
	actions = append(actions, "", "bogus")
	for _, a := range actions {
		for _, tgt := range [][2]string{{"", ""}, {"CLAS", ""}, {"CLAS", "ZCL_X"}} {
			out[fmt.Sprintf("unhandled %q %q %q", a, tgt[0], tgt[1])] = getUnhandledErrorMessage(a, tgt[0], tgt[1])
		}
	}
	return out
}

func TestHelpGolden(t *testing.T) {
	got := map[string]string{}
	for name, text := range helpGoldenOutputs(t) {
		sum := sha256.Sum256([]byte(text))
		got[name] = hex.EncodeToString(sum[:])
	}

	if *updateHelpGolden {
		names := make([]string, 0, len(got))
		for name := range got {
			names = append(names, name)
		}
		sort.Strings(names)
		var b strings.Builder
		for _, name := range names {
			fmt.Fprintf(&b, "%s  %s\n", got[name], name)
		}
		if err := os.MkdirAll(filepath.Dir(helpGoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(helpGoldenPath, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	f, err := os.Open(helpGoldenPath)
	if err != nil {
		t.Fatalf("%v (run with -update-help-golden to create it)", err)
	}
	defer f.Close()
	want := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		sum, name, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			t.Fatalf("malformed line in %s: %q", helpGoldenPath, sc.Text())
		}
		want[name] = sum
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	for name, sum := range got {
		if w, ok := want[name]; !ok {
			t.Errorf("%s: not in %s", name, helpGoldenPath)
		} else if w != sum {
			t.Errorf("%s: output changed; if intended, rerun with -update-help-golden", name)
		}
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("%s: in %s but no longer produced", name, helpGoldenPath)
		}
	}
}
