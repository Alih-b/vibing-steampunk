package adt

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// --- Table Operations ---

// GetTable retrieves the source/definition of a database table.
func (c *Client) GetTable(ctx context.Context, tableName string) (string, error) {
	tableName = strings.ToUpper(tableName)

	// URL encode to handle namespaced objects like /DMO/TRAVEL
	sourcePath := fmt.Sprintf("/sap/bc/adt/ddic/tables/%s/source/main", url.PathEscape(tableName))
	resp, err := c.transport.Request(ctx, sourcePath, &RequestOptions{
		Method: http.MethodGet,
	})
	if err != nil {
		return "", fmt.Errorf("getting table source: %w", err)
	}

	return string(resp.Body), nil
}

// GetView retrieves the source/definition of a DDIC database view.
// This is for classic DDIC views (SE11), not CDS views (which use GetDDLS).
func (c *Client) GetView(ctx context.Context, viewName string) (string, error) {
	viewName = strings.ToUpper(viewName)

	// URL encode the name to handle namespaced objects like /DMO/...
	sourcePath := fmt.Sprintf("/sap/bc/adt/ddic/views/%s/source/main", url.PathEscape(viewName))
	resp, err := c.transport.Request(ctx, sourcePath, &RequestOptions{
		Method: http.MethodGet,
	})
	if err != nil {
		return "", fmt.Errorf("getting view source: %w", err)
	}

	return string(resp.Body), nil
}

// GetStructure retrieves the source/definition of a data structure.
func (c *Client) GetStructure(ctx context.Context, structName string) (string, error) {
	structName = strings.ToUpper(structName)

	// URL encode to handle namespaced objects like /DMO/...
	sourcePath := fmt.Sprintf("/sap/bc/adt/ddic/structures/%s/source/main", url.PathEscape(structName))
	resp, err := c.transport.Request(ctx, sourcePath, &RequestOptions{
		Method: http.MethodGet,
	})
	if err != nil {
		return "", fmt.Errorf("getting structure source: %w", err)
	}

	return string(resp.Body), nil
}
