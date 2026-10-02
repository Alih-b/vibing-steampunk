// Package mcp provides the MCP server implementation for ABAP ADT tools.
// tools_git.go registers abapGit tools.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// registerGitTools registers Git/abapGit integration tools.
func (s *Server) registerGitTools(shouldRegister func(string) bool) {
	if shouldRegister("GitTypes") {
		s.mcpServer.AddTool(mcp.NewTool("GitTypes",
			mcp.WithDescription("Get list of supported abapGit object types. Returns 158 object types that can be exported/imported via abapGit. Requires abapGit to be installed on SAP system."),
		), s.handleGitTypes)
	}

	if shouldRegister("GitExport") {
		s.mcpServer.AddTool(mcp.NewTool("GitExport",
			mcp.WithDescription("Export ABAP objects as abapGit-compatible ZIP. Supports 158 object types. Saves ZIP file to output_dir (default: current directory). Use packages OR objects parameter."),
			mcp.WithString("packages",
				mcp.Description("Comma-separated package names to export (e.g., '$ZRAY,$TMP'). Supports wildcards."),
			),
			mcp.WithString("objects",
				mcp.Description("JSON array of objects: [{\"type\":\"CLAS\",\"name\":\"ZCL_TEST\"}]"),
			),
			mcp.WithBoolean("include_subpackages",
				mcp.Description("Include subpackages when exporting by package (default: true)"),
			),
			mcp.WithString("output_dir",
				mcp.Description("Output directory for ZIP file (default: current directory)"),
			),
		), s.handleGitExport)
	}
}
