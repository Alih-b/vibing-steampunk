// Package mcp provides the MCP server implementation for ABAP ADT tools.
// tools_search.go registers search and grep tools.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// registerSearchTools registers object search tools.
func (s *Server) registerSearchTools(shouldRegister func(string) bool) {
	if shouldRegister("SearchObject") {
		s.mcpServer.AddTool(mcp.NewTool("SearchObject",
			mcp.WithDescription("Search for ABAP objects using quick search"),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("Search query string (use * wildcard for partial match)"),
			),
			mcp.WithNumber("maxResults",
				mcp.Description("Maximum number of results to return (default 100)"),
			),
			mcp.WithString("objectType",
				mcp.Description("Only objects of this type (CLAS, PROG, INTF, FUGR, ...)"),
			),
			mcp.WithBoolean("exact",
				mcp.Description("Only objects whose name equals query, ignoring case (no wildcards). Combines with objectType and maxResults. The name is searched without a wildcard. If the search returns its full window of 1000 matches, the answer says so (inconclusive without an equal name, an incomplete note next to the results with one) — pass objectType to narrow."),
			),
		), s.handleSearchObject)
	}
}

// registerGrepTools registers grep/search tools.
func (s *Server) registerGrepTools(shouldRegister func(string) bool) {
	if shouldRegister("GrepObject") {
		s.mcpServer.AddTool(mcp.NewTool("GrepObject",
			mcp.WithDescription("Search for regex pattern in a single ABAP object's source code. Returns matches with line numbers and optional context. Use for finding TODO comments, string literals, patterns, or code snippets before editing."),
			mcp.WithString("object_url",
				mcp.Required(),
				mcp.Description("ADT URL of object (e.g., /sap/bc/adt/programs/programs/ZTEST)"),
			),
			mcp.WithString("pattern",
				mcp.Required(),
				mcp.Description("Regular expression pattern (Go regexp syntax). Examples: 'TODO', 'lv_\\w+', 'SELECT.*FROM'"),
			),
			mcp.WithBoolean("case_insensitive",
				mcp.Description("If true, perform case-insensitive matching. Default: false"),
			),
			mcp.WithNumber("context_lines",
				mcp.Description("Number of lines to show before/after each match (like grep -C). Default: 0"),
			),
		), s.handleGrepObject)
	}

	if shouldRegister("GrepPackage") {
		s.mcpServer.AddTool(mcp.NewTool("GrepPackage",
			mcp.WithDescription("Search for regex pattern across all source objects in an ABAP package. Returns matches grouped by object. Use for package-wide analysis, finding patterns across multiple programs/classes."),
			mcp.WithString("package_name",
				mcp.Required(),
				mcp.Description("Package name (e.g., $TMP, ZPACKAGE)"),
			),
			mcp.WithString("pattern",
				mcp.Required(),
				mcp.Description("Regular expression pattern (Go regexp syntax). Examples: 'TODO', 'lv_\\w+', 'SELECT.*FROM'"),
			),
			mcp.WithBoolean("case_insensitive",
				mcp.Description("If true, perform case-insensitive matching. Default: false"),
			),
			mcp.WithString("object_types",
				mcp.Description("Comma-separated object types to search (e.g., 'PROG/P,CLAS/OC'). Empty = search all source objects. Valid: PROG/P, CLAS/OC, INTF/OI, FUGR/F, FUGR/FF, PROG/I"),
			),
			mcp.WithNumber("max_results",
				mcp.Description("Maximum number of matching objects to return. 0 = unlimited. Default: 100"),
			),
		), s.handleGrepPackage)
	}

	if shouldRegister("GrepObjects") {
		s.registerGrepObjects()
	}

	if shouldRegister("GrepPackages") {
		s.registerGrepPackages()
	}
}
