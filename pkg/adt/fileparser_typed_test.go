package adt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A typed file is named by its file name. These cases each took the name
// from the content before, and so deployed one object's source into another.

// writeFiles puts several files in one directory and returns it.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return dir
}

func progXML(name, subc string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<abapGit version="v1.0.0" serializer="LCL_OBJECT_PROG" serializer_version="v1.0.0">
 <asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0">
  <asx:values>
   <PROGDIR>
    <NAME>` + name + `</NAME>
    <SUBC>` + subc + `</SUBC>
    <RLOAD>E</RLOAD>
   </PROGDIR>
  </asx:values>
 </asx:abap>
</abapGit>
`
}

func wantRefused(t *testing.T, path string, mustMention ...string) {
	t.Helper()
	info, err := parseWithin(t, path)
	if err == nil {
		t.Fatalf("%s was accepted as %s %s; it must be refused", filepath.Base(path), info.ObjectType, info.ObjectName)
	}
	for _, m := range mustMention {
		if !strings.Contains(err.Error(), m) {
			t.Errorf("the error should mention %q, got: %v", m, err)
		}
	}
}

func wantObject(t *testing.T, path string, kind CreatableObjectType, name string) *ABAPFileInfo {
	t.Helper()
	info, err := parseWithin(t, path)
	if err != nil {
		t.Fatalf("parsing %s: %v", filepath.Base(path), err)
	}
	if info.ObjectType != kind || info.ObjectName != name {
		t.Fatalf("%s read as %s %s, want %s %s", filepath.Base(path), info.ObjectType, info.ObjectName, kind, name)
	}
	return info
}

// abapGit exports a report's TOP include as zrep_top.prog.abap, and it opens
// with PROGRAM zrep. It was read as PROG ZREP and deployed over the report.
func TestATopIncludeProgFileIsNotTheMainProgram(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"zrep.prog.abap":     "REPORT zrep.\nINCLUDE zrep_top.\nSTART-OF-SELECTION.\n  WRITE gv_x.\n",
		"zrep_top.prog.abap": "PROGRAM zrep.\nDATA gv_x TYPE i.\n",
	})
	wantRefused(t, filepath.Join(dir, "zrep_top.prog.abap"), "ZREP_TOP", "ZREP", "SUBC", "zrep_top.incl.abap")
	wantObject(t, filepath.Join(dir, "zrep.prog.abap"), ObjectTypeProgram, "ZREP")
}

// With the .prog.xml abapGit writes beside it (SUBC I) the same file is the
// include it is named for, whatever statement it opens with.
func TestAProgFileMarkedAsAnIncludeIsThatInclude(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"zrep_top.prog.abap":   "PROGRAM zrep.\nDATA gv_x TYPE i.\n",
		"zrep_top.prog.xml":    progXML("ZREP_TOP", "I"),
		"zrep_f01.prog.abap":   "FORM do_it.\nENDFORM.\n",
		"zrep_f01.prog.xml":    progXML("ZREP_F01", "I"),
		"sapmzdemo.prog.abap":  "PROGRAM sapmzdemo.\n",
		"sapmzdemo.prog.xml":   progXML("SAPMZDEMO", "M"),
		"mzdemotop.prog.abap":  "PROGRAM sapmzdemo.\nDATA ok_code TYPE sy-ucomm.\n",
		"mzdemotop.prog.xml":   progXML("MZDEMOTOP", "I"),
		"#dmo#x_top.prog.abap": "PROGRAM /dmo/x.\n",
		"#dmo#x_top.prog.xml":  progXML("/DMO/X_TOP", "I"),
	})
	wantObject(t, filepath.Join(dir, "zrep_top.prog.abap"), ObjectTypeInclude, "ZREP_TOP")
	wantObject(t, filepath.Join(dir, "zrep_f01.prog.abap"), ObjectTypeInclude, "ZREP_F01")
	wantObject(t, filepath.Join(dir, "mzdemotop.prog.abap"), ObjectTypeInclude, "MZDEMOTOP")
	wantObject(t, filepath.Join(dir, "#dmo#x_top.prog.abap"), ObjectTypeInclude, "/DMO/X_TOP")
	wantObject(t, filepath.Join(dir, "sapmzdemo.prog.abap"), ObjectTypeProgram, "SAPMZDEMO")
}

// A .prog.xml that does not say SUBC I does not excuse a program statement
// for another program, and one that names another program is not trusted.
func TestAProgFileWhoseXMLDisagreesIsRefused(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"zrep_top.prog.abap": "PROGRAM zrep.\n",
		"zrep_top.prog.xml":  progXML("ZREP_TOP", "1"),
		"zother.prog.abap":   "REPORT zother.\n",
		"zother.prog.xml":    progXML("ZREP", "I"),
	})
	wantRefused(t, filepath.Join(dir, "zrep_top.prog.abap"), "ZREP_TOP", "ZREP")
	wantRefused(t, filepath.Join(dir, "zother.prog.abap"), "zother.prog.xml", "ZREP")
}

// A .prog.abap without an introductory statement and without a .prog.xml
// is an include nobody marked: it is refused, not deployed as a program.
func TestAProgFileWithoutAProgramStatementIsRefused(t *testing.T) {
	path := writeFixture(t, "zrep_f01.prog.abap", "FORM do_it.\n  WRITE 'x.'.\nENDFORM.\n")
	wantRefused(t, path, "REPORT or PROGRAM", "zrep_f01.incl.abap")
}

// A program file holding another program's source is refused, namespaces
// included; the file name decides, with # for /.
func TestAProgFileNamedForAnotherProgramIsRefused(t *testing.T) {
	wantRefused(t, writeFixture(t, "zone.prog.abap", "REPORT ztwo.\n"), "ZONE", "ZTWO")
	wantRefused(t, writeFixture(t, "#dmo#one.prog.abap", "REPORT /dmo/two.\n"), "/DMO/ONE", "/DMO/TWO")
	wantObject(t, writeFixture(t, "#dmo#one.prog.abap", "REPORT /dmo/one LINE-SIZE 255.\n"), ObjectTypeProgram, "/DMO/ONE")
}

// A forward or local declaration before the global class was taken for it:
// CLASS lcl DEFINITION DEFERRED named the class LCL.
func TestAClassFileSkipsForwardAndLocalDeclarations(t *testing.T) {
	cases := map[string]string{
		"deferred": "CLASS lcl_helper DEFINITION DEFERRED.\nCLASS zcl_main DEFINITION PUBLIC CREATE PUBLIC.\n  PUBLIC SECTION.\nENDCLASS.\nCLASS zcl_main IMPLEMENTATION.\nENDCLASS.\n",
		"load":     "CLASS zcl_other DEFINITION LOAD.\nCLASS zcl_main DEFINITION PUBLIC.\nENDCLASS.\n",
		"chained":  "CLASS: lcl_a DEFINITION DEFERRED, lcl_b DEFINITION DEFERRED.\nCLASS zcl_main DEFINITION PUBLIC FINAL.\nENDCLASS.\n",
		"local":    "* a comment with CLASS zcl_fake DEFINITION PUBLIC.\nCLASS lcl_local DEFINITION.\nENDCLASS.\n\"! doc\nCLASS zcl_main\n  DEFINITION\n  PUBLIC.\nENDCLASS.\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			info := wantObject(t, writeFixture(t, "zcl_main.clas.abap", src), ObjectTypeClass, "ZCL_MAIN")
			if info.ClassIncludeType != ClassIncludeMain {
				t.Errorf("include type %q, want main", info.ClassIncludeType)
			}
		})
	}
}

// A class file holding another global class, or only local classes, is
// refused rather than deployed under either name.
func TestAClassFileNamedForAnotherClassIsRefused(t *testing.T) {
	wantRefused(t, writeFixture(t, "zcl_one.clas.abap", "CLASS zcl_two DEFINITION PUBLIC.\nENDCLASS.\n"), "ZCL_ONE", "ZCL_TWO")
	wantRefused(t, writeFixture(t, "zcl_one.clas.abap", "CLASS lcl DEFINITION DEFERRED.\nCLASS lcl DEFINITION.\nENDCLASS.\n"), "ZCL_ONE")
	wantRefused(t, writeFixture(t, "#dmo#cl_one.clas.abap", "CLASS /dmo/cl_two DEFINITION PUBLIC.\nENDCLASS.\n"), "/DMO/CL_ONE", "/DMO/CL_TWO")
}

func TestAnInterfaceFileIsNamedByItsFile(t *testing.T) {
	wantObject(t, writeFixture(t, "zif_main.intf.abap", "INTERFACE zif_other DEFERRED.\nINTERFACE zif_main PUBLIC.\nENDINTERFACE.\n"), ObjectTypeInterface, "ZIF_MAIN")
	wantRefused(t, writeFixture(t, "zif_one.intf.abap", "INTERFACE zif_two PUBLIC.\nENDINTERFACE.\n"), "ZIF_ONE", "ZIF_TWO")
}

// A function group's main program holds INCLUDE statements; its
// FUNCTION-POOL is in the TOP include. The file names the group.
func TestAFunctionGroupFileIsNamedByItsFile(t *testing.T) {
	wantObject(t, writeFixture(t, "zdemo_fg.fugr.abap", "*****\n  INCLUDE lzdemo_fgtop.\n  INCLUDE lzdemo_fguxx.\n"), ObjectTypeFunctionGroup, "ZDEMO_FG")
	wantRefused(t, writeFixture(t, "zdemo_fg.fugr.abap", "FUNCTION-POOL zother_fg.\n"), "ZDEMO_FG", "ZOTHER_FG")
	wantRefused(t, writeFixture(t, "zdemo_fg.fugr.abap", "REPORT zdemo_fg.\n"), "ZDEMO_FG")
}

func TestAFunctionModuleFuncFileIsNamedByItsFile(t *testing.T) {
	info := wantObject(t, writeFixture(t, "zdemo_fg.fugr.z_one.func.abap", "FUNCTION z_one.\nENDFUNCTION.\n"), ObjectTypeFunctionMod, "Z_ONE")
	if info.ParentName != "ZDEMO_FG" {
		t.Errorf("parent %q", info.ParentName)
	}
	wantRefused(t, writeFixture(t, "zdemo_fg.fugr.z_one.func.abap", "FUNCTION z_two.\nENDFUNCTION.\n"), "Z_ONE", "Z_TWO")
	wantRefused(t, writeFixture(t, "z_one.func.abap", "FUNCTION z_two.\nENDFUNCTION.\n"), "Z_ONE", "Z_TWO")
}

func TestRAPSourcesAreNamedByTheirFile(t *testing.T) {
	wantObject(t, writeFixture(t, "zi_travel.ddls.asddls", "@EndUserText.label: 'define view entity zi_fake'\n// define view entity zi_comment\n/* define view zi_block */\ndefine root view entity ZI_Travel\n  as select from ztravel { key id }\n"), ObjectTypeDDLS, "ZI_TRAVEL")
	wantObject(t, writeFixture(t, "zi_travel_ext.ddls.asddls", "extend view entity ZI_Travel with { ztravel.x }\n"), ObjectTypeDDLS, "ZI_TRAVEL_EXT")
	wantRefused(t, writeFixture(t, "zi_one.ddls.asddls", "define view entity ZI_Two as select from t { key a }\n"), "ZI_ONE", "ZI_TWO")
	wantObject(t, writeFixture(t, "zi_travel.bdef.asbdef", "managed implementation in class zbp_i_travel unique;\nstrict ( 2 );\ndefine behavior for ZI_Travel alias Travel\n{ create; }\ndefine behavior for ZI_Booking alias Booking\n{ }\n"), ObjectTypeBDEF, "ZI_TRAVEL")
	wantRefused(t, writeFixture(t, "zi_one.bdef.asbdef", "managed;\ndefine behavior for ZI_Two\n{ }\n"), "ZI_ONE", "ZI_TWO")
	wantObject(t, writeFixture(t, "zui_travel.srvd.srvdsrv", "@EndUserText.label: 'x'\ndefine service ZUI_Travel {\n  expose ZI_Travel;\n}\n"), ObjectTypeSRVD, "ZUI_TRAVEL")
	wantRefused(t, writeFixture(t, "zui_one.srvd.srvdsrv", "define service ZUI_Two { expose ZI_X; }\n"), "ZUI_ONE", "ZUI_TWO")
}

// A file name that is not an object name is refused, not guessed from.
func TestATypedFileNeedsAnObjectNameAsItsStem(t *testing.T) {
	wantRefused(t, writeFixture(t, "z rep.prog.abap", "REPORT zrep.\n"), "not an object name")
	wantRefused(t, writeFixture(t, ".clas.abap", "CLASS zcl_x DEFINITION PUBLIC.\nENDCLASS.\n"), "not an object name")
}

// DeployFromFile must stop at the parse: no request is made for the TOP
// include, so nothing can be created, locked or written. The client has no
// transport; any request would panic.
func TestDeployFromFileRefusesATopIncludeBeforeAnyRequest(t *testing.T) {
	path := writeFixture(t, "zrep_top.prog.abap", "PROGRAM zrep.\n")
	c := &Client{}
	_, err := c.DeployFromFile(context.Background(), path, "$TMP", "")
	if err == nil || !strings.Contains(err.Error(), "ZREP_TOP") {
		t.Fatalf("want a refusal naming ZREP_TOP, got %v", err)
	}
}

// The name may follow on the next line; it was read as ENTITY.
func TestADDLDefinitionSplitAcrossLinesIsRead(t *testing.T) {
	wantObject(t, writeFixture(t, "zi_split.ddls.asddls", "define root view entity\n  ZI_Split\n  as select from t { key a }\n"), ObjectTypeDDLS, "ZI_SPLIT")
	wantRefused(t, writeFixture(t, "zi_split.ddls.asddls", "define root view entity\n  ZI_Other as select from t { key a }\n"), "ZI_SPLIT", "ZI_OTHER")
	wantRefused(t, writeFixture(t, "zi_split.ddls.asddls", "define root view entity\n"), "could not find the name")
}

// CLASS x DEFINITION LOCAL FRIENDS grants friendship and declares nothing:
// it does not stand in for the class's own declaration.
func TestLocalFriendsIsNotADeclaration(t *testing.T) {
	err := func() error {
		_, err := parseWithin(t, writeFixture(t, "zcl_main.clas.abap", "CLASS zcl_main DEFINITION LOCAL FRIENDS ltcl_test.\n"))
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "does not declare ZCL_MAIN") {
		t.Fatalf("want a refusal for a missing declaration, got %v", err)
	}
	wantObject(t, writeFixture(t, "zcl_main.clas.abap", "CLASS zcl_main DEFINITION LOCAL FRIENDS ltcl_test.\nCLASS zcl_main DEFINITION PUBLIC.\nENDCLASS.\n"), ObjectTypeClass, "ZCL_MAIN")
}
