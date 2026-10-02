//go:build integration

package adt

import (
	"context"
	"os"
	"strings"
	"testing"
)

// The function-module flow needs an existing function group to write into, so
// the group and its package come from the environment rather than being baked
// into the repo:
//
//	SAP_URL=… SAP_USER=… SAP_PASSWORD=… \
//	VSP_TEST_FUGR=<existing group> VSP_TEST_PACKAGE=<its package> \
//	go test -tags=integration -run FunctionModule -v ./pkg/adt/
func functionTestTarget(t *testing.T) (group, pkg string) {
	t.Helper()

	group = os.Getenv("VSP_TEST_FUGR")
	if group == "" {
		t.Skip("VSP_TEST_FUGR (an existing function group) required for function module tests")
	}
	return group, integrationPackage()
}

// TestIntegration_CreateRFCFunctionModule covers the whole reason
// CreateFunctionModule exists: a module created through the plain creation
// endpoint comes back with processingType="normal" even when the creation
// document asks for "rfc". Only the metadata PUT that CreateFunctionModule
// performs makes the module remote-enabled.
func TestIntegration_CreateRFCFunctionModule(t *testing.T) {
	client := getIntegrationClient(t)
	ctx := context.Background()
	group, pkg := functionTestTarget(t)

	const name = "ZVSP_IT_RFC_PROBE"
	objectURL := GetObjectURL(ObjectTypeFunctionMod, name, group)

	// Leave nothing behind, whatever the assertions do.
	t.Cleanup(func() {
		lock, err := client.LockObject(ctx, objectURL, "MODIFY")
		if err != nil {
			t.Logf("cleanup: could not lock %s: %v", name, err)
			return
		}
		if err := client.DeleteObject(ctx, objectURL, lock.LockHandle, ""); err != nil {
			t.Logf("cleanup: could not delete %s: %v", name, err)
		}
	})

	result, err := client.CreateFunctionModule(ctx, CreateFunctionModuleOptions{
		Group:       group,
		Name:        name,
		Description: "vsp integration probe: RFC-enabled function module",
		PackageName: pkg,
		RFCEnabled:  true,
		Source: `FUNCTION ` + strings.ToLower(name) + `
  IMPORTING
    VALUE(iv_n) TYPE i
  EXPORTING
    VALUE(ev_result) TYPE i.

  ev_result = iv_n * 2.

ENDFUNCTION.`,
	})
	if err != nil {
		t.Fatalf("CreateFunctionModule failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("CreateFunctionModule reported failure: %s", result.Message)
	}
	if !result.RFCEnabled || !result.SourceWritten {
		t.Errorf("result = %+v, want RFCEnabled and SourceWritten", result)
	}

	// The flag is what the creation endpoint silently drops — read it back
	// from SAP rather than trusting our own result struct.
	info, err := client.GetFunctionModule(ctx, group, name)
	if err != nil {
		t.Fatalf("GetFunctionModule failed: %v", err)
	}
	if !info.IsRFCEnabled() {
		t.Errorf("processingType = %q, want %q — the module is not remote-enabled",
			info.ProcessingType, FunctionProcessingRFC)
	}
	if info.Description == "" {
		t.Error("description was blanked by the metadata PUT")
	}
	if !strings.EqualFold(info.Group, group) {
		t.Errorf("group = %q, want %q", info.Group, group)
	}

	// And back again: the switch has to work in both directions.
	if err := client.SetFunctionModuleProcessingType(ctx, group, name, FunctionProcessingNormal); err != nil {
		t.Fatalf("SetFunctionModuleProcessingType(normal) failed: %v", err)
	}
	info, err = client.GetFunctionModule(ctx, group, name)
	if err != nil {
		t.Fatalf("GetFunctionModule after switch failed: %v", err)
	}
	if info.IsRFCEnabled() {
		t.Errorf("processingType = %q, want it switched back to %q",
			info.ProcessingType, FunctionProcessingNormal)
	}
}

// TestIntegration_CreateFunctionGroupThenRFCModule covers the from-scratch
// path: a new function group, then a remote-enabled module inside it. The
// group creation endpoint is plain CreateObject — only the module needs the
// extra metadata PUT.
func TestIntegration_CreateFunctionGroupThenRFCModule(t *testing.T) {
	client := getIntegrationClient(t)
	ctx := context.Background()

	pkg := integrationPackage()

	const group = "ZVSP_IT_GRP"
	const name = "ZVSP_IT_GRP_FM"
	groupURL := GetObjectURL(ObjectTypeFunctionGroup, group, "")

	t.Cleanup(func() {
		// The module goes with the group, so deleting the group is enough.
		lock, err := client.LockObject(ctx, groupURL, "MODIFY")
		if err != nil {
			t.Logf("cleanup: could not lock %s: %v", group, err)
			return
		}
		if err := client.DeleteObject(ctx, groupURL, lock.LockHandle, ""); err != nil {
			t.Logf("cleanup: could not delete %s: %v", group, err)
		}
	})

	if err := client.CreateObject(ctx, CreateObjectOptions{
		ObjectType:  ObjectTypeFunctionGroup,
		Name:        group,
		Description: "vsp integration probe: function group",
		PackageName: pkg,
	}); err != nil {
		t.Fatalf("CreateObject(FUGR/F) failed: %v", err)
	}
	if _, err := client.Activate(ctx, groupURL, group); err != nil {
		t.Fatalf("activating group failed: %v", err)
	}

	result, err := client.CreateFunctionModule(ctx, CreateFunctionModuleOptions{
		Group:       group,
		Name:        name,
		Description: "vsp integration probe: RFC module in a fresh group",
		PackageName: pkg,
		RFCEnabled:  true,
		Source: `FUNCTION ` + strings.ToLower(name) + `
  IMPORTING
    VALUE(iv_text) TYPE string
  EXPORTING
    VALUE(ev_text) TYPE string.

  ev_text = |echo: { iv_text }|.

ENDFUNCTION.`,
	})
	if err != nil {
		t.Fatalf("CreateFunctionModule in fresh group failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("CreateFunctionModule reported failure: %s", result.Message)
	}

	info, err := client.GetFunctionModule(ctx, group, name)
	if err != nil {
		t.Fatalf("GetFunctionModule failed: %v", err)
	}
	if !info.IsRFCEnabled() {
		t.Errorf("processingType = %q, want %q", info.ProcessingType, FunctionProcessingRFC)
	}
}
