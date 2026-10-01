package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// The SAP tool description and the rfc help must not advertise per-call
// destination overrides: a host, sysnr or port other than the server's own
// gateway is refused.
func TestRFCDocs_NoDestinationOverridesAdvertised(t *testing.T) {
	s := NewServer(&Config{BaseURL: "https://sap.example.com:44300", Username: "u", Password: "p", Client: "001", Language: "EN", Mode: "hyperfocused"})
	raw := s.mcpServer.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	resp, ok := raw.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("expected JSONRPCResponse, got %T", raw)
	}
	var tools []mcp.Tool
	switch r := resp.Result.(type) {
	case mcp.ListToolsResult:
		tools = r.Tools
	case *mcp.ListToolsResult:
		tools = r.Tools
	}
	var desc string
	for _, tool := range tools {
		if tool.Name == "SAP" {
			desc = tool.Description
		}
	}
	if desc == "" {
		t.Fatal("no SAP tool registered")
	}
	if strings.Contains(desc, "destination overrides") {
		t.Error("the SAP tool description still advertises destination overrides")
	}
	if !strings.Contains(desc, "own gateway") {
		t.Error("the SAP tool description does not say rfc goes to the server's own gateway")
	}

	help := toolResultText(t, handleHelp("rfc"))
	for _, want := range []string{"may\nonly repeat that destination", "--read-only", "--block-free-sql"} {
		if !strings.Contains(help, want) {
			t.Errorf("rfc help does not say %q", want)
		}
	}
}
