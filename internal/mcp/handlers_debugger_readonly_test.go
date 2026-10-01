package mcp

import (
	"context"
	"strings"
	"testing"
	"time"
)

// debugRouteRefused asserts that SAP(action="debug", target=objectType) is
// refused by the safety config without a single request reaching SAP.
func debugRouteRefused(t *testing.T, objectType string, params map[string]any) {
	t.Helper()
	s, hits := reportTestServer(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, handled, err := s.routeDebuggerAction(ctx, "debug", objectType, "", params)
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if !res.IsError || !strings.Contains(toolResultText(t, res), "blocked by safety configuration") {
		t.Fatalf("want a safety refusal, got %q", toolResultText(t, res))
	}
	if n := hits(); n != 0 {
		t.Errorf("a refused %s still reached SAP %d time(s)", objectType, n)
	}
}

// debugRouteReachesSAP asserts the same call goes out without --read-only.
func debugRouteReachesSAP(t *testing.T, objectType string, params map[string]any) {
	t.Helper()
	s, hits := reportTestServer(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, _, _ := s.routeDebuggerAction(ctx, "debug", objectType, "", params)
	if res != nil && strings.Contains(toolResultText(t, res), "blocked") {
		t.Fatalf("refused without --read-only: %q", toolResultText(t, res))
	}
	if hits() == 0 {
		t.Errorf("%s never reached SAP without --read-only", objectType)
	}
}

func TestCallRFC_RefusedUnderReadOnly(t *testing.T) {
	params := map[string]any{"function": "Z_DOUBLE", "params": `{"N":"21"}`}
	debugRouteRefused(t, "CALL_RFC", params)
	debugRouteReachesSAP(t, "CALL_RFC", params)
}

func TestMoveObject_RefusedUnderReadOnly(t *testing.T) {
	params := map[string]any{"object_type": "CLAS", "object_name": "ZCL_DEMO", "new_package": "$ZDEMO"}
	debugRouteRefused(t, "MOVE", params)
	debugRouteReachesSAP(t, "MOVE", params)

	// The same handler behind SAP(action="edit", target="MOVE").
	s, hits := reportTestServer(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, handled, err := s.routeCRUDAction(ctx, "edit", "MOVE", "", params)
	if !handled || err != nil || !res.IsError || !strings.Contains(toolResultText(t, res), "blocked by safety configuration") {
		t.Fatalf("edit MOVE: handled=%v err=%v res=%v", handled, err, res)
	}
	if hits() != 0 {
		t.Error("a refused edit MOVE still reached SAP")
	}
}
