// Package mcp provides the MCP server implementation for ABAP ADT tools.
// tools_workflow.go registers write-and-activate workflow tools.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// registerWorkflowTools registers workflow tools (WriteProgram, WriteClass, etc.)
func (s *Server) registerWorkflowTools(shouldRegister func(string) bool) {
	if shouldRegister("WriteProgram") {
		s.mcpServer.AddTool(mcp.NewTool("WriteProgram",
			mcp.WithDescription("Update an existing program with syntax check and activation (Lock -> SyntaxCheck -> Update -> Unlock -> Activate)"),
			mcp.WithString("program_name",
				mcp.Required(),
				mcp.Description("Name of the ABAP program"),
			),
			mcp.WithString("source",
				mcp.Required(),
				mcp.Description("ABAP source code"),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request number (optional for local packages)"),
			),
		), s.handleWriteProgram)
	}

	if shouldRegister("WriteClass") {
		s.mcpServer.AddTool(mcp.NewTool("WriteClass",
			mcp.WithDescription("Update an existing class with syntax check and activation (Lock -> SyntaxCheck -> Update -> Unlock -> Activate)"),
			mcp.WithString("class_name",
				mcp.Required(),
				mcp.Description("Name of the ABAP class"),
			),
			mcp.WithString("source",
				mcp.Required(),
				mcp.Description("ABAP class source code (definition and implementation)"),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request number (optional for local packages)"),
			),
		), s.handleWriteClass)
	}

	if shouldRegister("CreateAndActivateProgram") {
		s.mcpServer.AddTool(mcp.NewTool("CreateAndActivateProgram",
			mcp.WithDescription("Create a new program with source code and activate it (Create -> Lock -> Update -> Unlock -> Activate)"),
			mcp.WithString("program_name",
				mcp.Required(),
				mcp.Description("Name of the ABAP program"),
			),
			mcp.WithString("description",
				mcp.Required(),
				mcp.Description("Program description"),
			),
			mcp.WithString("package_name",
				mcp.Required(),
				mcp.Description("Package name (e.g., $TMP for local)"),
			),
			mcp.WithString("source",
				mcp.Required(),
				mcp.Description("ABAP source code"),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request number (required for non-local packages)"),
			),
		), s.handleCreateAndActivateProgram)
	}

	if shouldRegister("CreateClassWithTests") {
		s.mcpServer.AddTool(mcp.NewTool("CreateClassWithTests",
			mcp.WithDescription("Create a new class with unit tests and run them (Create -> Lock -> Update -> CreateTestInclude -> UpdateTest -> Unlock -> Activate -> RunTests)"),
			mcp.WithString("class_name",
				mcp.Required(),
				mcp.Description("Name of the ABAP class"),
			),
			mcp.WithString("description",
				mcp.Required(),
				mcp.Description("Class description"),
			),
			mcp.WithString("package_name",
				mcp.Required(),
				mcp.Description("Package name (e.g., $TMP for local)"),
			),
			mcp.WithString("class_source",
				mcp.Required(),
				mcp.Description("ABAP class source code (definition and implementation)"),
			),
			mcp.WithString("test_source",
				mcp.Required(),
				mcp.Description("ABAP unit test source code"),
			),
			mcp.WithString("transport",
				mcp.Description("Transport request number (required for non-local packages)"),
			),
		), s.handleCreateClassWithTests)
	}
}
