// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_help.go provides help documentation for the universal SAP tool.
package mcp

import (
	"embed"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// helpFiles holds one text file per help topic. Each file is the topic's text
// plus one trailing newline, which helpText strips.
//
//go:embed help/*.txt
var helpFiles embed.FS

// helpTopicFiles maps every topic name, aliases included, to its file in help/.
// Any other topic gets the overview.
var helpTopicFiles = map[string]string{
	"read":           "read",
	"edit":           "edit",
	"create":         "create",
	"delete":         "delete",
	"search":         "search",
	"query":          "query",
	"test":           "test",
	"info":           "info",
	"rfc":            "rfc",
	"i18n":           "i18n",
	"revisions":      "revisions",
	"history":        "revisions",
	"lint":           "lint",
	"grep":           "grep",
	"debug":          "debug",
	"analyze":        "analyze",
	"system":         "system",
	"tips":           "tips",
	"best_practices": "tips",
	"workflows":      "tips",
	"best":           "tips",
}

// helpText returns the text of help/<name>.txt with its placeholders filled.
func helpText(name string) string {
	raw, err := helpFiles.ReadFile("help/" + name + ".txt")
	if err != nil {
		panic("help topic file missing: " + err.Error())
	}
	text := strings.TrimSuffix(string(raw), "\n")
	return strings.ReplaceAll(text, "{{deleteNameTypesLine}}", deleteNameTypesLine)
}

// handleHelp returns help documentation for the universal SAP tool.
func handleHelp(topic string) *mcp.CallToolResult {
	topic = strings.ToLower(strings.TrimSpace(topic))
	name, ok := helpTopicFiles[topic]
	if !ok {
		name = "overview"
	}
	return mcp.NewToolResultText(helpText(name))
}
