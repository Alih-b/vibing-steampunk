package adt

import "testing"

func TestStructureSourceName(t *testing.T) {
	for src, want := range map[string][2]string{
		"@EndUserText.label : 'x'\ndefine structure zdemo {\n  a : abap.char(1);\n}":                            {"ZDEMO", ""},
		"define   structure /ns/s_demo { a : abap.char(1); }":                                                   {"/NS/S_DEMO", ""},
		"@AbapCatalog.enhancement.category : #NOT_EXTENSIBLE\nextend type shp_vl10_item with /ns/append_x {\n}": {"/NS/APPEND_X", "SHP_VL10_ITEM"},
		"// extend type fake with wrong\ndefine structure zdemo { a : abap.char(1); }":                          {"ZDEMO", ""},
		"/* define structure zold */ @EndUserText.label : 'extend type x with y'\ndefine structure znew { }":    {"ZNEW", ""},
		"-- define structure zold\nextend type zbase with zappend { }":                                          {"ZAPPEND", "ZBASE"},
	} {
		name, extends, err := structureSourceName(src)
		if err != nil || name != want[0] || extends != want[1] {
			t.Errorf("%q: %s %s %v, want %v", src, name, extends, err, want)
		}
	}
	if _, _, err := structureSourceName("define table zdemo { }"); err == nil {
		t.Error("a table definition accepted as a structure")
	}
	if got := StructureURL("/NS/S_DEMO"); got != "/sap/bc/adt/ddic/structures/%2Fns%2Fs_demo" {
		t.Errorf("namespace URL %s", got)
	}
}
