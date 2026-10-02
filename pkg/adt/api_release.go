package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// --- API Release State (Clean Core) ---

// GetAPIReleaseState retrieves the API release state for an ABAP object.
// This checks whether the object is released for use in ABAP Cloud (S/4HANA Clean Core).
func (c *Client) GetAPIReleaseState(ctx context.Context, objectURI string) (*APIReleaseState, error) {
	// objectURI is the full ADT path of the OBJECT, e.g. "/sap/bc/adt/oo/classes/cl_abap_typedescr".
	// We escape it to attach it to the endpoint path.
	endpoint := fmt.Sprintf("/sap/bc/adt/apireleases/%s", url.PathEscape(objectURI))

	resp, err := c.transport.Request(ctx, endpoint, &RequestOptions{
		Method: http.MethodGet,
		Accept: "application/vnd.sap.adt.apirelease.v10+xml",
	})
	if err != nil {
		return nil, fmt.Errorf("getting API release state: %w", err)
	}

	body := strings.TrimSpace(string(resp.Body))

	if u, err := strconv.Unquote(body); err == nil {
		body = u
	}

	if strings.Contains(body, "&lt;") {
		body = html.UnescapeString(body)
	}

	var state APIReleaseState
	if err := xml.Unmarshal([]byte(body), &state); err != nil {
		return nil, fmt.Errorf("unmarshal API release state: %w", err)
	}

	return &state, nil
}
