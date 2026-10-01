package mcp

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// handleCreateStructure creates a DDIC structure or append structure from its
// DDL source:
// SAP(action="create", target="STRUCT", params={"description": "...", "package": "$TMP",
//
//	"source": "define structure zdemo { field : abap.char(10); }"})
func (s *Server) handleCreateStructure(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	pkg := getStringParam(args, "package")
	if pkg == "" {
		pkg = getStringParam(args, "package_name")
	}
	res, err := s.adtClient.CreateStructure(ctx, adt.StructureOptions{
		Name:           getStringParam(args, "name"),
		Description:    getStringParam(args, "description"),
		Package:        pkg,
		Transport:      getStringParam(args, "transport"),
		Source:         getStringParam(args, "source"),
		MasterLanguage: getStringParam(args, "language"),
	})
	if err != nil {
		if res != nil {
			out, _ := json.MarshalIndent(res, "", "  ")
			return newToolResultError(err.Error() + "\n\n" + string(out)), nil
		}
		return newToolResultError(err.Error()), nil
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	return mcp.NewToolResultText(string(out)), nil
}
