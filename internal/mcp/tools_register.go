// Package mcp provides the MCP server implementation for ABAP ADT tools.
// tools_register.go holds the mode logic (shouldRegister) and the top-level
// registration order. The register*Tools functions it calls live in one file
// per domain: tools_read.go, tools_crud.go, tools_debug.go, tools_transport.go, ...
package mcp

import (
	"strings"
)

// registerTools registers ADT tools with the MCP server based on mode, disabled groups, and granular config.
// Mode "focused" registers essential tools.
// Mode "expert" registers all tools.
// DisabledGroups can disable specific tool groups using short codes:
//   - "5" or "U" = UI5/BSP tools (3 tools, read-only)
//   - "T" = Test tools: RunUnitTests, RunATCCheck (2 tools)
//   - "H" = HANA/AMDP debugger (7 tools)
//   - "D" = ABAP Debugger (6 session tools)
//   - "C" = CTS/Transport tools (5 tools)
//   - "G" = Git/abapGit tools (2 tools)
//   - "R" = Report tools (4 tools)
//   - "I" = Install tools (4 tools)
//   - "X" = EXPERIMENTAL: All debugger + RunReport (17 tools) - use to disable unreliable features
//
// toolsConfig from .vsp.json has highest priority:
//   - If tool is explicitly disabled (false), it will NOT be registered
//   - If tool is explicitly enabled (true), it WILL be registered (overrides focused mode)
//   - If tool is not in config, mode/disabledGroups rules apply
func (s *Server) registerTools(mode string, disabledGroups string, toolsConfig map[string]bool) {
	// Hyperfocused mode: the universal tool, and nothing else.
	if mode == "hyperfocused" {
		s.registerUniversalTool()
		return
	}

	groups := toolGroups()
	focusedTools := focusedToolSet()

	// Build set of disabled tools based on disabledGroups string
	disabledTools := make(map[string]bool)
	for _, code := range strings.ToUpper(disabledGroups) {
		if tools, ok := groups[string(code)]; ok {
			for _, tool := range tools {
				disabledTools[tool] = true
			}
		}
	}

	// Helper to check if tool should be registered
	shouldRegister := func(toolName string) bool {
		// Priority 1: Check granular tool config from .vsp.json (highest priority)
		if toolsConfig != nil {
			if enabled, exists := toolsConfig[toolName]; exists {
				return enabled // Explicit config overrides everything
			}
		}
		// Priority 2: Check if tool is disabled by group
		if disabledTools[toolName] {
			return false
		}
		// Priority 3: Check mode
		if mode == "expert" {
			return true // Expert mode: register all tools (except disabled)
		}
		return focusedTools[toolName] // Focused mode: only whitelisted tools (except disabled)
	}

	// The universal tool is registered in every mode, not only hyperfocused.
	//
	// It used to be hyperfocused-only, which meant an agent in focused or
	// expert could not reach a single one of the thirty-eight `analyze` types:
	// eight analysis handlers, every post-mortem type and every AMDP target are
	// routed through SAP() and registered as tools nowhere. Two of the three
	// modes advertised a capability surface that was missing a third of itself,
	// and nothing said so — the same disease as a tool whitelisted behind a
	// registration function nobody calls.
	//
	// It goes through shouldRegister like everything else, so a deployment that
	// wants it gone can still turn it off by name.
	if shouldRegister("SAP") {
		s.registerUniversalTool()
	}

	// Register all tools
	s.registerUnifiedTools(shouldRegister)
	s.registerReadTools(shouldRegister)
	s.registerSystemTools(shouldRegister)
	s.registerAnalysisTools(shouldRegister)
	s.registerDiagnosticsTools(shouldRegister)
	s.registerDebuggerTools(shouldRegister)
	s.registerSearchTools(shouldRegister)
	s.registerDevTools(shouldRegister)
	s.registerCRUDTools(shouldRegister)
	s.registerClassIncludeTools(shouldRegister)
	s.registerWorkflowTools(shouldRegister)
	s.registerFileTools(shouldRegister)
	s.registerEditTools(shouldRegister)
	s.registerGrepTools(shouldRegister)
	s.registerCodeIntelTools(shouldRegister)
	s.registerUI5Tools(shouldRegister)
	s.registerAMDPTools(shouldRegister)
	s.registerTransportTools(shouldRegister)
	s.registerGitTools(shouldRegister)
	s.registerReportTools(shouldRegister)
	s.registerInstallTools(shouldRegister)
	s.registerVersionHistoryTools(shouldRegister)
	s.registerTestingQualityTools(shouldRegister)
	s.registerI18NTools(shouldRegister)
	s.registerIAMTools(shouldRegister)

	// Register tool aliases for common operations
	s.registerToolAliases(shouldRegister)
}
