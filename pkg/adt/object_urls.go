package adt

import (
	"fmt"
	"net/url"
	"strings"
)

// --- Helper to get object URLs ---

// GetObjectURL returns the ADT URL for an object based on its type and name.
// All names are URL-encoded to support namespaced objects like /UI5/CL_REPOSITORY_LOAD.
func GetObjectURL(objectType CreatableObjectType, name string, parentName string) string {
	name = strings.ToUpper(name)
	encodedName := url.PathEscape(name)

	switch objectType {
	case ObjectTypeProgram:
		return fmt.Sprintf("/sap/bc/adt/programs/programs/%s", encodedName)
	case ObjectTypeInclude:
		return fmt.Sprintf("/sap/bc/adt/programs/includes/%s", encodedName)
	case ObjectTypeClass:
		return fmt.Sprintf("/sap/bc/adt/oo/classes/%s", encodedName)
	case ObjectTypeInterface:
		return fmt.Sprintf("/sap/bc/adt/oo/interfaces/%s", encodedName)
	case ObjectTypeFunctionGroup:
		return fmt.Sprintf("/sap/bc/adt/functions/groups/%s", encodedName)
	case ObjectTypeFunctionMod:
		parentName = strings.ToUpper(parentName)
		encodedParent := url.PathEscape(parentName)
		return fmt.Sprintf("/sap/bc/adt/functions/groups/%s/fmodules/%s", encodedParent, encodedName)
	case ObjectTypePackage:
		return fmt.Sprintf("/sap/bc/adt/packages/%s", encodedName)
	case ObjectTypeMessageClass:
		return fmt.Sprintf("/sap/bc/adt/messageclass/%s", url.PathEscape(strings.ToLower(name)))
	// RAP object types - use lowercase for CDS objects
	case ObjectTypeDDLS:
		return fmt.Sprintf("/sap/bc/adt/ddic/ddl/sources/%s", url.PathEscape(strings.ToLower(name)))
	case ObjectTypeBDEF:
		return fmt.Sprintf("/sap/bc/adt/bo/behaviordefinitions/%s", url.PathEscape(strings.ToLower(name)))
	case ObjectTypeSRVD:
		return fmt.Sprintf("/sap/bc/adt/ddic/srvd/sources/%s", url.PathEscape(strings.ToLower(name)))
	case ObjectTypeTable:
		// A DDIC table's source is its DDL, at the same shape as the CDS types
		// above. Addressable all along; nothing asked for it.
		return fmt.Sprintf("/sap/bc/adt/ddic/tables/%s", url.PathEscape(strings.ToLower(name)))
	case ObjectTypeSRVB:
		return fmt.Sprintf("/sap/bc/adt/businessservices/bindings/%s", url.PathEscape(strings.ToLower(name)))
	default:
		return ""
	}
}

// GetSourceURL returns the source URL for an object.
func GetSourceURL(objectType CreatableObjectType, name string, parentName string) string {
	objectURL := GetObjectURL(objectType, name, parentName)
	if objectURL == "" {
		return ""
	}
	return objectURL + "/source/main"
}
