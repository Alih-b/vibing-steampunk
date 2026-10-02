package adt

import (
	"context"
	"net/http"
	"testing"
)

// DeleteObjectGated releases its lock after the DELETE, because the DELETE
// does not release the ENQUEUE. Behind a session-holding proxy
// (ProxyContextIDGuard) DeleteObject has already retired the context that
// held it, so an UNLOCK would land in a fresh context, fail, and report a
// stranded lock that is not there.

func unlocksAfter(calls []wireCall, i int) int {
	n := 0
	for _, c := range calls[i+1:] {
		if c.method == http.MethodPost && c.query.Get("_action") == "UNLOCK" {
			n++
		}
	}
	return n
}

func TestDeleteObjectGated_UnlocksAfterDelete(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, deleteRoute("/sap/bc/adt/programs/programs/zdemo_del", "ZDEMO_DEL", "$TMP"),
		WithAllowedPackages("$TMP"))
	note, err := client.DeleteObjectGated(context.Background(), "/sap/bc/adt/programs/programs/zdemo_del", "")
	if err != nil || note != "" {
		t.Fatalf("DeleteObjectGated: note=%q err=%v", note, err)
	}
	calls := rec.snapshot()
	del := lastIndexBefore(calls, len(calls), isDelete)
	if del < 0 || unlocksAfter(calls, del) != 1 {
		dumpCalls(t, calls)
		t.Fatal("want one UNLOCK after the DELETE")
	}
}

func TestDeleteObjectGated_ProxyGuardSkipsTheUnlock(t *testing.T) {
	rec := &adtRecorder{}
	client := newStubbedClient(t, rec, deleteRoute("/sap/bc/adt/programs/programs/zdemo_del", "ZDEMO_DEL", "$TMP"),
		WithAllowedPackages("$TMP"), WithProxyContextIDGuard())
	note, err := client.DeleteObjectGated(context.Background(), "/sap/bc/adt/programs/programs/zdemo_del", "")
	if err != nil {
		t.Fatalf("DeleteObjectGated: %v", err)
	}
	if note != "" {
		t.Errorf("note = %q; the context was retired with the DELETE, so there is no stranded lock to report", note)
	}
	calls := rec.snapshot()
	del := lastIndexBefore(calls, len(calls), isDelete)
	if del < 0 {
		dumpCalls(t, calls)
		t.Fatal("no DELETE was sent")
	}
	if n := unlocksAfter(calls, del); n != 0 {
		dumpCalls(t, calls)
		t.Errorf("%d UNLOCK(s) after the DELETE; behind the proxy guard the context is already retired", n)
	}
}
