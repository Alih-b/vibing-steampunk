package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

func ddicPackage(args map[string]any) string {
	if p := getStringParam(args, "package"); p != "" {
		return p
	}
	return getStringParam(args, "package_name")
}

func ddicResult(res *adt.DDICObjectResult, err error) (*mcp.CallToolResult, error) {
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

// handleCreateDomain: SAP(action="create", target="DOMA ZDEMO", params={"description": "...",
// "data_type": "CHAR", "length": 2, "fixed_values": [{"low": "A", "text": "Alpha"}]}).
func (s *Server) handleCreateDomain(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	o := adt.DomainOptions{
		Name:           getStringParam(args, "name"),
		Description:    getStringParam(args, "description"),
		Package:        ddicPackage(args),
		Transport:      getStringParam(args, "transport"),
		DataType:       getStringParam(args, "data_type"),
		Length:         intParam(args, "length", 0),
		Decimals:       intParam(args, "decimals", 0),
		OutputLength:   intParam(args, "output_length", 0),
		ConversionExit: getStringParam(args, "conversion_exit"),
		ValueTable:     getStringParam(args, "value_table"),
		MasterLanguage: getStringParam(args, "language"),
	}
	o.Lowercase, _ = getBoolParam(args, "lowercase")
	o.Signed, _ = getBoolParam(args, "signed")
	if raw, ok := args["fixed_values"]; ok && raw != nil {
		b, _ := json.Marshal(raw)
		if err := json.Unmarshal(b, &o.FixedValues); err != nil {
			return newToolResultError(fmt.Sprintf(`fixed_values must be [{"low": "A", "high": "", "text": "..."}]: %v`, err)), nil
		}
	}
	return ddicResult(s.adtClient.CreateDomain(ctx, o))
}

// handleCreateDataElement: SAP(action="create", target="DTEL ZDEMO", params={"description": "...",
// "domain": "ZDEMO", "short_label": "Demo", "medium_label": "...", "long_label": "...", "heading": "..."}).
func (s *Server) handleCreateDataElement(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	o := adt.DataElementOptions{
		Name:           getStringParam(args, "name"),
		Description:    getStringParam(args, "description"),
		Package:        ddicPackage(args),
		Transport:      getStringParam(args, "transport"),
		Domain:         getStringParam(args, "domain"),
		DataType:       getStringParam(args, "data_type"),
		Length:         intParam(args, "length", 0),
		Decimals:       intParam(args, "decimals", 0),
		ShortLabel:     getStringParam(args, "short_label"),
		MediumLabel:    getStringParam(args, "medium_label"),
		LongLabel:      getStringParam(args, "long_label"),
		HeadingLabel:   getStringParam(args, "heading"),
		SearchHelp:     getStringParam(args, "search_help"),
		ParameterID:    getStringParam(args, "parameter_id"),
		MasterLanguage: getStringParam(args, "language"),
	}
	o.ChangeDocument, _ = getBoolParam(args, "change_document")
	return ddicResult(s.adtClient.CreateDataElement(ctx, o))
}
