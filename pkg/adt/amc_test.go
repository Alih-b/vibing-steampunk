package adt

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// amcRoute answers an AMC application that exists: LOCK succeeds, the PUT of
// its definition fails (and ends the caller's context, as a cancelled or
// timed-out call would), UNLOCK answers unlockStatus.
func amcRoute(cancel context.CancelFunc, unlockStatus int, unlocks *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "UNLOCK":
			unlocks.Add(1)
			w.WriteHeader(unlockStatus)
		case r.Method == http.MethodPut:
			cancel()
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}
}

// A failed write releases the lock even when the caller's context is over,
// and a lock that cannot be released is reported, not dropped
// (PR #296 review).
func TestUpsertAMCApplicationReleasesTheLockAfterAFailedWrite(t *testing.T) {
	var unlocks atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := newStubbedClient(t, &adtRecorder{}, amcRoute(cancel, http.StatusOK, &unlocks))
	err := client.UpsertAMCApplication(ctx, "ZDEMO_AMC", "demo", "$TMP", "<asx:abap/>")
	if err == nil || !strings.Contains(err.Error(), "writing AMC application") {
		t.Fatalf("got %v", err)
	}
	if unlocks.Load() != 1 {
		t.Errorf("%d UNLOCKs after the failed write; want 1, on a context the caller's cancel does not reach", unlocks.Load())
	}

	unlocks.Store(0)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	client = newStubbedClient(t, &adtRecorder{}, amcRoute(cancel2, http.StatusInternalServerError, &unlocks))
	err = client.UpsertAMCApplication(ctx2, "ZDEMO_AMC", "demo", "$TMP", "<asx:abap/>")
	if err == nil || !strings.Contains(err.Error(), "left LOCKED") {
		t.Errorf("a lock left behind is not reported: %v", err)
	}
}
