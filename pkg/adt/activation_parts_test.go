package adt

import (
	"strings"
	"testing"
)

func TestInactivePartsOf(t *testing.T) {
	recs := []InactiveObjectRecord{
		{Object: &InactiveObject{URI: "/sap/bc/adt/functions/groups/zdemo", Name: "ZDEMO", User: "DEV"}},
		{Object: &InactiveObject{URI: "/sap/bc/adt/functions/groups/zdemo/includes/lzdemotop", Name: "LZDEMOTOP", User: "DEV"}},
		{Object: &InactiveObject{URI: "/sap/bc/adt/functions/groups/zdemo/includes/lzdemof01", Name: "LZDEMOF01", User: "OTHER"}},
		{Object: &InactiveObject{URI: "/sap/bc/adt/functions/groups/zdemo2/includes/lzdemo2top", Name: "LZDEMO2TOP", User: "DEV"}},
		{Object: &InactiveObject{URI: "/sap/bc/adt/functions/groups/zdemo/includes/lzdemof02", Name: "LZDEMOF02", User: "DEV", Deleted: true}},
	}
	parts := inactivePartsOf("/sap/bc/adt/functions/groups/ZDEMO", "dev", recs)
	if len(parts) != 1 || parts[0].Name != "LZDEMOTOP" {
		t.Errorf("parts %+v: only the caller's own, live part below the group, not the group itself, not ZDEMO2", parts)
	}
	if !objectInactive("/sap/bc/adt/functions/groups/ZDEMO/source/main", recs) {
		t.Error("the group itself is inactive")
	}
	if objectInactive("/sap/bc/adt/functions/groups/zother", recs) {
		t.Error("an object not in the list is not inactive")
	}
}

func TestRefusedWithoutReasonAndProblemLines(t *testing.T) {
	if !refusedWithoutReason(&ActivationResult{}) {
		t.Error("an empty refusal")
	}
	if refusedWithoutReason(&ActivationResult{Success: true}) ||
		refusedWithoutReason(&ActivationResult{Messages: []ActivationResultMessage{{Type: "E"}}}) {
		t.Error("a success or a refusal with a message is not without reason")
	}
	r := &ActivationResult{Inactive: []InactiveObject{{Name: "LZDEMOTOP", URI: "/x/lzdemotop"}}}
	if got := strings.Join(r.ProblemLines(), ";"); !strings.Contains(got, "still inactive: LZDEMOTOP") {
		t.Errorf("problem lines %q", got)
	}
}
