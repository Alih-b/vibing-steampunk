// Package mcp provides the MCP server implementation for ABAP ADT tools.
// tools_analysis.go registers call-graph analysis and code intelligence tools.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// registerAnalysisTools registers code analysis infrastructure tools.
func (s *Server) registerAnalysisTools(shouldRegister func(string) bool) {
	// The four call-graph tools below name their source in their own
	// descriptions, and that is deliberate. They used to say "call graph" and
	// mean /sap/bc/adt/cai/callgraph, a resource that exists on no release we
	// have checked; an agent had no way to know the empty answer it got was a
	// missing resource rather than an unused object. The two sources that do
	// answer have different strengths and different blind spots, so the tool
	// says which one spoke.
	//
	// object_uri is no longer required: an agent that knows the object by name
	// should not have to build a URI to ask about it.
	if shouldRegister("GetCallGraph") {
		s.mcpServer.AddTool(mcp.NewTool("GetCallGraph",
			mcp.WithDescription("Who calls this ABAP object, and what it calls. 'callers' reads the where-used list behind SE84; "+
				"'callees' reads the CROSS and WBCROSSGT cross-reference tables, which record references made at activation "+
				"rather than observed calls. One hop in either direction."),
			mcp.WithString("object_uri",
				mcp.Description("ADT URI of the object (e.g., /sap/bc/adt/oo/classes/zcl_test). Or give object_type and object_name."),
			),
			mcp.WithString("object_type",
				mcp.Description("CLAS, INTF, PROG, FUGR or FUNC — with object_name, instead of object_uri"),
			),
			mcp.WithString("object_name",
				mcp.Description("Object name, e.g. ZCL_ORDER_PROCESSING"),
			),
			mcp.WithString("direction",
				mcp.Description("'callers' (who uses this), 'callees' (what this uses), or 'both'. Default: callers"),
			),
			mcp.WithNumber("max_results",
				mcp.Description("Maximum number of results (default: 200)"),
			),
		), s.handleGetCallGraph)
	}

	if shouldRegister("GetObjectStructure") {
		s.mcpServer.AddTool(mcp.NewTool("GetObjectStructure",
			mcp.WithDescription("Get object explorer tree structure. Returns hierarchical view of object components."),
			mcp.WithString("object_name",
				mcp.Required(),
				mcp.Description("Object name (e.g., ZCL_TEST, ZPROGRAM)"),
			),
			mcp.WithNumber("max_results",
				mcp.Description("Maximum number of results (default: 100)"),
			),
		), s.handleGetObjectStructure)
	}

	if shouldRegister("GetCallersOf") {
		s.mcpServer.AddTool(mcp.NewTool("GetCallersOf",
			mcp.WithDescription("Who references this ABAP object, from the where-used list behind SE84. Reports each caller's "+
				"package, type and the method the reference sits in. One hop: the list is not recursive. "+
				"An empty answer and a misspelt name look identical here, so the answer says so."),
			mcp.WithString("object_uri",
				mcp.Description("ADT URI of the object (e.g., /sap/bc/adt/oo/classes/zcl_test). Or give object_type and object_name."),
			),
			mcp.WithString("object_type",
				mcp.Description("CLAS, INTF, PROG, FUGR or FUNC — with object_name, instead of object_uri"),
			),
			mcp.WithString("object_name",
				mcp.Description("Object name, e.g. ZCL_ORDER_PROCESSING"),
			),
			mcp.WithNumber("max_results",
				mcp.Description("Maximum number of results (default: 200)"),
			),
		), s.handleGetCallersOf)
	}

	if shouldRegister("GetCalleesOf") {
		s.mcpServer.AddTool(mcp.NewTool("GetCalleesOf",
			mcp.WithDescription("What this ABAP object's code reaches, from the CROSS and WBCROSSGT cross-reference tables. "+
				"These are references recorded when the object was activated, not observed calls: a dynamic CALL METHOD (name) "+
				"is in no row. Rows marked calls:true are invocations; the rest are type and data references. Needs free SQL."),
			mcp.WithString("object_uri",
				mcp.Description("ADT URI of the object (e.g., /sap/bc/adt/oo/classes/zcl_test). Or give object_type and object_name."),
			),
			mcp.WithString("object_type",
				mcp.Description("CLAS, INTF, PROG, FUGR or FUNC — with object_name, instead of object_uri"),
			),
			mcp.WithString("object_name",
				mcp.Description("Object name, e.g. ZCL_ORDER_PROCESSING"),
			),
			mcp.WithNumber("max_results",
				mcp.Description("Maximum number of results (default: 200)"),
			),
		), s.handleGetCalleesOf)
	}

	if shouldRegister("AnalyzeCallGraph") {
		s.mcpServer.AddTool(mcp.NewTool("AnalyzeCallGraph",
			mcp.WithDescription("Counts over one object's references: how many, of what kind, in which direction. "+
				"Same two sources as GetCallGraph, so the same caveats apply — and the depth is always 1, because neither source is recursive."),
			mcp.WithString("object_uri",
				mcp.Description("ADT URI of the object to analyze. Or give object_type and object_name."),
			),
			mcp.WithString("object_type",
				mcp.Description("CLAS, INTF, PROG, FUGR or FUNC — with object_name, instead of object_uri"),
			),
			mcp.WithString("object_name",
				mcp.Description("Object name, e.g. ZCL_ORDER_PROCESSING"),
			),
			mcp.WithString("direction",
				mcp.Description("Direction: 'callers' or 'callees' (default: callees)"),
			),
		), s.handleAnalyzeCallGraph)
	}

	if shouldRegister("CompareCallGraphs") {
		s.mcpServer.AddTool(mcp.NewTool("CompareCallGraphs",
			mcp.WithDescription("Compare an object's recorded references with an actual execution trace: what ran, what did not, "+
				"and what ran without being recorded. The static side comes from the cross-reference tables, so a 'static only' "+
				"edge may be a type the code names and never calls rather than an untested path."),
			mcp.WithString("object_uri",
				mcp.Required(),
				mcp.Description("ADT URI of the root object"),
			),
			mcp.WithString("trace_data",
				mcp.Required(),
				mcp.Description("JSON array of trace edges from execution (format: [{caller_name, callee_name}, ...])"),
			),
		), s.handleCompareCallGraphs)
	}

	if shouldRegister("TraceExecution") {
		s.mcpServer.AddTool(mcp.NewTool("TraceExecution",
			mcp.WithDescription("COMPOSITE RCA TOOL: Performs traced execution analysis. 1) Builds static call graph from object, 2) Optionally runs unit tests, 3) Collects trace data, 4) Extracts actual call edges, 5) Compares static vs actual for root cause analysis."),
			mcp.WithString("object_uri",
				mcp.Required(),
				mcp.Description("ADT URI of the starting object for static call graph"),
			),
			mcp.WithNumber("max_depth",
				mcp.Description("Maximum depth for call graph traversal (default: 5)"),
			),
			mcp.WithBoolean("run_tests",
				mcp.Description("Run unit tests before collecting trace (default: false)"),
			),
			mcp.WithString("test_object_uri",
				mcp.Description("Object URI for tests to run (defaults to object_uri)"),
			),
			mcp.WithString("trace_user",
				mcp.Description("Filter traces by user (defaults to current user)"),
			),
		), s.handleTraceExecution)
	}

	// --- Graph Engine: Dependency & Boundary Analysis ---

	if shouldRegister("CheckBoundaries") {
		s.mcpServer.AddTool(mcp.NewTool("CheckBoundaries",
			mcp.WithDescription("Analyze package boundary violations. Checks if a package/object's dependencies cross package boundaries. Detects: same-package deps, SAP standard deps, whitelisted Z-package deps, violations (cross-package Z* deps), and dynamic calls. Uses embedded ABAP parser (offline) + TADIR resolution (online)."),
			mcp.WithString("package",
				mcp.Description("Package to analyze (e.g., $ZDEV). All objects in package are checked."),
			),
			mcp.WithString("object",
				mcp.Description("Single object to analyze (e.g., ZCL_TEST). Source is read from SAP."),
			),
			mcp.WithString("source",
				mcp.Description("ABAP source code to analyze offline (no SAP connection needed)."),
			),
			mcp.WithString("whitelist",
				mcp.Description("Comma-separated list of allowed Z-packages (supports glob: '$ZCOMMON,$ZUTIL*'). Dependencies on these packages are OK."),
			),
			mcp.WithNumber("depth",
				mcp.Description("Analysis depth (default: 1). Higher values follow transitive deps."),
			),
			mcp.WithString("format",
				mcp.Description("Output format: 'text' (default), 'json', 'full' (includes standard deps)"),
			),
		), s.handleCheckBoundaries)
	}

	if shouldRegister("GraphStats") {
		s.mcpServer.AddTool(mcp.NewTool("GraphStats",
			mcp.WithDescription("Extract dependency statistics from ABAP source code using embedded parser. Returns node/edge counts by type, edge kind, and source. Works offline without SAP."),
			mcp.WithString("source",
				mcp.Required(),
				mcp.Description("ABAP source code to analyze"),
			),
		), s.handleGraphStats)
	}
}

// registerCodeIntelTools registers code intelligence tools.
func (s *Server) registerCodeIntelTools(shouldRegister func(string) bool) {
	if shouldRegister("FindDefinition") {
		s.mcpServer.AddTool(mcp.NewTool("FindDefinition",
			mcp.WithDescription("Navigate to the definition of a symbol at a given position in source code"),
			mcp.WithString("source_url",
				mcp.Required(),
				mcp.Description("ADT URL of the source file (e.g., /sap/bc/adt/programs/programs/ZTEST/source/main)"),
			),
			mcp.WithString("source",
				mcp.Required(),
				mcp.Description("Full source code of the file"),
			),
			mcp.WithNumber("line",
				mcp.Required(),
				mcp.Description("Line number (1-based)"),
			),
			mcp.WithNumber("start_column",
				mcp.Required(),
				mcp.Description("Start column of the symbol (1-based)"),
			),
			mcp.WithNumber("end_column",
				mcp.Required(),
				mcp.Description("End column of the symbol (1-based)"),
			),
			mcp.WithBoolean("implementation",
				mcp.Description("Navigate to implementation instead of definition (default: false)"),
			),
			mcp.WithString("main_program",
				mcp.Description("Main program for includes (optional)"),
			),
		), s.handleFindDefinition)
	}

	if shouldRegister("FindReferences") {
		s.mcpServer.AddTool(mcp.NewTool("FindReferences",
			mcp.WithDescription("Find all references to an ABAP object or symbol"),
			mcp.WithString("object_url",
				mcp.Required(),
				mcp.Description("ADT URL of the object (e.g., /sap/bc/adt/oo/classes/ZCL_TEST)"),
			),
			mcp.WithNumber("line",
				mcp.Description("Line number for position-based reference search (1-based, optional)"),
			),
			mcp.WithNumber("column",
				mcp.Description("Column number for position-based reference search (1-based, optional)"),
			),
		), s.handleFindReferences)
	}

	if shouldRegister("GetContext") {
		s.mcpServer.AddTool(mcp.NewTool("GetContext",
			mcp.WithDescription("Analyze ABAP source dependencies and return compressed public API contracts (prologue). Produces a compact summary of all referenced classes, interfaces, and function modules — showing only their public signatures. Use this to understand the surrounding codebase context before editing."),
			mcp.WithString("object_type",
				mcp.Required(),
				mcp.Description("ABAP object type: PROG, CLAS, INTF, FUNC, FUGR"),
			),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("Object name (e.g., ZCL_ORDER_PROCESSOR)"),
			),
			mcp.WithString("source",
				mcp.Description("Source code to analyze (if omitted, fetched from SAP)"),
			),
			mcp.WithNumber("max_deps",
				mcp.Description("Maximum dependencies to resolve (default: 20)"),
			),
		), s.handleGetContext)
	}

	if shouldRegister("CodeCompletion") {
		s.mcpServer.AddTool(mcp.NewTool("CodeCompletion",
			mcp.WithDescription("Get code completion suggestions at a position in source code"),
			mcp.WithString("source_url",
				mcp.Required(),
				mcp.Description("ADT URL of the source file (e.g., /sap/bc/adt/programs/programs/ZTEST/source/main)"),
			),
			mcp.WithString("source",
				mcp.Required(),
				mcp.Description("Full source code of the file"),
			),
			mcp.WithNumber("line",
				mcp.Required(),
				mcp.Description("Line number (1-based)"),
			),
			mcp.WithNumber("column",
				mcp.Required(),
				mcp.Description("Column number (1-based)"),
			),
		), s.handleCodeCompletion)
	}

	if shouldRegister("GetTypeHierarchy") {
		s.mcpServer.AddTool(mcp.NewTool("GetTypeHierarchy",
			mcp.WithDescription("Get the type hierarchy (supertypes or subtypes) for a class/interface"),
			mcp.WithString("source_url",
				mcp.Required(),
				mcp.Description("ADT URL of the source file"),
			),
			mcp.WithString("source",
				mcp.Required(),
				mcp.Description("Full source code of the file"),
			),
			mcp.WithNumber("line",
				mcp.Required(),
				mcp.Description("Line number (1-based)"),
			),
			mcp.WithNumber("column",
				mcp.Required(),
				mcp.Description("Column number (1-based)"),
			),
			mcp.WithBoolean("super_types",
				mcp.Description("Get supertypes instead of subtypes (default: false = subtypes)"),
			),
		), s.handleGetTypeHierarchy)
	}

	if shouldRegister("GetClassComponents") {
		s.mcpServer.AddTool(mcp.NewTool("GetClassComponents",
			mcp.WithDescription("Get the structure of a class - lists all methods, attributes, events, and other components with their visibility and properties"),
			mcp.WithString("class_url",
				mcp.Required(),
				mcp.Description("ADT URL of the class (e.g., /sap/bc/adt/oo/classes/ZCL_TEST)"),
			),
		), s.handleGetClassComponents)
	}
}
