package mcp

import (
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// A transport of copies that exists but is incomplete is reported with what
// was done, and marked as an error: an agent must not read it as success.
func TestTransportOfCopiesResult_PartialIsAnError(t *testing.T) {
	res := &adt.TransportOfCopiesResult{
		Source: "TR-SRC", Transport: "TR-TOC", Target: "QAS",
		CopiedFrom: []string{"TR-TASK1"}, CopiedEntries: []string{"R3TR PROG ZDEMO_A"},
		Failed:        []adt.TransportOfCopiesFailure{{From: "TR-TASK2", Entries: []string{"R3TR PROG ZDEMO_B"}, Error: "sy-subrc 8"}},
		ReleaseStatus: "not attempted: the copy is incomplete",
	}
	out := transportOfCopiesResult(res, errors.New("transport of copies TR-TOC of TR-SRC is incomplete"))
	if !out.IsError {
		t.Fatal("an incomplete transport of copies was returned as success")
	}
	text := out.Content[0].(mcp.TextContent).Text
	for _, want := range []string{"TR-TOC", "TR-TASK1", "R3TR PROG ZDEMO_A", "TR-TASK2", "R3TR PROG ZDEMO_B", "not attempted"} {
		if !strings.Contains(text, want) {
			t.Errorf("result lacks %q:\n%s", want, text)
		}
	}

	if ok := transportOfCopiesResult(&adt.TransportOfCopiesResult{Transport: "TR-TOC"}, nil); ok.IsError {
		t.Error("a complete copy was marked as an error")
	}
	if refused := transportOfCopiesResult(nil, errors.New("refused")); !refused.IsError {
		t.Error("a refusal was not marked as an error")
	}
}
