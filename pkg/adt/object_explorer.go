package adt

import (
	"context"
	"strings"
)

// ObjectExplorerNode represents a node in the object explorer tree.
type ObjectExplorerNode struct {
	URI         string               `json:"uri"`
	Name        string               `json:"name"`
	Type        string               `json:"type"`
	Description string               `json:"description,omitempty"`
	Children    []ObjectExplorerNode `json:"children,omitempty"`
}

// GetObjectStructureCAI returns an object's components as a tree.
//
// It used to ask /sap/bc/adt/cai/objectexplorer/objects, and that resource does
// not exist. Not "not on older releases" — it is advertised in the discovery
// document of none of 7.50, 7.57 or 7.58 and answers 404 on all of them, along
// with the rest of the /cai/ namespace, which also took the call graph down
// with it. So this had never returned anything, and its three callers —
// analyze type=object_structure among them — had never worked.
//
// The replacement was already in this file. /sap/bc/adt/oo/classes/{name}/
// objectstructure answers 200 with a richer document, and GetClassObjectStructure
// already spoke it. The callers are left alone deliberately: the fix belongs
// where the wrong URL was, not spread across everything that trusted it.
//
// maxResults is honoured because callers pass it, though the resource returns a
// whole class in one answer and there is nothing to page.
func (c *Client) GetObjectStructureCAI(ctx context.Context, objectName string, maxResults int) (*ObjectExplorerNode, error) {
	if maxResults <= 0 {
		maxResults = 100
	}
	structure, err := c.GetClassObjectStructure(ctx, objectName)
	if err != nil {
		return nil, err
	}
	if structure == nil {
		return nil, nil
	}

	root := &ObjectExplorerNode{
		Name: structure.Name,
		Type: structure.Type,
		URI:  "/sap/bc/adt/oo/classes/" + strings.ToLower(objectName),
	}
	for i, el := range structure.Elements {
		if i >= maxResults {
			break
		}
		root.Children = append(root.Children, ObjectExplorerNode{
			Name: el.Name,
			Type: el.Type,
		})
	}
	return root, nil
}
