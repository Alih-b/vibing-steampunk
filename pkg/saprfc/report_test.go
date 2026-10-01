package saprfc

import (
	"reflect"
	"testing"
)

func TestAbapStep_CarriesTheVariantAndTheSelection(t *testing.T) {
	step := abapStep("VSP_ZREPORT", "12345678", "ZREPORT", " default ", []ReportParam{
		{Name: "p_werks", Low: "1000"},
		{Name: "S_DATUM", Kind: "S", Option: "BT", Low: "20260101", High: "20260131"},
	})
	if step["ABAP_PROGRAM_NAME"] != "ZREPORT" || step["JOBCOUNT"] != "12345678" {
		t.Errorf("step = %v", step)
	}
	if step["ABAP_VARIANT_NAME"] != "DEFAULT" {
		t.Errorf("ABAP_VARIANT_NAME = %v, want DEFAULT", step["ABAP_VARIANT_NAME"])
	}
	want := []map[string]any{
		{"SELNAME": "P_WERKS", "KIND": "P", "SIGN": "I", "OPTION": "EQ", "LOW": "1000", "HIGH": ""},
		{"SELNAME": "S_DATUM", "KIND": "S", "SIGN": "I", "OPTION": "BT", "LOW": "20260101", "HIGH": "20260131"},
	}
	if got := step["SELINFO"]; !reflect.DeepEqual(got, want) {
		t.Errorf("SELINFO = %v, want %v", got, want)
	}
	if _, ok := step["ALLPRIPAR"]; !ok {
		t.Error("without print parameters the step writes no spool")
	}
}

func TestAbapStep_NoVariantNoSelection(t *testing.T) {
	step := abapStep("VSP_ZREPORT", "1", "ZREPORT", "", nil)
	if _, ok := step["ABAP_VARIANT_NAME"]; ok {
		t.Error("an empty variant must not be sent")
	}
	if _, ok := step["SELINFO"]; ok {
		t.Error("an empty selection must not be sent")
	}
}
