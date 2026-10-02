// Package mcp provides the MCP server implementation for ABAP ADT tools.
// tools_debug.go registers ABAP debugger and AMDP debugger tools.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// registerDebuggerTools registers ABAP debugger session and breakpoint tools.
func (s *Server) registerDebuggerTools(shouldRegister func(string) bool) {
	if shouldRegister("SetBreakpoint") {
		s.mcpServer.AddTool(mcp.NewTool("SetBreakpoint",
			mcp.WithDescription("Set an external breakpoint through SAP's ADT resources. Kinds: 'line' (an object and a line), 'statement' (an ABAP keyword), 'exception' (an exception class). The line is the line in the source as vsp shows it. Needs no ZADT_VSP and no other ABAP on the system."),
			mcp.WithString("kind",
				mcp.Description("Breakpoint type: 'line' (default), 'statement', or 'exception'"),
			),
			mcp.WithString("program",
				mcp.Description("Object the breakpoint sits in — a program, class or function module name, or an ADT source URI"),
			),
			mcp.WithNumber("line",
				mcp.Description("Line number, counted in the object's own source as vsp reads it"),
			),
			mcp.WithString("condition",
				mcp.Description("Optional ABAP condition; the breakpoint only stops when it is true (e.g., 'lv_count > 10')"),
			),
			mcp.WithString("statement",
				mcp.Description("ABAP statement for statement breakpoints (e.g., 'CALL FUNCTION', 'SELECT', 'LOOP', 'CALL METHOD')"),
			),
			mcp.WithString("exception",
				mcp.Description("Exception class for exception breakpoints (e.g., 'CX_SY_ZERODIVIDE', 'CX_SY_OPEN_SQL_DB')"),
			),
		), s.handleSetBreakpoint)
	}

	if shouldRegister("GetBreakpoints") {
		s.mcpServer.AddTool(mcp.NewTool("GetBreakpoints",
			mcp.WithDescription("List the breakpoints this session registered. ADT does not report breakpoints belonging to other clients, so this is not a system-wide list."),
		), s.handleGetBreakpoints)
	}

	if shouldRegister("DeleteBreakpoint") {
		s.mcpServer.AddTool(mcp.NewTool("DeleteBreakpoint",
			mcp.WithDescription("Delete a breakpoint by ID, or pass 'all' to remove every breakpoint this session registered."),
			mcp.WithString("breakpoint_id",
				mcp.Required(),
				mcp.Description("ID of the breakpoint to delete, from SetBreakpoint or GetBreakpoints — or 'all'"),
			),
		), s.handleDeleteBreakpoint)
	}

	if shouldRegister("CallRFC") {
		s.mcpServer.AddTool(mcp.NewTool("CallRFC",
			mcp.WithDescription("Call a function module via WebSocket (ZADT_VSP). Useful for triggering ABAP code execution to hit breakpoints. Parameters are passed as key-value pairs."),
			mcp.WithString("function",
				mcp.Required(),
				mcp.Description("Function module name (e.g., 'RFC_PING', 'BAPI_USER_GET_DETAIL')"),
			),
			mcp.WithString("params",
				mcp.Description("JSON object with function parameters (e.g., '{\"IV_PARAM\":\"value\"}')"),
			),
		), s.handleCallRFC)
	}

	if shouldRegister("MoveObject") {
		s.mcpServer.AddTool(mcp.NewTool("MoveObject",
			mcp.WithDescription("Move an ABAP object to a different package. Uses ZADT_VSP WebSocket to call TR_TADIR_INTERFACE. Requires ZADT_VSP deployed."),
			mcp.WithString("object_type",
				mcp.Required(),
				mcp.Description("Object type: CLAS, PROG, INTF, FUGR, TABL, etc."),
			),
			mcp.WithString("object_name",
				mcp.Required(),
				mcp.Description("Name of the object to move (e.g., 'ZCL_TEST')"),
			),
			mcp.WithString("new_package",
				mcp.Required(),
				mcp.Description("Target package (e.g., '$ZRAY', 'ZPACKAGE')"),
			),
		), s.handleMoveObject)
	}

	if shouldRegister("DebuggerListen") {
		s.mcpServer.AddTool(mcp.NewTool("DebuggerListen",
			mcp.WithDescription("Wait for a debuggee to hit a breakpoint, attach to it, and report where it stopped. BLOCKING: it returns when something stops or the timeout expires. Listen and attach are one call because a debuggee is only attachable while it waits. Breakpoints fire only for code running in ANOTHER session, so trigger the code from elsewhere while this waits."),
			mcp.WithString("user",
				mcp.Description("User to listen for (defaults to current user)"),
			),
			mcp.WithNumber("timeout",
				mcp.Description("Timeout in seconds (default: 60, max: 240)"),
			),
		), s.handleDebuggerListen)
	}

	if shouldRegister("DebuggerAttach") {
		s.mcpServer.AddTool(mcp.NewTool("DebuggerAttach",
			mcp.WithDescription("Attach to a debuggee that has hit a breakpoint. Use the debuggee_id from DebuggerListen result."),
			mcp.WithString("debuggee_id",
				mcp.Required(),
				mcp.Description("ID of the debuggee (from DebuggerListen result)"),
			),
			mcp.WithString("user",
				mcp.Description("User for debugging (defaults to current user)"),
			),
		), s.handleDebuggerAttach)
	}

	if shouldRegister("DebuggerDetach") {
		s.mcpServer.AddTool(mcp.NewTool("DebuggerDetach",
			mcp.WithDescription("Detach from the current debug session and release the debuggee."),
		), s.handleDebuggerDetach)
	}

	if shouldRegister("DebuggerStep") {
		s.mcpServer.AddTool(mcp.NewTool("DebuggerStep",
			mcp.WithDescription("Perform a step operation in the debugger."),
			mcp.WithString("step_type",
				mcp.Required(),
				mcp.Description("Step type: 'stepInto', 'stepOver', 'stepReturn', 'stepContinue', 'stepRunToLine', 'stepJumpToLine'"),
			),
			mcp.WithString("uri",
				mcp.Description("Target URI for stepRunToLine/stepJumpToLine (e.g., '/sap/bc/adt/programs/programs/ZTEST/source/main#start=42')"),
			),
		), s.handleDebuggerStep)
	}

	if shouldRegister("DebuggerGetStack") {
		s.mcpServer.AddTool(mcp.NewTool("DebuggerGetStack",
			mcp.WithDescription("Get the current call stack during a debug session."),
		), s.handleDebuggerGetStack)
	}

	if shouldRegister("DebuggerGetVariables") {
		s.mcpServer.AddTool(mcp.NewTool("DebuggerGetVariables",
			mcp.WithDescription("Read variables at the current stop. With no argument it returns the stopped frame's own variables with their values; pass a composite id (from a previous answer) to expand a structure or table, or names to read specific ones."),
			mcp.WithArray("variable_ids",
				mcp.Description("Leave empty for the locals; or a single composite id to expand; or variable names to read"),
				mcp.Items(map[string]interface{}{"type": "string"}),
			),
		), s.handleDebuggerGetVariables)
	}
}

// registerAMDPTools registers AMDP/HANA debugger tools.
func (s *Server) registerAMDPTools(shouldRegister func(string) bool) {
	if shouldRegister("AMDPDebuggerStart") {
		s.mcpServer.AddTool(mcp.NewTool("AMDPDebuggerStart",
			mcp.WithDescription("Start an AMDP (HANA SQLScript) debug session with persistent goroutine. Creates a background goroutine that maintains the HTTP session cookies. Use AMDPDebuggerStep/AMDPGetVariables to interact, AMDPDebuggerStop to terminate."),
			mcp.WithString("user",
				mcp.Description("User to debug (defaults to current user)"),
			),
		), s.handleAMDPDebuggerStart)
	}

	if shouldRegister("AMDPDebuggerResume") {
		s.mcpServer.AddTool(mcp.NewTool("AMDPDebuggerResume",
			mcp.WithDescription("Get current AMDP debug session status. In goroutine model, this returns the current state without blocking. The session manager goroutine handles events internally."),
		), s.handleAMDPDebuggerResume)
	}

	if shouldRegister("AMDPDebuggerStop") {
		s.mcpServer.AddTool(mcp.NewTool("AMDPDebuggerStop",
			mcp.WithDescription("Stop the AMDP debug session and terminate the background goroutine. Cleans up the HTTP session on SAP server."),
		), s.handleAMDPDebuggerStop)
	}

	if shouldRegister("AMDPDebuggerStep") {
		s.mcpServer.AddTool(mcp.NewTool("AMDPDebuggerStep",
			mcp.WithDescription("Perform a step operation in the AMDP debugger. Communicates via channel to the session manager goroutine."),
			mcp.WithString("step_type",
				mcp.Required(),
				mcp.Description("Step type: 'stepInto', 'stepOver', 'stepReturn', 'stepContinue'"),
			),
		), s.handleAMDPDebuggerStep)
	}

	if shouldRegister("AMDPGetVariables") {
		s.mcpServer.AddTool(mcp.NewTool("AMDPGetVariables",
			mcp.WithDescription("Get variable values during AMDP debugging. Communicates via channel to the session manager goroutine. Returns scalar, table, and array types."),
		), s.handleAMDPGetVariables)
	}

	if shouldRegister("AMDPSetBreakpoint") {
		s.mcpServer.AddTool(mcp.NewTool("AMDPSetBreakpoint",
			mcp.WithDescription("Set a breakpoint in AMDP (SQLScript) code. Requires an active AMDP debug session. Specify the procedure name and line number."),
			mcp.WithString("proc_name",
				mcp.Required(),
				mcp.Description("AMDP procedure name (e.g., 'ZCL_TEST=>METHOD_NAME')"),
			),
			mcp.WithNumber("line",
				mcp.Required(),
				mcp.Description("Line number in the SQLScript code"),
			),
		), s.handleAMDPSetBreakpoint)
	}

	if shouldRegister("AMDPGetBreakpoints") {
		s.mcpServer.AddTool(mcp.NewTool("AMDPGetBreakpoints",
			mcp.WithDescription("Get all breakpoints registered in the current AMDP debug session. Useful for verifying breakpoints are set correctly."),
		), s.handleAMDPGetBreakpoints)
	}
}
