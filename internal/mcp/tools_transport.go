// Package mcp provides the MCP server implementation for ABAP ADT tools.
// tools_transport.go registers CTS transport and gCTS tools.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// registerTransportTools registers CTS/Transport management tools.
func (s *Server) registerTransportTools(shouldRegister func(string) bool) {
	if shouldRegister("ListTransports") {
		s.mcpServer.AddTool(mcp.NewTool("ListTransports", append([]mcp.ToolOption{
			mcp.WithDescription("List transport requests of a user as flat rows: workbench and customizing, modifiable and released by default (request_status D limits it to modifiable). Requires --enable-transports OR --allow-transportable-edits. Reads GET /sap/bc/adt/cts/transportrequests with explicit requestType/requestStatus, falls back to the saved search configuration and then to E070/E07T; the answer names the source used."),
			mcp.WithString("user",
				mcp.Description("Username to list transports for (default: the connection user, '*' for all users — source sql only)"),
			),
		}, transportListingParams...)...,
		), s.handleListTransports)
	}

	if shouldRegister("GetTransport") {
		s.mcpServer.AddTool(mcp.NewTool("GetTransport",
			mcp.WithDescription("Get detailed transport information including objects and tasks. Requires --enable-transports OR --allow-transportable-edits flag."),
			mcp.WithString("transport",
				mcp.Required(),
				mcp.Description("Transport request number (e.g., 'A4HK900094')"),
			),
		), s.handleGetTransport)
	}

	if shouldRegister("CreateTransport") {
		s.mcpServer.AddTool(mcp.NewTool("CreateTransport",
			mcp.WithDescription("Create a new transport request. Requires --enable-transports flag and not --transport-read-only."),
			mcp.WithString("description",
				mcp.Required(),
				mcp.Description("Transport description"),
			),
			mcp.WithString("package",
				mcp.Required(),
				mcp.Description("Target package (DEVCLASS)"),
			),
			mcp.WithString("transport_layer",
				mcp.Description("Transport layer (optional)"),
			),
			mcp.WithString("type",
				mcp.Description("Type: 'workbench' (default) or 'customizing'"),
			),
			mcp.WithString("cts_project",
				mcp.Description("CTS project to file the request under (optional; defaults to --cts-project)"),
			),
			mcp.WithString("target",
				mcp.Description("Transport target, e.g. a target group like /GROUP/ (optional; defaults to --transport-target)"),
			),
		), s.handleCreateTransport)
	}

	if shouldRegister("ReleaseTransport") {
		s.mcpServer.AddTool(mcp.NewTool("ReleaseTransport",
			mcp.WithDescription("Release a transport request. This action is IRREVERSIBLE. Requires --enable-transports flag and not --transport-read-only."),
			mcp.WithString("transport",
				mcp.Required(),
				mcp.Description("Transport request number"),
			),
			mcp.WithBoolean("ignore_locks",
				mcp.Description("Release even with locked objects (default: false)"),
			),
			mcp.WithBoolean("skip_atc",
				mcp.Description("Skip ATC quality checks (default: false)"),
			),
		), s.handleReleaseTransport)
	}

	if shouldRegister("DeleteTransport") {
		s.mcpServer.AddTool(mcp.NewTool("DeleteTransport",
			mcp.WithDescription("Delete a transport request. Only modifiable transports can be deleted. Requires --enable-transports flag and not --transport-read-only."),
			mcp.WithString("transport",
				mcp.Required(),
				mcp.Description("Transport request number"),
			),
		), s.handleDeleteTransport)
	}
}

// registerGCTSTools registers gCTS (git-enabled Change Transport System) tools.
func (s *Server) registerGCTSTools(shouldRegister func(string) bool) { //nolint:unused // Dead on main too: called from nowhere (see handlers_route_eleven.go). Moved, not new.
	if shouldRegister("GctsListRepositories") {
		s.mcpServer.AddTool(mcp.NewTool("GctsListRepositories",
			mcp.WithDescription("List all gCTS repositories on the SAP system. Returns repository ID, name, URL, branch, status and role."),
		), s.handleGctsListRepositories)
	}

	if shouldRegister("GctsGetRepository") {
		s.mcpServer.AddTool(mcp.NewTool("GctsGetRepository",
			mcp.WithDescription("Get details of a specific gCTS repository including configuration."),
			mcp.WithString("rid",
				mcp.Required(),
				mcp.Description("Repository ID"),
			),
		), s.handleGctsGetRepository)
	}

	if shouldRegister("GctsCreateRepository") {
		s.mcpServer.AddTool(mcp.NewTool("GctsCreateRepository",
			mcp.WithDescription("Create a new gCTS repository. Expert mode only."),
			mcp.WithString("rid",
				mcp.Required(),
				mcp.Description("Repository ID"),
			),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Repository name"),
			),
			mcp.WithString("url",
				mcp.Required(),
				mcp.Description("Git remote URL"),
			),
			mcp.WithString("branch",
				mcp.Description("Default branch (default: main)"),
			),
			mcp.WithString("package",
				mcp.Description("ABAP package (VSID)"),
			),
			mcp.WithString("role",
				mcp.Description("Repository role (SOURCE or TARGET)"),
			),
		), s.handleGctsCreateRepository)
	}

	if shouldRegister("GctsDeleteRepository") {
		s.mcpServer.AddTool(mcp.NewTool("GctsDeleteRepository",
			mcp.WithDescription("Delete a gCTS repository. Expert mode only."),
			mcp.WithString("rid",
				mcp.Required(),
				mcp.Description("Repository ID"),
			),
		), s.handleGctsDeleteRepository)
	}

	if shouldRegister("GctsCloneRepository") {
		s.mcpServer.AddTool(mcp.NewTool("GctsCloneRepository",
			mcp.WithDescription("Clone a gCTS repository on the SAP system. Expert mode only."),
			mcp.WithString("rid",
				mcp.Required(),
				mcp.Description("Repository ID"),
			),
		), s.handleGctsCloneRepository)
	}

	if shouldRegister("GctsPull") {
		s.mcpServer.AddTool(mcp.NewTool("GctsPull",
			mcp.WithDescription("Pull changes into a gCTS repository, optionally to a specific commit. Expert mode only."),
			mcp.WithString("rid",
				mcp.Required(),
				mcp.Description("Repository ID"),
			),
			mcp.WithString("commit_id",
				mcp.Description("Commit ID to pull to (optional)"),
			),
		), s.handleGctsPull)
	}

	if shouldRegister("GctsCommit") {
		s.mcpServer.AddTool(mcp.NewTool("GctsCommit",
			mcp.WithDescription("Create a commit in a gCTS repository. Expert mode only."),
			mcp.WithString("rid",
				mcp.Required(),
				mcp.Description("Repository ID"),
			),
			mcp.WithString("message",
				mcp.Required(),
				mcp.Description("Commit message"),
			),
		), s.handleGctsCommit)
	}

	if shouldRegister("GctsListBranches") {
		s.mcpServer.AddTool(mcp.NewTool("GctsListBranches",
			mcp.WithDescription("List branches in a gCTS repository."),
			mcp.WithString("rid",
				mcp.Required(),
				mcp.Description("Repository ID"),
			),
		), s.handleGctsListBranches)
	}

	if shouldRegister("GctsSwitchBranch") {
		s.mcpServer.AddTool(mcp.NewTool("GctsSwitchBranch",
			mcp.WithDescription("Switch the active branch of a gCTS repository. Expert mode only."),
			mcp.WithString("rid",
				mcp.Required(),
				mcp.Description("Repository ID"),
			),
			mcp.WithString("branch",
				mcp.Required(),
				mcp.Description("Branch name to switch to"),
			),
		), s.handleGctsSwitchBranch)
	}

	if shouldRegister("GctsGetHistory") {
		s.mcpServer.AddTool(mcp.NewTool("GctsGetHistory",
			mcp.WithDescription("Get commit history of a gCTS repository."),
			mcp.WithString("rid",
				mcp.Required(),
				mcp.Description("Repository ID"),
			),
		), s.handleGctsGetHistory)
	}
}

// transportListingParams are the parameters GetUserTransports and
// ListTransports share. They mirror the query parameters of
// GET /sap/bc/adt/cts/transportrequests; see pkg/adt/transport_query.go
// for the contract.
var transportListingParams = []mcp.ToolOption{
	mcp.WithString("request_type",
		mcp.Description("Letters of K (workbench), W (customizing), T (transport of copies); default KWT"),
	),
	mcp.WithString("request_status",
		mcp.Description("Letters of D (modifiable), R (released); default DR. Without it the organizer would return released requests only"),
	),
	mcp.WithString("released_from",
		mcp.Description("YYYYMMDD; with released_to bounds the released requests (default: last 14 days)"),
	),
	mcp.WithString("released_to",
		mcp.Description("YYYYMMDD; see released_from"),
	),
	mcp.WithBoolean("targets",
		mcp.Description("Group by transport target and CTS project (default true)"),
	),
	mcp.WithString("source",
		mcp.Description("Where to read from: auto (default: params, then config, then sql — first source with requests wins), params (organizer tree with explicit parameters), config (organizer tree through the saved search configuration, as Eclipse does; the configuration decides the filters and the user), sql (E070/E07T)"),
	),
	mcp.WithString("config_uri",
		mcp.Description("Search configuration to use with source config, e.g. /sap/bc/adt/cts/transportrequests/searchconfiguration/configurations/<id>; default: the one saved for the user, else the first one (noted in the answer)"),
	),
}
