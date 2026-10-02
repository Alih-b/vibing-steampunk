package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestNewToolResultError(t *testing.T) {
	result := newToolResultError("test error message")

	if result == nil {
		t.Fatal("newToolResultError returned nil")
	}

	if !result.IsError {
		t.Error("IsError should be true")
	}

	if len(result.Content) != 1 {
		t.Fatalf("Expected 1 content item, got %d", len(result.Content))
	}

	textContent, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("Content should be TextContent, got %T", result.Content[0])
	}

	if textContent.Text != "test error message" {
		t.Errorf("Text = %v, want 'test error message'", textContent.Text)
	}
}

func TestConfig(t *testing.T) {
	cfg := &Config{
		BaseURL:            "https://sap.example.com:44300",
		Username:           "testuser",
		Password:           "testpass",
		Client:             "100",
		Language:           "DE",
		InsecureSkipVerify: true,
	}

	if cfg.BaseURL != "https://sap.example.com:44300" {
		t.Errorf("BaseURL = %v, want https://sap.example.com:44300", cfg.BaseURL)
	}
	if cfg.Username != "testuser" {
		t.Errorf("Username = %v, want testuser", cfg.Username)
	}
	if cfg.Password != "testpass" {
		t.Errorf("Password = %v, want testpass", cfg.Password)
	}
	if cfg.Client != "100" {
		t.Errorf("Client = %v, want 100", cfg.Client)
	}
	if cfg.Language != "DE" {
		t.Errorf("Language = %v, want DE", cfg.Language)
	}
	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify should be true")
	}
}

func TestNewServer(t *testing.T) {
	cfg := &Config{
		BaseURL:  "https://sap.example.com:44300",
		Username: "testuser",
		Password: "testpass",
		Client:   "001",
		Language: "EN",
	}

	server := NewServer(cfg)

	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	if server.mcpServer == nil {
		t.Error("MCP server should not be nil")
	}
	if server.adtClient == nil {
		t.Error("ADT client should not be nil")
	}
}

func TestDebuggerGetVariablesSchemaIncludesItems(t *testing.T) {
	cfg := &Config{
		BaseURL:  "https://sap.example.com:44300",
		Username: "testuser",
		Password: "testpass",
		Client:   "001",
		Language: "EN",
	}

	server := NewServer(cfg)
	if server == nil || server.mcpServer == nil {
		t.Fatal("server or MCP server is nil")
	}

	rawResponse := server.mcpServer.HandleMessage(context.Background(), []byte(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/list",
		"params": {}
	}`))

	response, ok := rawResponse.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", rawResponse)
	}

	var tools []mcp.Tool
	switch result := response.Result.(type) {
	case mcp.ListToolsResult:
		tools = result.Tools
	case *mcp.ListToolsResult:
		tools = result.Tools
	default:
		t.Fatalf("expected ListToolsResult, got %T", response.Result)
	}

	var debuggerTool *mcp.Tool
	for i := range tools {
		if tools[i].Name == "DebuggerGetVariables" {
			debuggerTool = &tools[i]
			break
		}
	}
	if debuggerTool == nil {
		t.Fatal("DebuggerGetVariables tool not found")
	}

	variableIDsRaw, ok := debuggerTool.InputSchema.Properties["variable_ids"]
	if !ok {
		t.Fatal("variable_ids property not found in DebuggerGetVariables schema")
	}

	variableIDs, ok := variableIDsRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected variable_ids schema to be map[string]interface{}, got %T", variableIDsRaw)
	}

	if variableIDs["type"] != "array" {
		t.Fatalf("expected variable_ids type to be 'array', got %v", variableIDs["type"])
	}

	itemsRaw, ok := variableIDs["items"]
	if !ok {
		t.Fatal("variable_ids array schema is missing items")
	}

	items, ok := itemsRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected items to be map[string]interface{}, got %T", itemsRaw)
	}

	if items["type"] != "string" {
		t.Fatalf("expected variable_ids.items.type to be 'string', got %v", items["type"])
	}
}

// TestUniversalToolDispatchesAdvertisedForms drives the hyperfocused SAP tool
// against a dead URL: a call that is wired fails at the network, a call that is
// not wired answers "No handler found" without ever getting there.
func TestUniversalToolDispatchesAdvertisedForms(t *testing.T) {
	cfg := &Config{
		BaseURL:  "http://127.0.0.1:1",
		Username: "probe",
		Password: "probe",
		Client:   "001",
		Language: "EN",
	}
	s := NewServer(cfg)

	cases := []struct {
		name string
		args map[string]any
	}{
		{"system info by params", map[string]any{"action": "system", "params": map[string]any{"type": "system_info"}}},
		{"system components by params", map[string]any{"action": "system", "params": map[string]any{"type": "components"}}},
		{"system connection by params", map[string]any{"action": "system", "params": map[string]any{"type": "connection"}}},
		{"system info by target", map[string]any{"action": "system", "target": "INFO"}},
		{"query sql_query", map[string]any{"action": "query", "params": map[string]any{"sql_query": "SELECT * FROM T000"}}},
		{"query alias query", map[string]any{"action": "query", "params": map[string]any{"query": "SELECT * FROM T000"}}},
		{"query sql in target", map[string]any{"action": "query", "target": "SELECT * FROM T000"}},
		{"query table in target", map[string]any{"action": "query", "target": "T000"}},
		{"query table contents", map[string]any{"action": "query", "target": "TABL_CONTENTS T000"}},
		{"test atc by target", map[string]any{"action": "test", "target": "ATC", "params": map[string]any{"object_uri": "/sap/bc/adt/oo/classes/zcl_test"}}},
		{"test atc by type", map[string]any{"action": "test", "params": map[string]any{"type": "atc", "object_url": "/sap/bc/adt/oo/classes/zcl_test"}}},
		{"test unit tests", map[string]any{"action": "test", "params": map[string]any{"object_url": "/sap/bc/adt/oo/classes/zcl_test"}}},
		{"analyze cds_impact", map[string]any{"action": "analyze", "params": map[string]any{"type": "cds_impact", "cds_view": "ZDDL_VIEW"}}},
		{"grep by object_name", map[string]any{"action": "grep", "params": map[string]any{"object_name": "ZCL_TEST", "object_type": "CLAS", "pattern": "MODIFY"}}},
		{"grep by target", map[string]any{"action": "grep", "target": "CLAS ZCL_TEST", "params": map[string]any{"pattern": "MODIFY"}}},
		{"grep by object_url", map[string]any{"action": "grep", "params": map[string]any{"object_url": "/sap/bc/adt/oo/classes/zcl_test", "pattern": "MODIFY"}}},
		{"search", map[string]any{"action": "search", "target": "ZCL_*"}},
		{"read class", map[string]any{"action": "read", "target": "CLAS ZCL_TEST"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.handleUniversalTool(context.Background(), newRequest(tc.args))
			if err != nil {
				t.Fatalf("handleUniversalTool returned an error: %v", err)
			}
			// Unrouted is the dispatcher's own answer when the chain runs
			// out, which for analyze and system is "needs ..." rather than
			// "No handler found".
			text := resultText(result)
			action, _ := tc.args["action"].(string)
			target, _ := tc.args["target"].(string)
			params, _ := tc.args["params"].(map[string]any)
			target, _ = queryTargetSQL(action, target, params)
			objectType, objectName := parseTarget(target)
			if text == getUnhandledErrorMessage(action, objectType, objectName) || strings.Contains(text, "No handler found") {
				t.Errorf("action was not dispatched: %s", text)
			}
			if strings.Contains(text, " needs one of: ") {
				t.Errorf("the route took the action but not this form of it: %s", text)
			}
		})
	}
}

// TestUniversalToolATCNotMistakenForUnitTest pins the routing order: target
// "ATC" with an object_url is an ATC run, not a unit-test run.
func TestUniversalToolATCNotMistakenForUnitTest(t *testing.T) {
	s := NewServer(&Config{BaseURL: "http://127.0.0.1:1", Username: "probe", Password: "probe", Client: "001", Language: "EN"})

	for _, key := range []string{"object_url", "object_uri"} {
		result, err := s.handleUniversalTool(context.Background(), newRequest(map[string]any{
			"action": "test",
			"target": "ATC",
			"params": map[string]any{key: "/sap/bc/adt/oo/classes/zcl_test"},
		}))
		if err != nil {
			t.Fatalf("handleUniversalTool returned an error: %v", err)
		}
		text := resultText(result)
		if strings.Contains(text, "No handler found") {
			t.Errorf("%s: ATC was not dispatched: %s", key, text)
		}
		if !strings.Contains(text, "ATC") {
			t.Errorf("%s: expected an ATC run, got: %s", key, text)
		}
	}
}

// TestUniversalToolQueryParamMapping checks that the SQL reaches the handler
// unchanged whichever documented spelling it arrived under.
func TestUniversalToolQueryParamMapping(t *testing.T) {
	const stmt = "SELECT * FROM T000 WHERE mandt = 'abc'"

	if !looksLikeSQL(stmt) {
		t.Error("looksLikeSQL should accept a SELECT statement")
	}
	if looksLikeSQL("CLAS ZCL_TEST") || looksLikeSQL("T000") {
		t.Error("looksLikeSQL should reject an object reference")
	}

	params := map[string]any{"object_uri": "/sap/bc/adt/oo/classes/zcl_test"}
	mapped := paramsWithAlias(params, "object_url", "object_uri")
	if mapped["object_url"] != "/sap/bc/adt/oo/classes/zcl_test" {
		t.Errorf("object_url = %v, want the value of object_uri", mapped["object_url"])
	}
	if _, ok := params["object_url"]; ok {
		t.Error("paramsWithAlias must not modify the input map")
	}

	kept := map[string]any{"object_url": "a", "object_uri": "b"}
	if paramsWithAlias(kept, "object_url", "object_uri")["object_url"] != "a" {
		t.Error("an existing value must win over its alias")
	}
}

// TestQueryTargetSQLExplicitStatementWins pins that a statement passed in
// params, under any accepted name, is not shadowed by SQL in target, and that
// the caller's map is not written into.
func TestQueryTargetSQLExplicitStatementWins(t *testing.T) {
	for _, key := range []string{"sql_query", "sql", "query", "statement"} {
		params := map[string]any{key: "SELECT mandt FROM t000"}
		target, out := queryTargetSQL("query", "SELECT * FROM T100", params)
		if target != "SQL" {
			t.Errorf("%s: target = %q, want SQL", key, target)
		}
		if got := firstParam(out, "sql_query", "sql", "query", "statement"); got != "SELECT mandt FROM t000" {
			t.Errorf("%s: statement = %q, want the one passed in params", key, got)
		}
	}
	params := map[string]any{}
	target, out := queryTargetSQL("query", "SELECT * FROM T000", params)
	if target != "SQL" || out["sql_query"] != "SELECT * FROM T000" {
		t.Errorf("SQL-only target: got target=%q sql_query=%v", target, out["sql_query"])
	}
	if len(params) != 0 {
		t.Errorf("queryTargetSQL wrote into the caller's map: %v", params)
	}
	if target, _ := queryTargetSQL("read", "SELECT * FROM T000", map[string]any{}); target != "SELECT * FROM T000" {
		t.Errorf("a non-query action must keep its target, got %q", target)
	}
}

// TestGrepBareTargetRefused pins that a one-word grep target is refused by
// name instead of being guessed to be a class.
func TestGrepBareTargetRefused(t *testing.T) {
	s := NewServer(&Config{BaseURL: "http://127.0.0.1:1", Username: "probe", Password: "probe", Client: "001", Language: "EN"})
	result, err := s.handleUniversalTool(context.Background(), newRequest(map[string]any{
		"action": "grep", "target": "$TMP", "params": map[string]any{"pattern": "SELECT"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(result)
	if !result.IsError || !strings.Contains(text, "does not say what kind of object") {
		t.Errorf("bare grep target: want a refusal naming the ambiguity, got: %s", text)
	}
}
