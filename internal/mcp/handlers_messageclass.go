package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// createMessageClass creates a message class and, when "messages" are given,
// writes them in "language" (default: the session language):
// SAP(action="create", target="OBJECT", params={"object_type": "MSAG/N", "name": "ZDEMO",
//
//	"description": "Demo", "package_name": "$TMP", "messages": {"001": "Order & not found"}}).
func (s *Server) createMessageClass(ctx context.Context, opts adt.CreateObjectOptions, args map[string]any) (*mcp.CallToolResult, error) {
	lang := strings.ToUpper(getStringParam(args, "language"))
	opts.MasterLanguage = lang
	messages, err := messageTexts(args["messages"])
	if err != nil {
		return newToolResultError(err.Error()), nil
	}
	if err := s.adtClient.CreateObject(ctx, opts); err != nil {
		return newToolResultError(fmt.Sprintf("Failed to create message class: %v", err)), nil
	}
	result := map[string]any{
		"status":     "created",
		"object_url": adt.GetObjectURL(opts.ObjectType, opts.Name, ""),
	}
	if len(messages) > 0 {
		// The class was created in the session language; its messages go
		// in the same one unless a language was named.
		if lang == "" {
			lang = strings.ToUpper(s.adtClient.Language())
		}
		if lang == "" {
			lang = "EN"
		}
		if err := s.adtClient.WriteMessageClassTexts(ctx, opts.Name, lang, messages, "", opts.Transport); err != nil {
			return newToolResultError(fmt.Sprintf("message class %s created, but its messages were not written: %v", strings.ToUpper(opts.Name), err)), nil
		}
		result["messages"] = len(messages)
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	return mcp.NewToolResultText(string(out)), nil
}

// messageTexts reads messages as an object {"001": "text"} or as a list of
// {"number": "001", "text": "..."}.
func messageTexts(v any) ([]adt.MessageClassMessage, error) {
	var out []adt.MessageClassMessage
	switch m := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		for no, text := range m {
			out = append(out, adt.MessageClassMessage{Number: no, Text: fmt.Sprint(text)})
		}
	case []any:
		raw, _ := json.Marshal(m)
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("messages: %v", err)
		}
	default:
		return nil, fmt.Errorf(`messages must be {"001": "text"} or [{"number": "001", "text": "..."}]`)
	}
	for i, msg := range out {
		no := strings.TrimSpace(msg.Number)
		if len(no) > 3 || strings.Trim(no, "0123456789") != "" || no == "" {
			return nil, fmt.Errorf("message number %q: three digits", msg.Number)
		}
		out[i].Number = fmt.Sprintf("%03s", no)
		if len([]rune(msg.Text)) > 73 {
			return nil, fmt.Errorf("message %s is %d characters long; T100 holds 73", out[i].Number, len([]rune(msg.Text)))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}
