package scripting

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/saprfc"
)

// readOnlyEngine is an engine whose debug session factory only counts how
// often a session was asked for; it never opens one.
func readOnlyEngine(t *testing.T, readOnly bool) (*LuaEngine, *int) {
	t.Helper()
	e := NewLuaEngine(adt.NewClient("http://127.0.0.1:1", "TESTUSER", "secret"))
	t.Cleanup(e.Close)
	opened := 0
	e.SetDebuggerFactory(func(context.Context) (*saprfc.Debugger, func(), error) {
		opened++
		return nil, nil, errors.New("no session in this test")
	})
	e.SetReadOnly(readOnly)
	return e, &opened
}

// Lua's setVariable is the session binding (saprfc.Debugger.SetVariable), not
// the ADT client's; on a read-only system it is refused before the session is
// even opened.
func TestLuaSetVariable_RefusedOnAReadOnlySystem(t *testing.T) {
	e, opened := readOnlyEngine(t, true)
	if err := e.Execute(`ok, err = setVariable("LV_COUNT", "42")`); err != nil {
		t.Fatal(err)
	}
	if msg := e.L.GetGlobal("err").String(); !strings.Contains(msg, "blocked by safety configuration") {
		t.Fatalf("want a safety refusal, got %q", msg)
	}
	if *opened != 0 {
		t.Errorf("a refused setVariable opened a debug session %d time(s)", *opened)
	}

	w, wOpened := readOnlyEngine(t, false)
	if err := w.Execute(`ok, err = setVariable("LV_COUNT", "42")`); err != nil {
		t.Fatal(err)
	}
	if *wOpened == 0 {
		t.Error("setVariable on a writable system never asked for a session")
	}
}

// The replay bindings overwrite variables too.
func TestLuaReplayBindings_RefusedOnAReadOnlySystem(t *testing.T) {
	for _, call := range []string{
		`ok, err = injectCheckpoint("cp1")`,
		`ok, err = forceReplay("rec1", 1)`,
		`ok, err = replayFromStep(1)`,
	} {
		t.Run(call, func(t *testing.T) {
			e, _ := readOnlyEngine(t, true)
			if err := e.Execute(call); err != nil {
				t.Fatal(err)
			}
			if msg := e.L.GetGlobal("err").String(); !strings.Contains(msg, "blocked by safety configuration") {
				t.Fatalf("want a safety refusal, got %q", msg)
			}
		})
	}
}
