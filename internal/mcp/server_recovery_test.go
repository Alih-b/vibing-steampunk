package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// A tool handler that panics must cost that one call, not the process. Before
// the server recovered, the panic went up through HandleMessage and ended vsp,
// taking every other tool with it (the crash class of issue #237).
func TestAPanickingToolHandlerReturnsAnError(t *testing.T) {
	s := NewServer(&Config{
		BaseURL:  "https://sap.example.com:44300",
		Username: "testuser",
		Password: "testpass",
		Client:   "001",
		Language: "EN",
	})
	s.mcpServer.AddTool(mcp.NewTool("ZZPanics"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var m map[string]int
		m["boom"] = 1 // a nil-map write: a real runtime panic, not a test stub
		return nil, nil
	})

	raw := s.mcpServer.HandleMessage(context.Background(), []byte(`{
		"jsonrpc": "2.0",
		"id": 1,
		"method": "tools/call",
		"params": {"name": "ZZPanics", "arguments": {}}
	}`))

	switch r := raw.(type) {
	case mcp.JSONRPCError:
		if !strings.Contains(r.Error.Message, "panic") {
			t.Errorf("the error should say a panic was recovered, got %q", r.Error.Message)
		}
	case mcp.JSONRPCResponse:
		res, ok := r.Result.(*mcp.CallToolResult)
		if !ok || !res.IsError {
			t.Errorf("expected an error result, got %#v", r.Result)
		}
	default:
		t.Fatalf("expected an error response, got %T", raw)
	}
}
