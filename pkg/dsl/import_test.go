package dsl

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

func writeImportDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// An abapGit export of a report with a TOP include and no .prog.xml: the
// include used to deploy as the report (walked after it, so it won), and a
// file that did not parse vanished without a word. Both must be reported.
func TestScanDirectoryReportsEveryFileItWillNotImport(t *testing.T) {
	dir := writeImportDir(t, map[string]string{
		"zrep.prog.abap":     "REPORT zrep.\nINCLUDE zrep_top.\n",
		"zrep_top.prog.abap": "PROGRAM zrep.\nDATA gv_x TYPE i.\n",
		"zcl_bad.clas.abap":  "* no class here\n",
		"zrep.prog.xml":      "<abapGit/>\n", // not a source: neither imported nor listed
	})
	files, skipped, err := ScanDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].ObjectType != adt.ObjectTypeProgram || files[0].ObjectName != "ZREP" ||
		filepath.Base(files[0].Path) != "zrep.prog.abap" {
		t.Fatalf("want only zrep.prog.abap as PROG ZREP, got %+v", files)
	}
	reasons := map[string]string{}
	for _, s := range skipped {
		reasons[filepath.Base(s.Path)] = s.Reason
	}
	if len(skipped) != 2 || reasons["zrep_top.prog.abap"] == "" || reasons["zcl_bad.clas.abap"] == "" {
		t.Fatalf("want zrep_top.prog.abap and zcl_bad.clas.abap skipped with reasons, got %+v", skipped)
	}
	if !strings.Contains(reasons["zrep_top.prog.abap"], "ZREP_TOP") {
		t.Errorf("reason for the TOP include should name it: %s", reasons["zrep_top.prog.abap"])
	}
}

// Execute returns the skipped files in its result and prints them.
func TestImportExecuteReturnsAndPrintsSkippedFiles(t *testing.T) {
	dir := writeImportDir(t, map[string]string{
		"zrep.prog.abap":     "REPORT zrep.\n",
		"zrep_top.prog.abap": "PROGRAM zrep.\n",
	})
	b, err := Import(nil).FromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	res, err := b.DryRun().Output(&out).Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalFiles != 2 || res.SuccessCount != 1 || res.SkippedCount != 1 || len(res.Skipped) != 1 {
		t.Fatalf("want 2 found, 1 imported, 1 skipped; got %+v", res)
	}
	if filepath.Base(res.Skipped[0].Path) != "zrep_top.prog.abap" || res.Skipped[0].Reason == "" {
		t.Errorf("skipped %+v", res.Skipped)
	}
	if !strings.Contains(out.String(), "zrep_top.prog.abap") || !strings.Contains(out.String(), "Skipped 1 file") {
		t.Errorf("the skipped file should be printed, got %q", out.String())
	}
}

// Two files that deploy to the same object would overwrite each other in
// whatever order they ran; neither is imported, and both are reported.
func TestImportRefusesTwoFilesForOneObject(t *testing.T) {
	dir := writeImportDir(t, map[string]string{
		"zrep.prog.abap": "REPORT zrep.\n",
		"zrep.incl.abap": "DATA x TYPE i.\n",
		"zok.prog.abap":  "REPORT zok.\n",
	})
	b, err := Import(nil).FromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	res, err := b.DryRun().Output(&out).Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.SuccessCount != 1 || res.Results[0].File.ObjectName != "ZOK" {
		t.Fatalf("only ZOK should be imported, got %+v", res.Results)
	}
	if res.SkippedCount != 2 {
		t.Fatalf("both ZREP files should be skipped, got %+v", res.Skipped)
	}
	for _, s := range res.Skipped {
		if !strings.Contains(s.Reason, "ZREP") {
			t.Errorf("reason should name the object: %s", s.Reason)
		}
	}
}

// zcl_a.abap (no include type) and zcl_a.clas.abap (main) both deploy to
// ZCL_A's main source.
func TestImportRefusesAPlainAndATypedFileForOneClass(t *testing.T) {
	dir := writeImportDir(t, map[string]string{
		"zcl_a.abap":      "CLASS zcl_a DEFINITION PUBLIC.\nENDCLASS.\nCLASS zcl_a IMPLEMENTATION.\nENDCLASS.\n",
		"zcl_a.clas.abap": "CLASS zcl_a DEFINITION PUBLIC.\nENDCLASS.\nCLASS zcl_a IMPLEMENTATION.\nENDCLASS.\n",
	})
	b, err := Import(nil).FromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.DryRun().Output(&bytes.Buffer{}).Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.SuccessCount != 0 || res.SkippedCount != 2 {
		t.Fatalf("both files should be refused, got %d imported, skipped %+v", res.SuccessCount, res.Skipped)
	}
}

// A file given twice is one file: imported once, not refused against itself.
func TestImportTakesAFileGivenTwiceOnce(t *testing.T) {
	dir := writeImportDir(t, map[string]string{"zrep.prog.abap": "REPORT zrep.\n"})
	p := filepath.Join(dir, "zrep.prog.abap")
	b, err := Import(nil).FromFiles(p, p, filepath.Join(dir, ".", "zrep.prog.abap"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	res, err := b.DryRun().Output(&out).Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalFiles != 1 || res.SuccessCount != 1 || res.SkippedCount != 0 || out.Len() != 0 {
		t.Fatalf("want one file imported once, got %+v, printed %q", res, out.String())
	}
}

// A new program's INCLUDE statements fail its syntax check until the
// includes exist, so includes are imported first.
func TestImportDeploysIncludesBeforeTheirProgram(t *testing.T) {
	dir := writeImportDir(t, map[string]string{
		"zrep.prog.abap":     "REPORT zrep.\nINCLUDE zrep_top.\n",
		"zrep_top.incl.abap": "DATA gv_x TYPE i.\n",
	})
	b, err := Import(nil).FromDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.DryRun().Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 || res.Results[0].File.ObjectName != "ZREP_TOP" || res.Results[1].File.ObjectName != "ZREP" {
		t.Fatalf("want ZREP_TOP then ZREP, got %+v", res.Results)
	}
}
