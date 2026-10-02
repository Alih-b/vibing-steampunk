// Package mcp provides the MCP server implementation for ABAP ADT tools.
// tools_ui5.go registers UI5/BSP tools.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// registerUI5Tools registers UI5/Fiori BSP management tools.
func (s *Server) registerUI5Tools(shouldRegister func(string) bool) {
	if shouldRegister("UI5ListApps") {
		s.mcpServer.AddTool(mcp.NewTool("UI5ListApps",
			mcp.WithDescription("List UI5/Fiori BSP applications. Use query parameter for filtering with wildcards (*)."),
			mcp.WithString("query",
				mcp.Description("Search query (supports * wildcard, e.g., 'Z*' for custom apps)"),
			),
			mcp.WithNumber("max_results",
				mcp.Description("Maximum number of results (default: 100)"),
			),
		), s.handleUI5ListApps)
	}

	if shouldRegister("UI5GetApp") {
		s.mcpServer.AddTool(mcp.NewTool("UI5GetApp",
			mcp.WithDescription("Get details of a UI5/Fiori BSP application including file structure."),
			mcp.WithString("app_name",
				mcp.Required(),
				mcp.Description("Name of the UI5 application"),
			),
		), s.handleUI5GetApp)
	}

	if shouldRegister("UI5GetFileContent") {
		s.mcpServer.AddTool(mcp.NewTool("UI5GetFileContent",
			mcp.WithDescription("Get content of a specific file within a UI5/Fiori BSP application."),
			mcp.WithString("app_name",
				mcp.Required(),
				mcp.Description("Name of the UI5 application"),
			),
			mcp.WithString("file_path",
				mcp.Required(),
				mcp.Description("Path to the file within the app (e.g., '/webapp/manifest.json')"),
			),
		), s.handleUI5GetFileContent)
	}

	if shouldRegister("UI5UploadFile") {
		s.mcpServer.AddTool(mcp.NewTool("UI5UploadFile",
			mcp.WithDescription("Upload a file to a UI5/Fiori BSP application."),
			mcp.WithString("app_name",
				mcp.Required(),
				mcp.Description("Name of the UI5 application"),
			),
			mcp.WithString("file_path",
				mcp.Required(),
				mcp.Description("Path for the file within the app (e.g., '/webapp/Component.js')"),
			),
			mcp.WithString("content",
				mcp.Required(),
				mcp.Description("File content to upload"),
			),
			mcp.WithString("content_type",
				mcp.Description("Content type (e.g., 'application/javascript', 'application/json')"),
			),
		), s.handleUI5UploadFile)
	}

	if shouldRegister("UI5DeleteFile") {
		s.mcpServer.AddTool(mcp.NewTool("UI5DeleteFile",
			mcp.WithDescription("Delete a file from a UI5/Fiori BSP application."),
			mcp.WithString("app_name",
				mcp.Required(),
				mcp.Description("Name of the UI5 application"),
			),
			mcp.WithString("file_path",
				mcp.Required(),
				mcp.Description("Path to the file to delete (e.g., '/webapp/test.js')"),
			),
		), s.handleUI5DeleteFile)
	}

	if shouldRegister("UI5CreateApp") {
		s.mcpServer.AddTool(mcp.NewTool("UI5CreateApp",
			mcp.WithDescription("Create a new UI5/Fiori BSP application."),
			mcp.WithString("app_name",
				mcp.Required(),
				mcp.Description("Name for the new UI5 application"),
			),
			mcp.WithString("description",
				mcp.Description("Description of the application"),
			),
			mcp.WithString("package",
				mcp.Required(),
				mcp.Description("Package name (e.g., '$TMP' for local, 'ZFIORI' for transportable)"),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request number (optional for local packages)"),
			),
		), s.handleUI5CreateApp)
	}

	if shouldRegister("UI5DeleteApp") {
		s.mcpServer.AddTool(mcp.NewTool("UI5DeleteApp",
			mcp.WithDescription("Delete a UI5/Fiori BSP application."),
			mcp.WithString("app_name",
				mcp.Required(),
				mcp.Description("Name of the UI5 application to delete"),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request number (optional for local packages)"),
			),
		), s.handleUI5DeleteApp)
	}
}
