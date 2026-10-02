package fakesap

// The worlds below are shared by the CLI and MCP golden tests so that both
// front ends are asked about the same landscape.

const goldClassSource = `CLASS zcl_gold_a DEFINITION PUBLIC.
  PUBLIC SECTION.
    INTERFACES zif_gold.
    METHODS run.
ENDCLASS.
CLASS zcl_gold_a IMPLEMENTATION.
  METHOD run.
    DATA lo TYPE REF TO zcl_foreign.
    DATA lv_fm TYPE string.
    CALL FUNCTION 'Z_GOLD_FM'.
    CALL FUNCTION 'Z_LOST_FM'.
    CALL FUNCTION lv_fm.
    SUBMIT zgold_report.
    SELECT * FROM zgold_t INTO TABLE @DATA(lt_rows).
    cl_abap_typedescr=>describe_by_name( 'X' ).
  ENDMETHOD.
ENDCLASS.`

const goldTestSource = `CLASS zcl_gold_test DEFINITION PUBLIC FOR TESTING.
  PUBLIC SECTION.
    METHODS works FOR TESTING.
ENDCLASS.
CLASS zcl_gold_test IMPLEMENTATION.
  METHOD works.
    DATA lo TYPE REF TO zcl_gold_a.
  ENDMETHOD.
ENDCLASS.`

const goldReportSource = `REPORT zgold_report.
DATA lv TYPE string.
SELECT SINGLE low FROM tvarvc INTO @lv WHERE name = 'ZGOLD_VAR'.
CALL FUNCTION 'Z_GOLD_FM'.
zcl_gold_a=>run( ).`

const goldInterfaceSource = `INTERFACE zif_gold PUBLIC.
  METHODS run.
ENDINTERFACE.`

// Gold is a package with a sub-package, a class that reaches into another
// package, a function module that resolves through its group, one that does
// not, a dynamic call, an unreadable class and a failing unit test.
func Gold() World {
	return World{
		Packages: map[string]string{
			"$ZGOLD":     "",
			"$ZGOLD_SUB": "$ZGOLD",
			"$ZOTHER":    "",
		},
		Objects: []Object{
			{Type: "CLAS", Name: "ZCL_GOLD_A", Package: "$ZGOLD", Source: goldClassSource, Revised: "2019-03-01T10:00:00Z"},
			{Type: "CLAS", Name: "ZCL_GOLD_TEST", Package: "$ZGOLD", Source: goldTestSource, Revised: "2019-02-01T10:00:00Z"},
			{Type: "CLAS", Name: "ZCL_GOLD_DENIED", Package: "$ZGOLD", Source: "x", SourceStatus: 403},
			{Type: "INTF", Name: "ZIF_GOLD", Package: "$ZGOLD", Source: goldInterfaceSource},
			{Type: "TABL", Name: "ZGOLD_T", Package: "$ZGOLD"},
			{Type: "PROG", Name: "ZGOLD_REPORT", Package: "$ZGOLD_SUB", Source: goldReportSource, Revised: "2018-01-01T10:00:00Z"},
			{Type: "CLAS", Name: "ZCL_FOREIGN", Package: "$ZOTHER", Source: "CLASS zcl_foreign DEFINITION PUBLIC. ENDCLASS."},
		},
		FuncModules: map[string]string{
			"Z_GOLD_FM": "SAPLZGOLD_FG",
			"Z_LOST_FM": "SAPLZLOST_FG",
		},
		Groups: map[string]string{
			"ZGOLD_FG": "$ZOTHER",
		},
		TVARVCReaders: [2][]string{
			{"ZCL_GOLD_A====================CM001"},
			{"ZGOLD_REPORT", "ZGOLD_MISSING"},
		},
		D010INC: [][2]string{
			{"ZCL_GOLD_A====================CP", "ZCL_GOLD_A====================CU"},
			{"ZCL_GOLD_A====================CP", "ZIF_GOLD======================IU"},
			{"ZGOLD_REPORT", "ZCL_GOLD_A====================CU"},
			{"ZGOLD_REPORT", "<SYSINI>"},
		},
		UnitTests: map[string]string{
			"/sap/bc/adt/packages/$ZGOLD":            "fail",
			"/sap/bc/adt/packages/$ZGOLD_SUB":        "deny",
			"/sap/bc/adt/oo/classes/zcl_gold_test":   "fail",
			"/sap/bc/adt/oo/classes/zcl_gold_a":      "pass",
			"/sap/bc/adt/programs/programs/zgold_rp": "deny",
		},
		ATCFindings: []int{1, 2, 3, 3},
	}
}

// Narrow is a package of one class whose source names two function modules
// outside it, so a TADIR or TFDIR lookup is exactly one batch and a failure names at
// most five objects. That keeps a failed batch's casualties independent of map
// order, and keeps every one of them inside the five a caveat lists by name —
// both of which a golden file needs.
func Narrow() World {
	w := Gold()
	w.Packages = map[string]string{"$ZNARROW": "", "$ZOTHER": ""}
	w.Objects = []Object{
		{Type: "CLAS", Name: "ZCL_NARROW", Package: "$ZNARROW", Revised: "2026-01-01T10:00:00Z", Source: `CLASS zcl_narrow DEFINITION PUBLIC.
  PUBLIC SECTION.
    METHODS run.
ENDCLASS.
CLASS zcl_narrow IMPLEMENTATION.
  METHOD run.
    CALL FUNCTION 'Z_GOLD_FM'.
    CALL FUNCTION 'Z_LOST_FM'.
  ENDMETHOD.
ENDCLASS.`},
		{Type: "CLAS", Name: "ZCL_FOREIGN", Package: "$ZOTHER"},
	}
	w.UnitTests = map[string]string{"/sap/bc/adt/packages/$ZNARROW": "pass", "/sap/bc/adt/oo/classes/zcl_narrow": "pass"}
	w.ATCFindings = nil
	return w
}

// NarrowTADIRDown is Narrow with the TADIR name lookup refused: pass one loses
// every name, and pass two rescues the function modules through their groups.
func NarrowTADIRDown() World {
	w := Narrow()
	w.Fail = []string{"'R3TR' AND OBJ_NAME IN"}
	return w
}

// NarrowFUGRDown is Narrow with the function-group lookup refused: TFDIR finds
// the groups, and their packages cannot be read.
func NarrowFUGRDown() World {
	w := Narrow()
	w.Fail = []string{"AND OBJECT = 'FUGR'"}
	return w
}

// NarrowAllDown refuses the TADIR name lookup and the function-group lookup
// both, so nothing outside the package is placed.
func NarrowAllDown() World {
	w := Narrow()
	w.Fail = []string{"'R3TR' AND OBJ_NAME IN", "AND OBJECT = 'FUGR'"}
	return w
}

// NarrowTFDIRDown is Narrow with TFDIR refused: pass one places what TADIR
// knows, and the function modules are left where pass two could not reach.
func NarrowTFDIRDown() World {
	w := Narrow()
	w.Fail = []string{"FROM TFDIR"}
	return w
}

// CrossDown refuses both TVARVC cross-reference reads, and WBCROSSGT alone.
func CrossDown(both bool) World {
	w := Gold()
	w.Fail = []string{"FROM WBCROSSGT"}
	if both {
		w.Fail = append(w.Fail, "FROM CROSS ")
	}
	return w
}
