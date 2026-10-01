// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_search.go contains handlers for object search operations.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// routeSearchAction routes "search" action.
func (s *Server) routeSearchAction(ctx context.Context, action, objectType, objectName string, params map[string]any) (*mcp.CallToolResult, bool, error) {
	if action != "search" {
		return nil, false, nil
	}
	// Target is the query string; could be "TYPE NAME" or just a query
	query := objectType
	if objectName != "" {
		query = objectType + " " + objectName
	}
	if query == "" {
		query = getStringParam(params, "query")
	}
	if query == "" {
		return nil, false, nil
	}
	args := map[string]any{"query": query}
	if v, ok := getFloatParam(params, "maxResults"); ok {
		args["maxResults"] = v
	}
	if v, ok := getFloatParam(params, "max_results"); ok {
		args["maxResults"] = v
	}
	if v, ok := getFloatParam(params, "max"); ok {
		args["maxResults"] = v
	}
	// Pass object type for server-side filtering so max applies after the
	// type filter (mirrors the CLI --type path).
	if v := getStringParam(params, "type"); v != "" {
		args["objectType"] = v
	}
	if v := getStringParam(params, "objectType"); v != "" {
		args["objectType"] = v
	}
	if v, ok := getBoolParam(params, "exact"); ok {
		args["exact"] = v
	}
	return s.callHandler(ctx, s.handleSearchObject, args)
}

// --- Search Handlers ---

func (s *Server) handleSearchObject(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, ok := request.GetArguments()["query"].(string)
	if !ok || query == "" {
		return newToolResultError("query is required"), nil
	}

	maxResults := defaultRows
	if mr, ok := request.GetArguments()["maxResults"].(float64); ok && mr > 0 {
		maxResults = int(mr)
	}

	objectType, _ := request.GetArguments()["objectType"].(string)

	// Exact: only the objects whose name is the query, whatever their type.
	// Marked incomplete when the search window came back full.
	if exact, _ := request.GetArguments()["exact"].(bool); exact {
		results, incomplete, err := s.adtClient.SearchObjectExact(ctx, query, objectType, maxResults)
		if err != nil {
			return newToolResultError(fmt.Sprintf("Failed to search: %v", err)), nil
		}
		if incomplete == "" {
			output, _ := json.MarshalIndent(results, "", "  ")
			return mcp.NewToolResultText(string(output)), nil
		}
		// Like the truncated pattern search: the wrapper appears only when
		// there is something to say.
		output, _ := json.MarshalIndent(map[string]any{
			"results":    results,
			"incomplete": incomplete,
		}, "", "  ")
		return mcp.NewToolResultText(string(output)), nil
	}

	// One more than asked for, so a full page can be told from a page that
	// happens to be exactly the size of the limit. Without it a search that
	// returns forty of four thousand is indistinguishable from one that found
	// forty and stopped, and the caller has no reason to look further.
	results, err := s.adtClient.SearchObjectByType(ctx, query, objectType, maxResults+1)
	if err != nil {
		return newToolResultError(fmt.Sprintf("Failed to search: %v", err)), nil
	}

	more := len(results) > maxResults
	if more {
		results = results[:maxResults]
	}

	// The bare array is kept as the answer when nothing was cut, because it is
	// what every caller of this has parsed since it shipped. The wrapper
	// appears only when there is something to say.
	if !more {
		output, _ := json.MarshalIndent(results, "", "  ")
		return mcp.NewToolResultText(string(output)), nil
	}
	output, _ := json.MarshalIndent(map[string]any{
		"results": results,
		// The total is not known: the search is bounded server-side, so
		// counting the rest would cost another request against a query the
		// caller may well want to narrow instead.
		"truncated": truncationNoteUnknownTotal(maxResults, "max_results", "narrow the pattern"),
	}, "", "  ")
	return mcp.NewToolResultText(string(output)), nil
}
