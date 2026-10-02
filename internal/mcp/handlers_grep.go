// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_grep.go contains handlers for grep/search operations on ABAP objects.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// routeGrepAction routes "grep" action.
func (s *Server) routeGrepAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (*mcp.CallToolResult, bool, error) {
	if action != "grep" {
		return nil, false, nil
	}

	// GrepObjects (multiple objects)
	if hasAnyParam(params, "object_urls") {
		return s.callHandler(ctx, s.handleGrepObjects, params)
	}

	// GrepPackages (multiple packages)
	if hasAnyParam(params, "packages") {
		return s.callHandler(ctx, s.handleGrepPackages, params)
	}

	// GrepPackage (single package). `package` is the CLI's own flag name and
	// the one a caller reaches for; only `package_name` was accepted, so the
	// natural call fell off the end of the chain and was answered with "no
	// handler found for action=grep" — of an action that works.
	if pkgName := firstParam(params, "package_name", "package"); pkgName != "" {
		args := copyParams(params)
		args["package_name"] = pkgName
		return s.callHandler(ctx, s.handleGrepPackage, args)
	}

	// GrepObject (single object)
	if objectURL := firstParam(params, "object_url", "object"); objectURL != "" {
		args := copyParams(params)
		args["object_url"] = objectURL
		return s.callHandler(ctx, s.handleGrepObject, args)
	}

	// Object given by name rather than by URL:
	//   SAP(action="grep", params={"object_name": "ZCL_TEST", "pattern": "..."})
	//   SAP(action="grep", target="CLAS ZCL_TEST", params={"pattern": "..."})
	// The handler only knows URLs, so build one from type and name.
	name := getStringParam(params, "object_name")
	objType := strings.ToUpper(getStringParam(params, "object_type"))
	if name == "" {
		// target="CLAS ZCL_TEST", or target="ZCL_TEST"
		if objectName != "" {
			name = objectName
			if objType == "" {
				objType = objectType
			}
		} else {
			name = objectType
		}
	} else if objType == "" && objectName == "" && objectType != "" {
		// target carried the type only: target="CLAS", params={"object_name": ...}
		objType = objectType
	}
	if name != "" {
		if objType == "" {
			// A bare one-word target is ambiguous: target="$TMP" is a package
			// and target="ZREPORT" is a program, and guessing CLAS for either
			// builds /sap/bc/adt/oo/classes/$tmp, which answers "Failed to
			// read source" -- a wrong URL reported as a missing object. Say
			// what is missing instead. $TMP is the very example the help text
			// gives for package_name, so this is a likely call.
			return newToolResultError(fmt.Sprintf(
				"grep: %q alone does not say what kind of object it is. "+
					"Pass target=\"CLAS %s\" (or PROG, INTF, FUGR), or params={\"object_type\": \"CLAS\"}, "+
					"or grep a package with params={\"package_name\": %q}.",
				name, name, name)), true, nil
		}
		url := buildADTObjectURL(objType, name)
		if url == "" {
			return newToolResultError(fmt.Sprintf(
				"Cannot build an object URL for object_type=%q. Supported types: CLAS, PROG, INTF, FUGR. "+
					"Pass object_url instead, or grep a package with params={\"package_name\": \"...\"}.",
				objType)), true, nil
		}
		args := copyParams(params)
		delete(args, "object_name")
		delete(args, "object_type")
		args["object_url"] = url
		return s.callHandler(ctx, s.handleGrepObject, args)
	}

	return needParams("grep", params,
		[]string{"package_name (or package)", "object_url (or object)", "object_name with object_type, or target=\"CLAS <name>\"", "packages", "object_urls"},
		`SAP(action="grep", params={"pattern": "SELECT", "package": "$TMP"})
  SAP(action="grep", target="CLAS ZCL_DEMO", params={"pattern": "TODO"})
  SAP(action="grep", params={"pattern": "TODO", "object_url": "/sap/bc/adt/oo/classes/zcl_demo"})`), true, nil
}

// --- Grep/Search Handlers ---

func (s *Server) handleGrepObject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	objectURL, ok := request.GetArguments()["object_url"].(string)
	if !ok || objectURL == "" {
		return newToolResultError("object_url is required"), nil
	}

	pattern, ok := request.GetArguments()["pattern"].(string)
	if !ok || pattern == "" {
		return newToolResultError("pattern is required"), nil
	}

	caseInsensitive := false
	if ci, ok := request.GetArguments()["case_insensitive"].(bool); ok {
		caseInsensitive = ci
	}

	contextLines := 0
	if cl, ok := request.GetArguments()["context_lines"].(float64); ok {
		contextLines = int(cl)
	}

	result, err := s.adtClient.GrepObject(ctx, objectURL, pattern, caseInsensitive, contextLines)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GrepObject failed: %v", err)), nil
	}

	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}

func (s *Server) handleGrepPackage(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	packageName, ok := request.GetArguments()["package_name"].(string)
	if !ok || packageName == "" {
		return newToolResultError("package_name is required"), nil
	}

	pattern, ok := request.GetArguments()["pattern"].(string)
	if !ok || pattern == "" {
		return newToolResultError("pattern is required"), nil
	}

	caseInsensitive := false
	if ci, ok := request.GetArguments()["case_insensitive"].(bool); ok {
		caseInsensitive = ci
	}

	// Parse object_types (comma-separated string to slice)
	var objectTypes []string
	if ot, ok := request.GetArguments()["object_types"].(string); ok && ot != "" {
		objectTypes = strings.Split(ot, ",")
		// Trim whitespace from each type
		for i := range objectTypes {
			objectTypes[i] = strings.TrimSpace(objectTypes[i])
		}
	}

	maxResults := 100 // default
	if mr, ok := request.GetArguments()["max_results"].(float64); ok {
		maxResults = int(mr)
	}

	result, err := s.adtClient.GrepPackage(ctx, packageName, pattern, caseInsensitive, objectTypes, maxResults)
	if err != nil {
		return newToolResultError(fmt.Sprintf("GrepPackage failed: %v", err)), nil
	}

	output, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}
