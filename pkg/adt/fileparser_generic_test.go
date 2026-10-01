package adt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// parseWithin runs ParseABAPFile on a goroutine and fails the test if it does
// not return in time. Before issue #237 was fixed, a generic .abap file sent
// ParseABAPFile and parseFromContent into endless mutual recursion; the stack
// grew until the runtime killed the whole process. The guard turns a
// regression back into that state into a quick, named failure (a stack
// overflow still ends the test binary, but a hang no longer can).
func parseWithin(t *testing.T, path string) (*ABAPFileInfo, error) {
	t.Helper()
	type outcome struct {
		info *ABAPFileInfo
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		info, err := ParseABAPFile(path)
		done <- outcome{info, err}
	}()
	select {
	case o := <-done:
		return o.info, o.err
	case <-time.After(5 * time.Second):
		t.Fatalf("ParseABAPFile(%s) did not return within 5s", filepath.Base(path))
		return nil, nil
	}
}

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return path
}

// Every generic .abap file whose content named a type recursed before the
// fix. Each of these must now come back with that type and its name.
func TestAGenericABAPFileIsTypedFromItsContent(t *testing.T) {
	cases := []struct {
		file, content string
		wantType      CreatableObjectType
		wantName      string
	}{
		{"zdemo.abap", "*& Report ZDEMO\nREPORT zdemo.\nWRITE 'hi'.\n", ObjectTypeProgram, "ZDEMO"},
		{"zdemo2.abap", "PROGRAM zdemo2 MESSAGE-ID zz.\n", ObjectTypeProgram, "ZDEMO2"},
		{"zcl_demo.abap", "CLASS zcl_demo DEFINITION\n  PUBLIC\n  FINAL\n  CREATE PUBLIC.\nENDCLASS.\nCLASS zcl_demo IMPLEMENTATION.\nENDCLASS.\n", ObjectTypeClass, "ZCL_DEMO"},
		{"zif_demo.abap", "\"! An interface\nINTERFACE zif_demo PUBLIC.\nENDINTERFACE.\n", ObjectTypeInterface, "ZIF_DEMO"},
		{"zdemo_fg.abap", "FUNCTION-POOL zdemo_fg.\n", ObjectTypeFunctionGroup, "ZDEMO_FG"},
		{"z_demo_func.abap", "FUNCTION z_demo_func.\nENDFUNCTION.\n", ObjectTypeFunctionMod, "Z_DEMO_FUNC"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			info, err := parseWithin(t, writeFixture(t, tc.file, tc.content))
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			if info.ObjectType != tc.wantType || info.ObjectName != tc.wantName {
				t.Errorf("got %s %s, want %s %s", info.ObjectType, info.ObjectName, tc.wantType, tc.wantName)
			}
		})
	}
}

// The exact file from issue #237: abapGit's own name for a function module.
func TestAnAbapGitFunctionModuleFileCarriesItsGroup(t *testing.T) {
	path := writeFixture(t, "zrepro.fugr.z_repro_func.abap", "FUNCTION z_repro_func.\nENDFUNCTION.\n")
	info, err := parseWithin(t, path)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if info.ObjectType != ObjectTypeFunctionMod || info.ObjectName != "Z_REPRO_FUNC" || info.ParentName != "ZREPRO" {
		t.Errorf("got %s %s in group %q, want FUGR/FF Z_REPRO_FUNC in ZREPRO", info.ObjectType, info.ObjectName, info.ParentName)
	}

	ns := writeFixture(t, "#aif#util.fugr.#aif#func.abap", "FUNCTION /aif/func.\nENDFUNCTION.\n")
	info, err = parseWithin(t, ns)
	if err != nil {
		t.Fatalf("parsing the namespaced module: %v", err)
	}
	if info.ObjectName != "/AIF/FUNC" || info.ParentName != "/AIF/UTIL" {
		t.Errorf("namespaced: got %s in %q", info.ObjectName, info.ParentName)
	}
}

// The group's own includes share the {group}.fugr.{x}.abap pattern. They are
// not modules, and saying so beats deploying them as something else.
func TestAFunctionGroupIncludeIsRefusedClearly(t *testing.T) {
	path := writeFixture(t, "zrepro.fugr.lzreprotop.abap", "FUNCTION-POOL zrepro.\nDATA gv_x TYPE i.\n")
	_, err := parseWithin(t, path)
	if err == nil || !strings.Contains(err.Error(), "function group ZREPRO") {
		t.Fatalf("expected a refusal naming the group, got %v", err)
	}
}

// A file opening with a local class could be a global class written without
// PUBLIC or a program include; neither guess is safe, so it is an error that
// says how to name the file.
func TestALocalClassInAGenericFileIsAnError(t *testing.T) {
	path := writeFixture(t, "zdemo_cls.abap", "CLASS lcl_helper DEFINITION.\nENDCLASS.\n")
	_, err := parseWithin(t, path)
	if err == nil || !strings.Contains(err.Error(), ".incl.abap") {
		t.Fatalf("expected an error pointing at the suffixes, got %v", err)
	}
}

// Before issue #235 an include was exported as {name}.abap. Such a file holds
// no statement naming its type, and it must still read back as the include.
func TestAnOldIncludeExportStillReadsBack(t *testing.T) {
	path := writeFixture(t, "zdemo_top.abap", "*&---------------------------------------------------------------------*\n*& Include ZDEMO_TOP\nDATA gv_count TYPE i.\n")
	info, err := parseWithin(t, path)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if info.ObjectType != ObjectTypeInclude || info.ObjectName != "ZDEMO_TOP" {
		t.Errorf("got %s %s, want PROG/I ZDEMO_TOP", info.ObjectType, info.ObjectName)
	}
}

func TestAGenericFileThatIsNothingIsAnError(t *testing.T) {
	for name, content := range map[string]string{
		"empty.abap":          "",
		"comments.abap":       "* only a comment\n\" and another\n",
		"zfoo.something.abap": "DATA x TYPE i.\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseWithin(t, writeFixture(t, name, content)); err == nil {
				t.Error("expected an error, got a type")
			}
		})
	}
}

// Export then import: every name ExportToFile writes must parse back as the
// type and name it was written for.
func TestExportedFileNamesParseBackAsTheSameObject(t *testing.T) {
	cases := []struct {
		objType       CreatableObjectType
		name, parent  string
		content       string
		wantFile      string
		wantParentOut string
	}{
		{ObjectTypeInclude, "ZDEMO_TOP", "", "DATA gv_count TYPE i.\n", "zdemo_top.incl.abap", ""},
		{ObjectTypeInclude, "/DMO/DEMO_TOP", "", "DATA gv_count TYPE i.\n", "#dmo#demo_top.incl.abap", ""},
		{ObjectTypeProgram, "ZDEMO", "", "REPORT zdemo.\n", "zdemo.prog.abap", ""},
		{ObjectTypeClass, "ZCL_DEMO", "", "CLASS zcl_demo DEFINITION PUBLIC.\nENDCLASS.\n", "zcl_demo.clas.abap", ""},
		{ObjectTypeInterface, "ZIF_DEMO", "", "INTERFACE zif_demo PUBLIC.\nENDINTERFACE.\n", "zif_demo.intf.abap", ""},
		{ObjectTypeFunctionGroup, "ZDEMO_FG", "", "FUNCTION-POOL zdemo_fg.\n", "zdemo_fg.fugr.abap", ""},
		{ObjectTypeFunctionMod, "Z_DEMO_CALL", "ZDEMO_FG", "FUNCTION z_demo_call.\nENDFUNCTION.\n", "zdemo_fg.fugr.z_demo_call.abap", "ZDEMO_FG"},
		{ObjectTypeFunctionMod, "/AIF/FUNC", "/AIF/UTIL", "FUNCTION /aif/func.\nENDFUNCTION.\n", "#aif#util.fugr.#aif#func.abap", "/AIF/UTIL"},
	}
	for _, tc := range cases {
		t.Run(tc.wantFile, func(t *testing.T) {
			dir := t.TempDir()
			path, err := ExportFilePath(tc.objType, tc.name, tc.parent, dir)
			if err != nil {
				t.Fatalf("export path: %v", err)
			}
			if filepath.Base(path) != tc.wantFile {
				t.Fatalf("exported as %s, want %s", filepath.Base(path), tc.wantFile)
			}
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			info, err := parseWithin(t, path)
			if err != nil {
				t.Fatalf("parsing the export: %v", err)
			}
			if info.ObjectType != tc.objType || info.ObjectName != tc.name || info.ParentName != tc.wantParentOut {
				t.Errorf("read back %s %s (parent %q), wrote %s %s (parent %q)",
					info.ObjectType, info.ObjectName, info.ParentName, tc.objType, tc.name, tc.wantParentOut)
			}
		})
	}
}

// An explicit file path is honoured; for an include that includes the bare
// {name}.abap it used to be written as.
func TestExportFilePathKeepsAnExplicitFile(t *testing.T) {
	for _, tc := range []struct {
		objType CreatableObjectType
		out     string
	}{
		{ObjectTypeInclude, "/x/zdemo_top.incl.abap"},
		{ObjectTypeInclude, "/x/zdemo_top.abap"},
		{ObjectTypeProgram, "/x/zdemo.prog.abap"},
		{ObjectTypeProgram, "/x/ZDEMO.PROG.ABAP"},
		{ObjectTypeFunctionMod, "/x/zfg.fugr.z_fm.abap"},
		{ObjectTypeFunctionMod, "/x/z_fm.func.abap"},
	} {
		got, err := ExportFilePath(tc.objType, "ZDEMO", "", tc.out)
		if err != nil || got != tc.out {
			t.Errorf("%s: got %s (%v), want the path as given", tc.out, got, err)
		}
	}
}

// An include written to zx.prog.abap would read back as a program, and
// deploying it would then create or overwrite program ZX.
func TestAnIncludeIsNotExportedUnderAnotherTypesName(t *testing.T) {
	for _, out := range []string{"/x/zx.prog.abap", "/x/zx.clas.abap", "/x/zfg.fugr.zx.abap"} {
		if got, err := ExportFilePath(ObjectTypeInclude, "ZX", "", out); err == nil {
			t.Errorf("%s: exported to %s, want a refusal", out, got)
		}
	}
}

// fugrMember used to slice past its own bounds on {group}.fugr.abap in any
// case but lower ("slice bounds out of range [9:8]"): a panic, the same
// process-ending class of failure as issue #237.
func TestFugrMemberDoesNotPanicOnMixedCase(t *testing.T) {
	for _, name := range []string{"ZFG.FUGR.ABAP", "zfg.Fugr.abap", "zfg.fugr.ABAP", "zfg.fugr.abap", ".fugr.abap", "x.fugr..abap"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("fugrMember(%q) panicked: %v", name, r)
				}
			}()
			if _, _, ok := fugrMember(name); ok {
				t.Errorf("fugrMember(%q) found a member where there is none", name)
			}
		}()
	}
	if g, m, ok := fugrMember("ZFG.FUGR.Z_FM.ABAP"); !ok || g != "ZFG" || m != "Z_FM" {
		t.Errorf("upper-case member file: got %q %q %v", g, m, ok)
	}
}

// Suffixes are case-blind everywhere, so an upper-case file name is the same
// file: the group file reads as the group (and does not panic on the way).
func TestUpperCaseSuffixesAreReadLikeLowerCase(t *testing.T) {
	cases := []struct {
		file, content string
		wantType      CreatableObjectType
		wantName      string
	}{
		{"ZFG.FUGR.ABAP", "FUNCTION-POOL zfg.\n", ObjectTypeFunctionGroup, "ZFG"},
		{"zfg.Fugr.abap", "FUNCTION-POOL zfg.\n", ObjectTypeFunctionGroup, "ZFG"},
		{"ZCL_UP.CLAS.ABAP", "CLASS zcl_up DEFINITION PUBLIC.\nENDCLASS.\n", ObjectTypeClass, "ZCL_UP"},
		{"ZUP_TOP.INCL.ABAP", "DATA x TYPE i.\n", ObjectTypeInclude, "ZUP_TOP"},
		{"ZUP.ABAP", "REPORT zup.\n", ObjectTypeProgram, "ZUP"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			info, err := parseWithin(t, writeFixture(t, tc.file, tc.content))
			if err != nil {
				t.Fatalf("parsing: %v", err)
			}
			if info.ObjectType != tc.wantType || info.ObjectName != tc.wantName {
				t.Errorf("got %s %s, want %s %s", info.ObjectType, info.ObjectName, tc.wantType, tc.wantName)
			}
		})
	}
}

// A TOP include opens with its main program's statement. Read by content
// alone, each of these became the main program or group, and DeployFromFile
// then updated the real object with the include's source. Only a file named
// for the object its statement opens may be typed from content.
func TestAnIncludeOpeningWithItsMainProgramIsRefused(t *testing.T) {
	cases := []struct{ file, content, names string }{
		{"mzfootop.abap", "PROGRAM sapmzfoo MESSAGE-ID zz.\nDATA x TYPE i.\n", "SAPMZFOO"},
		{"zrep_top.abap", "REPORT zrep.\nDATA x TYPE i.\n", "ZREP"},
		{"lzfgtop.abap", "FUNCTION-POOL zfg MESSAGE-ID zz.\nDATA x TYPE i.\n", "ZFG"},
		{"zother.abap", "FUNCTION z_fm.\nENDFUNCTION.\n", "Z_FM"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			info, err := parseWithin(t, writeFixture(t, tc.file, tc.content))
			if err == nil {
				t.Fatalf("read as %s %s, want a refusal", info.ObjectType, info.ObjectName)
			}
			if !strings.Contains(err.Error(), tc.names) || !strings.Contains(err.Error(), ".incl.abap") {
				t.Errorf("the refusal should name %s and the .incl.abap way out, got: %v", tc.names, err)
			}
		})
	}
}

// The abapGit module file must hold the module its name says.
func TestAnAbapGitModuleFileHoldingAnotherModuleIsRefused(t *testing.T) {
	path := writeFixture(t, "zfg.fugr.z_one.abap", "FUNCTION z_two.\nENDFUNCTION.\n")
	if info, err := parseWithin(t, path); err == nil {
		t.Fatalf("read as %s %s, want a refusal", info.ObjectType, info.ObjectName)
	}
}

// Only CLASS <n> DEFINITION PUBLIC and INTERFACE <n> PUBLIC declare a global
// object. A PUBLIC elsewhere in the statement (CREATE PUBLIC), or a forward
// declaration, does not, even when the name matches the file.
func TestOnlyAGlobalDeclarationTypesAClassOrInterface(t *testing.T) {
	cases := []struct{ file, content string }{
		{"lcl_app.abap", "CLASS lcl_app DEFINITION CREATE PUBLIC.\nENDCLASS.\n"},
		{"lcl_app2.abap", "CLASS lcl_app2 DEFINITION FINAL\n  CREATE PUBLIC.\nENDCLASS.\n"},
		{"zcl_x.abap", "CLASS zcl_x DEFINITION DEFERRED PUBLIC.\n"},
		{"zcl_y.abap", "CLASS zcl_y DEFINITION PUBLIC LOAD.\n"},
		{"zif_x.abap", "INTERFACE zif_x DEFERRED PUBLIC.\n"},
		{"zif_y.abap", "INTERFACE zif_y LOAD.\n"},
		{"lif_z.abap", "INTERFACE lif_z.\nENDINTERFACE.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			info, err := parseWithin(t, writeFixture(t, tc.file, tc.content))
			if err == nil {
				t.Fatalf("read as %s %s, want a refusal", info.ObjectType, info.ObjectName)
			}
			if !strings.Contains(err.Error(), ".incl.abap") {
				t.Errorf("the refusal should say how to name the file, got: %v", err)
			}
		})
	}
}

// CLASS: lcl_a DEFINITION DEFERRED, lcl_b ... declares local classes in a
// chain. It is neither the global class nor safe to guess at.
func TestAChainedClassStatementIsRefused(t *testing.T) {
	for name, content := range map[string]string{
		"zchain.abap":  "CLASS: zchain DEFINITION DEFERRED, lcl_b DEFINITION DEFERRED.\n",
		"zchain2.abap": "CLASS : zchain2 DEFINITION DEFERRED.\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseWithin(t, writeFixture(t, name, content))
			if err == nil || !strings.Contains(err.Error(), "chained") {
				t.Fatalf("expected a refusal naming the chained statement, got %v", err)
			}
		})
	}
}

// Windows editors put a byte order mark before the first statement. It is
// not part of REPORT, and the file is still the program.
func TestAByteOrderMarkDoesNotHideTheFirstStatement(t *testing.T) {
	info, err := parseWithin(t, writeFixture(t, "zbom.abap", "\uFEFFREPORT zbom.\nWRITE 'x'.\n"))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if info.ObjectType != ObjectTypeProgram || info.ObjectName != "ZBOM" {
		t.Errorf("got %s %s, want PROG/P ZBOM", info.ObjectType, info.ObjectName)
	}
}

// The line scan reads one line at a time; a class statement split before
// DEFINITION escaped it, and the file was refused for having no name.
func TestAClassStatementSplitAcrossLinesStillNamesTheClass(t *testing.T) {
	path := writeFixture(t, "zcl_split.clas.abap", "CLASS zcl_split\n  DEFINITION\n  PUBLIC.\nENDCLASS.\nCLASS zcl_split IMPLEMENTATION.\nENDCLASS.\n")
	info, err := parseWithin(t, path)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if info.ObjectName != "ZCL_SPLIT" {
		t.Errorf("got %q, want ZCL_SPLIT", info.ObjectName)
	}

	_, err = parseWithin(t, writeFixture(t, "zcl_none.clas.abap", "* nothing here\n"))
	if err == nil || !strings.Contains(err.Error(), "CLASS <name> DEFINITION") {
		t.Errorf("the error should name the statement it looked for, got %v", err)
	}
}

// Lowercasing can grow a name's bytes (Ⱥ is 2 bytes, ⱥ is 3): positions found
// in the lowered name must not be used to cut the original.
func TestFugrMemberMultibyteDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked: %v", r)
		}
	}()
	_, _, _ = fugrMember("ȺȺȺȺȺȺȺȺ.fugr.x.abap")
	_, _, _ = fugrMember("ȺȺȺȺȺȺȺȺ.FUGR.X.ABAP")
}
