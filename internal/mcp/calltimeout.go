package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// MaxCallTimeout caps a per-call budget, and the server default. An hour is far beyond any ABAP Unit
// run or deploy an agent should wait on synchronously.
const MaxCallTimeout = time.Hour

// callTimeoutDescription documents the timeout parameter of every long call.
const callTimeoutDescription = "Seconds this call may take in all (default: the server's --call-timeout; without one, each request to SAP is limited to 60s). When it runs out the call returns a timeout message; the work may still be running on SAP."

// callBudget reads a call's budget: params.timeout in seconds, else the
// server default (--call-timeout / SAP_CALL_TIMEOUT). Zero means no budget of
// the call's own: each request to SAP is still bounded by the client's
// per-request timeout.
func callBudget(args map[string]any, serverDefault time.Duration) (time.Duration, error) {
	raw, ok := args["timeout"]
	if !ok || raw == nil {
		return serverDefault, nil
	}
	var secs float64
	switch v := raw.(type) {
	case float64:
		secs = v
	case int:
		secs = float64(v)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("timeout must be a number of seconds, got %q", v)
		}
		secs = f
	default:
		return 0, fmt.Errorf("timeout must be a number of seconds, got %v", raw)
	}
	if math.IsNaN(secs) || math.IsInf(secs, 0) || secs <= 0 {
		return 0, fmt.Errorf("timeout must be a positive number of seconds, got %v", raw)
	}
	// Capped in seconds, before the conversion: a huge value converted
	// first overflows int64 nanoseconds to a non-positive Duration, which
	// would leave the call without any deadline.
	if secs >= MaxCallTimeout.Seconds() {
		return MaxCallTimeout, nil
	}
	d := time.Duration(secs * float64(time.Second))
	if d <= 0 {
		// Below a nanosecond: zero would read as "no budget".
		return 0, fmt.Errorf("timeout must be a positive number of seconds, got %v", raw)
	}
	return d, nil
}

// longCall runs one long operation (ExecuteABAP, ABAP Unit, a deploy) under
// the call's budget, and turns the bare "context deadline exceeded",
// "context canceled" or "Client.Timeout exceeded" it would otherwise end with
// into a sentence that says what happened: the wait ended, the work on SAP may
// not have.
func (s *Server) longCall(ctx context.Context, request mcp.CallToolRequest, op string, h handlerFunc) (*mcp.CallToolResult, error) {
	var serverDefault time.Duration
	if s.config != nil {
		serverDefault = s.config.CallTimeout
	}
	budget, err := callBudget(request.GetArguments(), serverDefault)
	if err != nil {
		return newToolResultError(err.Error()), nil
	}
	start := time.Now()
	callCtx := ctx
	if budget > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
		callCtx = adt.WithCallDeadline(callCtx)
	}
	result, herr := h(callCtx, request)
	ended := callEnd{budget: budget, elapsed: time.Since(start)}
	if dl, ok := callCtx.Deadline(); ok {
		ended.hasDeadline = true
		ended.deadline = max(dl.Sub(start), 0)
	}
	if msg := timeoutMessage(op, callCtx, ended, result, herr); msg != "" {
		return newToolResultError(msg), nil
	}
	return result, herr
}

// callEnd is what longCall knows about a call's time when it ends.
type callEnd struct {
	budget time.Duration // the call's own budget; 0 when it had none
	// deadline is from start to the effective deadline, the earlier of the
	// budget's and the caller's, when hasDeadline.
	deadline    time.Duration
	hasDeadline bool
	elapsed     time.Duration
}

// cancellationMarkers are the texts an ended context or an HTTP timeout
// leaves in an error.
var cancellationMarkers = []string{"context deadline exceeded", "context canceled", "Client.Timeout"}

// reflectsCancellation reports whether what the handler returned is the
// result of its context ending: a context error, or a context error's text in
// its error or its result. A handler that finished its work and returned an
// ordinary answer did not time out, even if the deadline passed just after.
func reflectsCancellation(text string, herr error) bool {
	if errors.Is(herr, context.DeadlineExceeded) || errors.Is(herr, context.Canceled) {
		return true
	}
	for _, m := range cancellationMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// timeoutMessage names a call that ended because a wait ran out, or "" when
// it did not. It rewrites only a result or error that reflects the ending; a
// call whose work completed is left alone. What the handler said is kept in
// full as detail: it can end in a cleanup warning (a lock left behind, a
// temporary program not deleted) the caller needs to act on.
func timeoutMessage(op string, ctx context.Context, end callEnd, result *mcp.CallToolResult, herr error) string {
	// Not only an error result: ExecuteABAP reports a run it could not finish
	// as an ordinary result ("Success: false"), with the context's error
	// somewhere inside it.
	text := ""
	if herr != nil {
		text = herr.Error()
	} else {
		text = resultText(result)
	}
	if !reflectsCancellation(text, herr) {
		return ""
	}
	const still = "the operation may still be running on SAP (check SM50/SM66 before retrying it)"
	var msg string
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		callersEarlier := end.hasDeadline && (end.budget == 0 || end.deadline < end.budget)
		switch {
		case callersEarlier && end.budget > 0:
			msg = fmt.Sprintf("%s timed out after %s, at the caller's own deadline, before its budget of %s; %s.",
				op, seconds(end.deadline), seconds(end.budget), still)
		case callersEarlier:
			msg = fmt.Sprintf("%s timed out after %s, at the caller's own deadline; %s.",
				op, seconds(end.deadline), still)
		default:
			msg = fmt.Sprintf("%s timed out after %s; %s. Pass a larger params.timeout (seconds), or start the server with a larger --call-timeout.",
				op, seconds(end.budget), still)
		}
	case errors.Is(ctx.Err(), context.Canceled):
		msg = fmt.Sprintf("%s was cancelled by the client after %s; %s.", op, seconds(end.elapsed), still)
	case strings.Contains(text, "Client.Timeout"):
		msg = fmt.Sprintf("%s timed out after %s: one request to SAP ran past the per-request limit; %s. Pass params.timeout (seconds) to give the whole call a longer budget.",
			op, seconds(end.elapsed), still)
	case end.budget > 0 && strings.Contains(text, "context deadline exceeded"):
		// The call's own context is live, so a shorter deadline inside the
		// call ended one of its requests.
		msg = fmt.Sprintf("%s stopped after %s: a request to SAP ran out of time within the call; %s.",
			op, seconds(end.elapsed), still)
	default:
		return ""
	}
	if detail := strings.TrimSpace(text); detail != "" {
		msg += "\nDetail: " + detail
	}
	return msg
}

func seconds(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return fmt.Sprintf("%ds", int(math.Round(d.Seconds())))
}
