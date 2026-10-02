package mcp

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// Help and routes, checked against each other in both directions.
//
// Help advertised analyze type=cds_impact, test target="ATC", system
// params.type=system_info and delete <TYPE> <NAME>, and none of them was
// routed: each answered "No handler found" for a call copied from the help.
// TestEveryDocumentedExampleIsAcceptedByItsHandler did not see them, because
// it reads only lines that are a call and nothing else (the tips write
// "4. CDS impact: SAP(...)"), and because the answer for an unrouted analyze
// or delete is "needs params" / "needs a target", not "no handler found".
//
// The other direction had drifted as quietly: routes nobody can find, such as
// analyze type=loads and type=effects, read target="CDS_ELEMENTS" and
// debug target="RUN_REPORT_ASYNC", worked and were documented nowhere.

// helpRouteServer is a hyperfocused server that can reach nothing and may
// change nothing: example.invalid does not resolve, and --read-only refuses
// every mutation before it is attempted.
func helpRouteServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(&Config{
		BaseURL: "https://example.invalid", Username: "tester", Password: "unused",
		Client: "000", Language: "EN", Mode: "hyperfocused", ReadOnly: true,
	})
}

// helpSources is every text help publishes, by name: each help topic, the
// overview, and the SAP tool's own description.
func helpSources(t *testing.T, s *Server) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, topic := range append(helpTopicsUnderTest(), "", "info") {
		out["help "+topic] = resultText(handleHelp(topic))
	}
	tool, ok := s.mcpServer.ListTools()["SAP"]
	if !ok {
		t.Fatal("hyperfocused mode registers no SAP tool")
	}
	out["SAP tool description"] = tool.Tool.Description
	return out
}

// sapCallRe finds the start of a call anywhere in a line.
var sapCallRe = regexp.MustCompile(`SAP\(action="`)

// sapCallsIn returns every SAP(action=..., ...) call in text, wherever it
// stands in its line. A call with nothing but an action is a topic heading
// -- SAP(action="lint") - Static analysis -- and is skipped.
func sapCallsIn(text string) []sapExample {
	var out []sapExample
	for _, line := range strings.Split(text, "\n") {
		for _, loc := range sapCallRe.FindAllStringIndex(line, -1) {
			call := balancedParens(line[loc[0]+len("SAP"):])
			if call == "" {
				continue
			}
			ex := sapExample{line: "SAP" + call}
			ex.action = quotedAfter(call, `action=`)
			ex.target = quotedAfter(call, `target=`)
			if i := strings.Index(call, "params="); i >= 0 {
				raw := balancedBraces(call[i+len("params="):])
				if raw == "" {
					continue
				}
				// A params object written with "..." or [...] is illustration;
				// the call is still checked for its route, without params.
				_ = json.Unmarshal([]byte(raw), &ex.params)
			} else if ex.target == "" {
				continue // heading
			}
			out = append(out, ex)
		}
	}
	return out
}

// balancedParens returns the leading (...) of s, ignoring parentheses inside
// double-quoted strings.
func balancedParens(s string) string {
	if !strings.HasPrefix(s, "(") {
		return ""
	}
	depth, inString, escaped := 0, false, false
	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inString = !inString
		case inString:
		case r == '(':
			depth++
		case r == ')':
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}
	return ""
}

// dispatch sends one call through handleUniversalTool and reports whether a
// route claimed it, and whether the route answered that the call was short
// of something. Unrouted is recognised exactly: the dispatcher's own answer
// when the chain runs out.
func dispatch(t *testing.T, s *Server, action, target string, params map[string]any) (routed, short bool, text string) {
	t.Helper()
	args := map[string]any{"action": action}
	if target != "" {
		args["target"] = target
	}
	if params != nil {
		args["params"] = mergeArgs(nil, params)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := s.handleUniversalTool(ctx, newRequest(args))
	if err != nil {
		return true, false, err.Error()
	}
	text = resultText(res)
	act := strings.ToLower(strings.TrimSpace(action))
	tgt, _ := queryTargetSQL(act, target, params)
	objType, objName := parseTarget(tgt)
	if text == getUnhandledErrorMessage(act, objType, objName) {
		return false, false, text
	}
	return true, strings.Contains(text, " needs one of: "), text
}

// TestHelpAdvertisesOnlyRoutedCalls: every call help shows reaches a route,
// and every list of targets or types the "no handler" answer prints is routed.
func TestHelpAdvertisesOnlyRoutedCalls(t *testing.T) {
	s := helpRouteServer(t)

	sources := helpSources(t, s)
	actions := strings.Split(strings.TrimSuffix(strings.TrimPrefix(validActionsLine, "Valid actions: "), "\n"), ", ")
	for _, a := range actions {
		sources["no-handler answer for "+a] = getUnhandledErrorMessage(a, "", "")
	}
	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	sort.Strings(names)

	checked := 0
	for _, src := range names {
		for _, ex := range sapCallsIn(sources[src]) {
			switch ex.action {
			case "", "help", "info":
				continue
			}
			checked++
			routed, short, text := dispatch(t, s, ex.action, ex.target, ex.params)
			if !routed {
				t.Errorf("%s advertises a call no route takes:\n  %s\n  -> %s", src, ex.line, firstLine(text))
			} else if short && ex.params != nil {
				t.Errorf("%s advertises a call its route says is incomplete:\n  %s\n  -> %s", src, ex.line, firstLine(text))
			}
		}
	}
	t.Logf("checked %d advertised calls", checked)
	if checked < 100 {
		t.Fatalf("only %d calls were extracted from help; the parser has stopped finding them", checked)
	}

	// The lists the "no handler" answer prints for each action.
	dir := t.TempDir()
	targetList := regexp.MustCompile(`Supported \w+ targets: (.*)`)
	typeList := regexp.MustCompile(`(?s)\(params\.type\): (.*?)\nUse SAP`)
	word := regexp.MustCompile(`[A-Za-z][A-Za-z0-9_]*\*?`)
	for _, a := range actions {
		msg := getUnhandledErrorMessage(a, "", "")
		if m := targetList.FindStringSubmatch(msg); m != nil {
			for _, tgt := range word.FindAllString(m[1], -1) {
				if strings.HasSuffix(tgt, "*") || tgt == "TYPE" || tgt == "NAME" || tgt == "with" || tgt == "params" || tgt == "object_url" {
					continue
				}
				routedBare, _, _ := dispatch(t, s, a, tgt, synthBag(dir))
				routedNamed, _, text := dispatch(t, s, a, tgt+" ZDEMO_X", synthBag(dir))
				if !routedBare && !routedNamed {
					t.Errorf("the no-handler answer for %s lists target %s, which no route takes -> %s", a, tgt, firstLine(text))
				}
			}
		}
		if m := typeList.FindStringSubmatch(msg); m != nil {
			for _, typ := range word.FindAllString(m[1], -1) {
				if routed, _, text := dispatch(t, s, a, "", map[string]any{"type": typ}); !routed {
					t.Errorf("the no-handler answer for %s lists type %s, which no route takes -> %s", a, typ, firstLine(text))
				}
			}
		}
	}
}

// helpUnadvertised are routes help deliberately does not show, each with the
// reason. An entry that help starts to mention, or that is no longer routed,
// fails the test, so the list cannot go stale.
var helpUnadvertised = map[string]string{
	"installed_components":  "alias of system params.type=components",
	"connection_info":       "alias of system params.type=connection",
	"add_to_transport":      "alias of system type=add_transport_object",
	"remove_from_transport": "alias of system type=remove_transport_object",
	"move_object":           "alias of system type=move_transport_object",
	"transport_of_copies":   "alias of system type=copy_to_toc",
	"read-table":            "alias of rfc op=read_table",
	"unit":                  "test type=unit is what test does when type is left out",
	"text_pool":             "alias of i18n op=texts_get",
	"write_text_pool":       "alias of i18n op=texts_set",
	"write_labels":          "refuses by design (help i18n says why); no call is shown for it",
	"recover_failed_create": "operator recovery that deletes the named object if it exists; not offered to a model",
	"coverage":              "takes an object URL as target, which parseTarget upper-cases; not shown until that form is verified",
	"api_state":             "takes an object URL as target, which parseTarget upper-cases; not shown until that form is verified",
	"ui5_list_apps":         "params.type spelling of read target=\"UI5_LIST\"",
	"ui5_get_app":           "params.type spelling of read target=\"UI5_APP\"",
	"ui5_get_file":          "params.type spelling of read target=\"UI5_FILE\"",
	"ui5_delete_file":       "params.type spelling of delete target=\"UI5_FILE\"",
	"ui5_delete_app":        "params.type spelling of delete target=\"UI5_APP\"",
	"ui5_upload":            "UI5 repository writes are unverified on a live system",
	"ui5_upload_file":       "UI5 repository writes are unverified on a live system",
	"ui5_create_app":        "UI5 repository writes are unverified on a live system",
}

// TestEveryRouteIsInHelp: every literal a route matches an action, target
// type, type or op against, and every type in the routing tables, is shown
// somewhere in help -- as "x", as target="X ..." or as "X ..." -- or is in
// helpUnadvertised with a reason.
func TestEveryRouteIsInHelp(t *testing.T) {
	s := helpRouteServer(t)
	var corpus strings.Builder
	for _, text := range helpSources(t, s) {
		corpus.WriteString(strings.ToLower(text))
		corpus.WriteString("\n")
	}
	low := corpus.String()
	mentioned := func(lit string) bool {
		l := strings.ToLower(lit)
		return strings.Contains(low, `"`+l+`"`) || strings.Contains(low, `"`+l+` `) || strings.Contains(low, `target="`+l)
	}

	routed := map[string]string{} // lower-case literal -> where it is routed
	lits, _ := routedLiterals(t)
	for fn, set := range lits {
		for lit := range set {
			routed[strings.ToLower(lit)] = fn
		}
	}
	for name, table := range map[string][]string{
		"analyze type": s.AnalyzeTypes(),
		"i18n op":      keys(s.i18nTypes()),
		"revisions op": keys(s.revisionTypes()),
		"action":       keys(s.lintTypes()),
	} {
		for _, k := range table {
			routed[strings.ToLower(k)] = name
		}
	}

	var missing []string
	for lit, where := range routed {
		if mentioned(lit) {
			if _, ok := helpUnadvertised[lit]; ok {
				t.Errorf("helpUnadvertised lists %q, which help now shows: remove it from the list", lit)
			}
			continue
		}
		if _, ok := helpUnadvertised[lit]; ok {
			continue
		}
		missing = append(missing, where+": "+lit)
	}
	for lit := range helpUnadvertised {
		if _, ok := routed[lit]; !ok {
			t.Errorf("helpUnadvertised lists %q, which no route matches any more: remove it", lit)
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("routed but shown nowhere in help (add a call to the topic, or a reason to helpUnadvertised): %s", m)
	}
}

func keys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
