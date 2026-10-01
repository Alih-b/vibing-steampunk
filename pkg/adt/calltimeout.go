package adt

import (
	"context"
	"net/http"
)

// callDeadlineKey marks a context whose deadline is the whole budget of a call.
type callDeadlineKey struct{}

// WithCallDeadline marks ctx so that its own deadline, rather than the
// client's per-request Timeout (60s by default), bounds every request made
// under it. A caller that gives one long call, such as ABAP Unit or
// ExecuteABAP, a budget of five minutes means five minutes: without this the
// first request to run past the client's Timeout would still be cut off.
//
// A context without a deadline is returned unmarked, so the per-request
// Timeout keeps applying and a request can never hang without bound.
func WithCallDeadline(ctx context.Context) context.Context {
	if _, ok := ctx.Deadline(); !ok {
		return ctx
	}
	return context.WithValue(ctx, callDeadlineKey{}, true)
}

// callDeadlineGoverns reports whether ctx was marked by WithCallDeadline and
// still has a deadline. The mark is a value, so it survives
// context.WithoutCancel, which drops the deadline: a context detached that way
// must fall back to the client's per-request Timeout, or a request under it
// could hang without bound.
func callDeadlineGoverns(ctx context.Context) bool {
	if _, ok := ctx.Deadline(); !ok {
		return false
	}
	v, _ := ctx.Value(callDeadlineKey{}).(bool)
	return v
}

// send hands req to the HTTP client. When the request's context carries a
// call deadline, the client's own Timeout is lifted for this request and the
// deadline alone ends it.
func (t *Transport) send(req *http.Request) (*http.Response, error) {
	if client, ok := t.httpClient.(*http.Client); ok && client.Timeout > 0 && callDeadlineGoverns(req.Context()) {
		unbounded := *client
		unbounded.Timeout = 0
		return unbounded.Do(req)
	}
	return t.httpClient.Do(req)
}
