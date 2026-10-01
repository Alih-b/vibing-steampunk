package scripting

import (
	"testing"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	lua "github.com/yuin/gopher-lua"
)

// A script walking a call graph sees what could not be searched, not only
// the children that were found.
func TestCallGraphToLuaCarriesUnsearched(t *testing.T) {
	L := lua.NewState()
	defer L.Close()

	tbl := callGraphToLua(L, &adt.CallGraphNode{
		Name:       "ZDEMO_FM",
		Children:   []adt.CallGraphNode{{Name: "ZDEMO_INCL", Type: "PROG/I"}},
		Unsearched: []adt.Unsearched{{Object: "ZDEMO_INCL", Reason: "status 500"}},
	})
	gaps, ok := L.GetField(tbl, "unsearched").(*lua.LTable)
	if !ok || gaps.Len() != 1 {
		t.Fatalf("unsearched = %v, want one entry", L.GetField(tbl, "unsearched"))
	}
	first := gaps.RawGetInt(1).(*lua.LTable)
	if L.GetField(first, "object").String() != "ZDEMO_INCL" || L.GetField(first, "reason").String() != "status 500" {
		t.Errorf("entry = object %v reason %v", L.GetField(first, "object"), L.GetField(first, "reason"))
	}

	plain := callGraphToLua(L, &adt.CallGraphNode{Name: "ZDEMO_FM"})
	if L.GetField(plain, "unsearched") != lua.LNil {
		t.Errorf("a graph with nothing unsearched should carry no unsearched field")
	}
}
