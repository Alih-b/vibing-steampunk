package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	openrfc "github.com/oisee/open-rfc-go/rfc"

	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// withIDocNumber fills params["number"] from the target ("IDOC 28757955").
func withIDocNumber(params map[string]any, objectName string) map[string]any {
	out := make(map[string]any, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	if objectName != "" && getStringParam(params, "number") == "" {
		out["number"] = objectName
	}
	return out
}

// handleReadIDoc reads one IDoc over RFC, as WE02 shows it:
// SAP(action="read", target="IDOC 28757955", params={"segment": "E1EDKA1"}).
func (s *Server) handleReadIDoc(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	number := getStringParam(args, "number")
	if number == "" {
		return newToolResultError(`an IDoc number is required: SAP(action="read", target="IDOC 28757955")`), nil
	}
	opts := saprfc.IDocOptions{
		Segment:     getStringParam(args, "segment"),
		MaxSegments: intParam(args, "max_segments", 500),
	}
	opts.AllFields, _ = getBoolParam(args, "all_fields")
	opts.Raw, _ = getBoolParam(args, "raw")

	c, release, err := s.rfcClientFor(ctx, args)
	if err != nil {
		return newToolResultError("reading an IDoc needs the RFC connection: " + err.Error()), nil
	}
	defer release()
	doc, err := saprfc.ReadIDoc(ctx, c, number, opts)
	if err != nil {
		if errors.Is(err, openrfc.ErrTransport) || errors.Is(err, openrfc.ErrClosed) {
			s.dropSharedRFC(ctx)
		}
		return newToolResultError(fmt.Sprintf("reading IDoc %s: %v", number, err)), nil
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return mcp.NewToolResultText(string(out)), nil
}
