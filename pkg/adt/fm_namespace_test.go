package adt

import (
	"strings"
	"testing"
)

// A function module in a namespaced group: the group has to be escaped in
// the creation URL and in the container reference, as in every other URL.
func TestFunctionModuleInNamespacedGroup(t *testing.T) {
	opts := CreateObjectOptions{ObjectType: ObjectTypeFunctionMod, Name: "/NS/FM", ParentName: "/NS/GROUP", Description: "d"}
	if got := creationURLFor(opts, objectTypes[ObjectTypeFunctionMod]); got != "/sap/bc/adt/functions/groups/%2Fns%2Fgroup/fmodules" {
		t.Errorf("creation URL %s", got)
	}
	body := buildCreateObjectBody(opts, objectTypes[ObjectTypeFunctionMod], "DEV")
	if !strings.Contains(body, `adtcore:uri="/sap/bc/adt/functions/groups/%2Fns%2Fgroup"`) {
		t.Errorf("container reference not escaped:\n%s", body)
	}
	if got := creationURLFor(CreateObjectOptions{ObjectType: ObjectTypeFunctionMod, ParentName: "ZGROUP"}, objectTypes[ObjectTypeFunctionMod]); got != "/sap/bc/adt/functions/groups/zgroup/fmodules" {
		t.Errorf("plain group %s", got)
	}
}

func TestGroupFromFunctionURI_Namespace(t *testing.T) {
	for uri, want := range map[string]string{
		"/sap/bc/adt/functions/groups/%2fns%2fvsp_fg/fmodules/%2fns%2fvsp_fm": "/NS/VSP_FG",
		"/sap/bc/adt/functions/groups/zdemo/fmodules/z_demo":                  "ZDEMO",
	} {
		if got := groupFromFunctionURI(uri); got != want {
			t.Errorf("%s: %s, want %s", uri, got, want)
		}
	}
}

// The metadata a remote-enabled module gets after its creation carries the
// container reference too; it must be escaped the same way.
func TestFunctionModuleMetadataXML_ContainerURI(t *testing.T) {
	for group, uri := range map[string]string{
		"/NS/GROUP": `adtcore:uri="/sap/bc/adt/functions/groups/%2Fns%2Fgroup"`,
		"ZGROUP":    `adtcore:uri="/sap/bc/adt/functions/groups/zgroup"`,
	} {
		body := functionModuleMetadataXML(&FunctionModuleInfo{Name: "Z_FM", Group: group, Description: "d"}, FunctionProcessingRFC)
		if !strings.Contains(body, uri) {
			t.Errorf("group %s: container reference missing %s:\n%s", group, uri, body)
		}
		if !strings.Contains(body, `adtcore:name="`+group+`" adtcore:type="FUGR/F"`) {
			t.Errorf("group %s: container name not kept:\n%s", group, body)
		}
	}
}
