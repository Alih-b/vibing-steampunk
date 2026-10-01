package adt

import (
	"net/http"
	"net/http/httptest"
)

// handlerRoundTripper serves every request from h in-process, with no socket.
// A client built on it can run inside a testing/synctest bubble: an httptest
// server's network I/O never blocks durably, so it cannot.
type handlerRoundTripper struct{ h http.Handler }

func (rt handlerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	rt.h.ServeHTTP(rec, req.Clone(req.Context()))
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

// newInMemoryClient is NewClient against h instead of a server: the same
// config, the same cookie jar and redirect policy, only the round trip is
// replaced. baseURL only has to parse; nothing is dialled.
func newInMemoryClient(h http.Handler, opts ...Option) *Client {
	cfg := NewConfig("http://sap.invalid", "TESTUSER", "pw", opts...)
	hc := cfg.NewHTTPClient()
	hc.Transport = handlerRoundTripper{h}
	return NewClientWithTransport(cfg, NewTransportWithClient(cfg, hc))
}
