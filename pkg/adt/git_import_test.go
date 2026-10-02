package adt

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- a synthetic abapGit zip ------------------------------------------------

func dotAbapgitXML(start, logic string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0">
 <asx:values>
  <DATA>
   <MASTER_LANGUAGE>E</MASTER_LANGUAGE>
   <STARTING_FOLDER>` + start + `</STARTING_FOLDER>
   <FOLDER_LOGIC>` + logic + `</FOLDER_LOGIC>
  </DATA>
 </asx:values>
</asx:abap>`
}

// makeZip builds a zip of name -> content; an empty dot leaves .abapgit.xml out.
func makeZip(t *testing.T, dot string, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	add := func(n, c string) {
		f, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(c))
	}
	if dot != "" {
		add(".abapgit.xml", dot)
	}
	for n, c := range files {
		add(n, c)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func demoZip(t *testing.T, logic string) []byte {
	return makeZip(t, dotAbapgitXML("/src/", logic), map[string]string{
		"README.md":                      "not an object",
		"src/package.devc.xml":           "<x/>",
		"src/zdemo_report.prog.abap":     "REPORT zdemo_report.",
		"src/zdemo_report.prog.xml":      "<x/>",
		"src/sub/package.devc.xml":       "<x/>",
		"src/sub/zcl_demo.clas.abap":     "CLASS zcl_demo DEFINITION. ENDCLASS.",
		"src/sub/zcl_demo.clas.xml":      "<x/>",
		"src/sub/#demo#cl_ns.clas.abap":  "CLASS /demo/cl_ns DEFINITION. ENDCLASS.",
		"src/sub/deep/package.devc.xml":  "<x/>",
		"src/sub/deep/zdemo_if.intf.xml": "<x/>",
	})
}

func TestAnalyzeGitZipFolderLogics(t *testing.T) {
	for logic, want := range map[string][]string{
		"PREFIX": {"$ZDEMO", "$ZDEMO_SUB", "$ZDEMO_SUB_DEEP"},
		"MIXED":  {"$ZDEMO", "$ZDEMO_DEEP", "$ZDEMO_SUB"},
		"FULL":   {"$ZDEMO", "$DEEP", "$SUB"},
	} {
		plan, err := AnalyzeGitZip(demoZip(t, logic), "$zdemo")
		if err != nil {
			t.Fatalf("%s: %v", logic, err)
		}
		if strings.Join(plan.Packages, ",") != strings.Join(want, ",") {
			t.Errorf("%s: packages %v, want %v", logic, plan.Packages, want)
		}
	}
	plan, err := AnalyzeGitZip(demoZip(t, "PREFIX"), "$ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(plan.Objects))
	for _, o := range plan.Objects {
		got = append(got, o.Type+" "+o.Name+" "+o.Package)
	}
	want := []string{
		"CLAS /DEMO/CL_NS $ZDEMO_SUB", "CLAS ZCL_DEMO $ZDEMO_SUB",
		"DEVC $ZDEMO $ZDEMO", "DEVC $ZDEMO_SUB $ZDEMO_SUB", "DEVC $ZDEMO_SUB_DEEP $ZDEMO_SUB_DEEP",
		"INTF ZDEMO_IF $ZDEMO_SUB_DEEP", "PROG ZDEMO_REPORT $ZDEMO",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("objects:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	data := demoZip(t, "PREFIX")
	p2, _ := AnalyzeGitZip(data, "$ZDEMO")
	h := sha256.Sum256(data)
	if p2.SHA256 != hex.EncodeToString(h[:]) || p2.Size != len(data) {
		t.Error("the plan does not carry the zip's size and SHA-256")
	}
}

func TestAnalyzeGitZipRefuses(t *testing.T) {
	cases := map[string][]byte{
		"no .abapgit.xml":   makeZip(t, "", map[string]string{"src/zdemo.prog.abap": "x"}),
		"not a zip":         []byte("PK? no"),
		"bad folder logic":  makeZip(t, dotAbapgitXML("/src/", "FLAT"), nil),
		"bad start folder":  makeZip(t, dotAbapgitXML("src", "PREFIX"), nil),
		"dot dot":           makeZip(t, dotAbapgitXML("/src/", "PREFIX"), map[string]string{"src/../x.prog.abap": "x"}),
		"package too long":  makeZip(t, dotAbapgitXML("/src/", "PREFIX"), map[string]string{"src/averyveryverylongfoldername/x.prog.abap": "x"}),
		"bad package chars": makeZip(t, dotAbapgitXML("/src/", "FULL"), map[string]string{"src/a-b/x.prog.abap": "x"}),
		"empty":             {},
	}
	for name, data := range cases {
		if _, err := AnalyzeGitZip(data, "$ZDEMO"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := AnalyzeGitZip(demoZip(t, "PREFIX"), "$Z DEMO"); err == nil {
		t.Error("a package name with a blank accepted")
	}
	// Two .abapgit.xml at the root: which one abapGit reads is not defined.
	var two bytes.Buffer
	w := zip.NewWriter(&two)
	for _, n := range []string{".abapgit.xml", "./.abapgit.xml"} {
		f, _ := w.Create(n)
		_, _ = f.Write([]byte(dotAbapgitXML("/src/", "PREFIX")))
	}
	_ = w.Close()
	if _, err := AnalyzeGitZip(two.Bytes(), "$ZDEMO"); err == nil || !strings.Contains(err.Error(), "more than one .abapgit.xml") {
		t.Errorf("two .abapgit.xml: %v", err)
	}
	// Declared to unpack past 200 MB: refused from the directory alone.
	var bomb bytes.Buffer
	w = zip.NewWriter(&bomb)
	f, _ := w.Create(".abapgit.xml")
	_, _ = f.Write([]byte(dotAbapgitXML("/src/", "PREFIX")))
	raw, _ := w.CreateRaw(&zip.FileHeader{Name: "src/zdemo.prog.abap", Method: zip.Deflate, CompressedSize64: 2, UncompressedSize64: 300 << 20})
	_, _ = raw.Write([]byte{3, 0})
	_ = w.Close()
	if _, err := AnalyzeGitZip(bomb.Bytes(), "$ZDEMO"); err == nil || !strings.Contains(err.Error(), "200 MB") {
		t.Errorf("300 MB unpacked: %v", err)
	}

	// A declared size near 2^64 must not wrap the sum back under the limit.
	var wrap bytes.Buffer
	w = zip.NewWriter(&wrap)
	f, _ = w.Create(".abapgit.xml")
	_, _ = f.Write([]byte(dotAbapgitXML("/src/", "PREFIX")))
	f, _ = w.Create("src/zdemo_a.prog.abap")
	_, _ = f.Write(bytes.Repeat([]byte("x"), 100))
	raw, _ = w.CreateRaw(&zip.FileHeader{Name: "src/zdemo_b.prog.abap", Method: zip.Deflate, CompressedSize64: 2, UncompressedSize64: math.MaxUint64 - 49})
	_, _ = raw.Write([]byte{3, 0})
	_ = w.Close()
	if _, err := AnalyzeGitZip(wrap.Bytes(), "$ZDEMO"); err == nil || !strings.Contains(err.Error(), "200 MB") {
		t.Errorf("a size that wraps the sum: %v", err)
	}

	big := make([]byte, GitZipMaxBytes+1)
	if _, err := AnalyzeGitZip(big, "$ZDEMO"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("over the limit: %v", err)
	}
}

// --- gates -------------------------------------------------------------------

func TestCheckGitImportPolicy(t *testing.T) {
	cases := []struct {
		name      string
		opts      []Option
		pkg, tr   string
		overwrite bool
		wantError string
	}{
		{"read-only", []Option{WithReadOnly()}, "$ZDEMO", "", false, "read-only"},
		{"create disallowed", []Option{WithSafety(SafetyConfig{DisallowedOps: "C"})}, "$ZDEMO", "", false, "blocked"},
		{"activate disallowed", []Option{WithSafety(SafetyConfig{DisallowedOps: "A"})}, "$ZDEMO", "", false, "blocked"},
		// abapGit's delete_add deletes an object and creates it again.
		{"overwrite, delete disallowed", []Option{WithSafety(SafetyConfig{DisallowedOps: "D"})}, "$ZDEMO", "", true, "blocked"},
		{"no overwrite, delete disallowed", []Option{WithSafety(SafetyConfig{DisallowedOps: "D"})}, "$ZDEMO", "", false, ""},
		{"package outside whitelist", []Option{WithAllowedPackages("$ZOTHER")}, "$ZDEMO", "", false, "blocked by safety"},
		{"transportable without opt-in", nil, "ZDEMO", "TRXK900001", false, "transportable"},
		{"transportable, transport outside whitelist", []Option{WithAllowTransportableEdits(), WithAllowedTransports("ABCK*")}, "ZDEMO", "TRXK900001", false, "allowed transports"},
		{"local with a transport", nil, "$ZDEMO", "TRXK900001", false, "takes no transport"},
		{"malformed transport", []Option{WithAllowTransportableEdits()}, "ZDEMO", "TR-1", false, "not <SID>K"},
		{"local ok", []Option{WithAllowedPackages("$Z*")}, "$ZDEMO", "", true, ""},
		{"transportable ok", []Option{WithAllowTransportableEdits()}, "ZDEMO", "TRXK900001", false, ""},
	}
	for _, c := range cases {
		cl := NewClient("http://sap.invalid", "TESTUSER", "pw", c.opts...)
		err := cl.CheckGitImportPolicy(c.pkg, c.tr, c.overwrite)
		switch {
		case c.wantError == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.wantError != "" && (err == nil || !strings.Contains(err.Error(), c.wantError)):
			t.Errorf("%s: got %v, want %q", c.name, err, c.wantError)
		}
	}
}

// Every package the zip maps a file to is checked, not only the target: a
// subfolder must not carry objects into a package the server may not touch.
func TestCheckGitImportPlanChecksEveryPackage(t *testing.T) {
	plan, err := AnalyzeGitZip(demoZip(t, "PREFIX"), "$ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw", WithAllowedPackages("$ZDEMO", "$ZDEMO_SUB"))
	if err := cl.CheckGitImportPlan(plan); err == nil || !strings.Contains(err.Error(), "$ZDEMO_SUB_DEEP") {
		t.Errorf("a subpackage outside the whitelist passed: %v", err)
	}
	cl = NewClient("http://sap.invalid", "TESTUSER", "pw", WithAllowedPackages("$ZDEMO*"))
	if err := cl.CheckGitImportPlan(plan); err != nil {
		t.Errorf("all packages allowed, refused: %v", err)
	}
	full, _ := AnalyzeGitZip(demoZip(t, "FULL"), "$ZDEMO")
	if err := cl.CheckGitImportPlan(full); err == nil {
		t.Error("FULL logic maps to $SUB and $DEEP, outside $ZDEMO*; accepted")
	}
}

// abapGit refuses a repository whose packages mix local and transportable
// ones; so does vsp, before anything is sent, either way round.
func TestCheckGitImportPlanRefusesALocalTransportableMix(t *testing.T) {
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw", WithAllowTransportableEdits())
	for _, plan := range []*GitZipPlan{
		{Package: "ZDEMO", FolderLogic: GitFolderLogicFull, Packages: []string{"ZDEMO", "$ZDEMO_SUB"}},
		{Package: "$ZDEMO", FolderLogic: GitFolderLogicFull, Packages: []string{"$ZDEMO", "ZDEMO_SUB"}},
	} {
		if err := cl.CheckGitImportPlan(plan); err == nil || !strings.Contains(err.Error(), "mix of local and transportable") {
			t.Errorf("%v: %v", plan.Packages, err)
		}
	}
	// From a zip: FULL folder logic names the package after the folder, so
	// a folder "$sub" under a transportable target is a local package.
	data := makeZip(t, dotAbapgitXML("/src/", "FULL"), map[string]string{"src/$sub/zdemo.prog.abap": "REPORT zdemo."})
	plan, err := AnalyzeGitZip(data, "ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.CheckGitImportPlan(plan); err == nil || !strings.Contains(err.Error(), "$SUB") {
		t.Errorf("%v: %v", plan.Packages, err)
	}
	for _, ok := range []*GitZipPlan{
		{Package: "ZDEMO", Packages: []string{"ZDEMO", "ZDEMO_SUB"}},
		{Package: "$ZDEMO", Packages: []string{"$ZDEMO", "$ZDEMO_SUB"}},
	} {
		if err := cl.CheckGitImportPlan(ok); err != nil {
			t.Errorf("%v refused: %v", ok.Packages, err)
		}
	}
}

// --- the git domain, faked ----------------------------------------------------

type fakeGitWS struct {
	mu     sync.Mutex
	calls  []wsCall
	client string
	// assembled zip
	got []byte
	// package_objects answers, in turn (the last one repeats)
	contents []map[string]any
	// delete_repo
	repoErr *WSError
	// import_status answers, in turn
	status []map[string]any
	// beginPackage overrides the package begin reports.
	beginPackage string
	// commitErr fails the commit without an answer; commitRefusal answers
	// it with a refusal.
	commitErr     error
	commitRefusal *WSError
	// beginErr fails begin without an answer, beginRefusal answers it
	// with a refusal; abortErr fails abort without an answer.
	beginErr     error
	beginRefusal *WSError
	// beginNoID answers begin without an assembly id.
	beginNoID bool
	abortErr  error
	// statusErr fails import_status without an answer once the status
	// answers are used up.
	statusErr error
	// closes counts Close: the connection reset.
	closes int
}

func (f *fakeGitWS) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return nil
}

func (f *fakeGitWS) closed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

func (f *fakeGitWS) SendDomainRequest(_ context.Context, domain, action string, params map[string]any, _ time.Duration) (*WSResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := json.Marshal(params)
	var p map[string]any
	_ = json.Unmarshal(b, &p)
	f.calls = append(f.calls, wsCall{Action: action, Params: p})
	if domain != "git" {
		return &WSResponse{Success: false, Error: &WSError{Code: "UNKNOWN_DOMAIN", Message: "no"}}, nil
	}
	ok := func(v any) (*WSResponse, error) {
		raw, _ := json.Marshal(v)
		return &WSResponse{Success: true, Data: raw}, nil
	}
	switch action {
	case "import_zip":
		switch p["step"] {
		case "begin":
			if f.beginErr != nil {
				return nil, f.beginErr
			}
			if f.beginRefusal != nil {
				return &WSResponse{Success: false, Error: f.beginRefusal}, nil
			}
			pkg := p["package"].(string)
			if f.beginPackage != "" {
				pkg = f.beginPackage
			}
			id := "A1"
			if f.beginNoID {
				id = ""
			}
			return ok(map[string]any{"assembly_id": id, "package": pkg, "system": "XYZ", "client": orDefaultString(f.client, "001")})
		case "chunk":
			c, _ := base64.StdEncoding.DecodeString(p["chunk_b64"].(string))
			if int(p["offset"].(float64)) != len(f.got) {
				return &WSResponse{Success: false, Error: &WSError{Code: "INVALID_CHUNK", Message: "order"}}, nil
			}
			f.got = append(f.got, c...)
			return ok(map[string]any{"received": len(f.got)})
		case "commit":
			if f.commitErr != nil {
				return nil, f.commitErr
			}
			if f.commitRefusal != nil {
				return &WSResponse{Success: false, Error: f.commitRefusal}, nil
			}
			return ok(map[string]any{"status": "pending", "job": "ZVSP_GIT_IMPORT", "job_count": "12345678", "package": "$ZDEMO"})
		case "abort":
			if f.abortErr != nil {
				return nil, f.abortErr
			}
			return ok(map[string]any{"aborted": true})
		}
	case "import_status":
		if f.statusErr != nil && len(f.status) == 0 {
			return nil, f.statusErr
		}
		if len(f.status) == 0 {
			return ok(map[string]any{"outcome": "unknown"})
		}
		s := f.status[0]
		if len(f.status) > 1 || f.statusErr != nil {
			f.status = f.status[1:]
		}
		return ok(s)
	case "package_objects":
		if len(f.contents) == 0 {
			return ok(map[string]any{"package": p["package"], "exists": false})
		}
		c := f.contents[0]
		if len(f.contents) > 1 {
			f.contents = f.contents[1:]
		}
		return ok(c)
	case "delete_repo":
		if f.repoErr != nil {
			return &WSResponse{Success: false, Error: f.repoErr}, nil
		}
		return ok(map[string]any{"deleted": true, "key": "000000000001", "name": "demo", "package": p["package"]})
	}
	return &WSResponse{Success: false, Error: &WSError{Code: "GIT_ERROR", Message: "Unknown action: " + action}}, nil
}

func (f *fakeGitWS) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		a := c.Action
		if s, ok := c.Params["step"].(string); ok {
			a += ":" + s
		}
		out = append(out, a)
	}
	return out
}

func (f *fakeGitWS) params(action string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		a := c.Action
		if s, ok := c.Params["step"].(string); ok {
			a += ":" + s
		}
		if a == action {
			return c.Params
		}
	}
	return nil
}

func TestStartGitImportSendsTheZipInChunks(t *testing.T) {
	var big bytes.Buffer
	for i := 0; big.Len() < 3*gitUploadChunk; i++ {
		fmt.Fprintf(&big, "WRITE / 'line %d'.\n", i)
	}
	// Stored, not deflated, so the zip is as large as its content.
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, _ := w.Create(".abapgit.xml")
	_, _ = f.Write([]byte(dotAbapgitXML("/src/", "PREFIX")))
	h := &zip.FileHeader{Name: "src/zdemo_big.prog.abap", Method: zip.Store}
	f, _ = w.CreateHeader(h)
	_, _ = f.Write(big.Bytes())
	_ = w.Close()
	data := b.Bytes()

	ws := &fakeGitWS{}
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw", WithAllowedPackages("$ZDEMO"))
	started, err := cl.StartGitImport(context.Background(), ws, data, GitImportOptions{Package: "$zdemo", Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ws.got, data) {
		t.Fatalf("the system assembled %d bytes, sent %d", len(ws.got), len(data))
	}
	acts := ws.actions()
	if acts[0] != "import_zip:begin" || acts[len(acts)-1] != "import_zip:commit" || len(acts) < 5 {
		t.Errorf("actions %v", acts)
	}
	begin := ws.params("import_zip:begin")
	sum := sha256.Sum256(data)
	if begin["sha256"] != hex.EncodeToString(sum[:]) || int(begin["size"].(float64)) != len(data) {
		t.Errorf("begin does not declare the zip's size and SHA-256: %v", begin)
	}
	if begin["package"] != "$ZDEMO" || begin["packages"] != "$ZDEMO" || begin["overwrite"] != "true" || begin["repo_name"] != "$ZDEMO" {
		t.Errorf("begin params %v", begin)
	}
	if started.JobCount != "12345678" || started.Job != "ZVSP_GIT_IMPORT" {
		t.Errorf("started %+v", started)
	}
}

// The gates run before anything is sent.
func TestStartGitImportGatesBeforeIO(t *testing.T) {
	zipData := demoZip(t, "PREFIX")
	cases := map[string]struct {
		opts []Option
		imp  GitImportOptions
	}{
		"read-only":                  {[]Option{WithReadOnly()}, GitImportOptions{Package: "$ZDEMO"}},
		"target outside whitelist":   {[]Option{WithAllowedPackages("$ZOTHER*")}, GitImportOptions{Package: "$ZDEMO"}},
		"subpackage outside":         {[]Option{WithAllowedPackages("$ZDEMO", "$ZDEMO_SUB")}, GitImportOptions{Package: "$ZDEMO"}},
		"transportable, no opt-in":   {nil, GitImportOptions{Package: "ZDEMO", Transport: "TRXK900001"}},
		"transportable, bad request": {[]Option{WithAllowTransportableEdits(), WithAllowedTransports("ABCK*")}, GitImportOptions{Package: "ZDEMO", Transport: "TRXK900001"}},
		"control char in repo name":  {nil, GitImportOptions{Package: "$ZDEMO", RepoName: "a\nb"}},
	}
	for name, c := range cases {
		ws := &fakeGitWS{}
		cl := NewClient("http://sap.invalid", "TESTUSER", "pw", c.opts...)
		if _, err := cl.StartGitImport(context.Background(), ws, zipData, c.imp); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if n := len(ws.actions()); n != 0 {
			t.Errorf("%s: %d messages sent before the refusal", name, n)
		}
	}
}

// ZADT_VSP answering from another client, or reading another package, aborts
// before a byte of the zip is sent.
func TestStartGitImportAbortsOnMismatch(t *testing.T) {
	for name, ws := range map[string]*fakeGitWS{
		"client":  {client: "200"},
		"package": {beginPackage: "$ZOTHER"},
	} {
		cl := NewClient("http://sap.invalid", "TESTUSER", "pw", WithClient("001"))
		if _, err := cl.StartGitImport(context.Background(), ws, demoZip(t, "PREFIX"), GitImportOptions{Package: "$ZDEMO"}); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if got := strings.Join(ws.actions(), ","); got != "import_zip:begin,import_zip:abort" {
			t.Errorf("%s: actions %s", name, got)
		}
	}
}

// A commit without an answer may have started the job: StartGitImport says
// so and returns what it knows. A refusal is an answer: nothing started.
func TestStartGitImportCommitWithoutAnswer(t *testing.T) {
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw")
	ws := &fakeGitWS{commitErr: context.DeadlineExceeded}
	started, err := cl.StartGitImport(context.Background(), ws, demoZip(t, "PREFIX"), GitImportOptions{Package: "$ZDEMO"})
	var unconfirmed *GitImportUnconfirmedError
	if !errors.As(err, &unconfirmed) || started == nil || unconfirmed.Started != started {
		t.Fatalf("got %+v, %v", started, err)
	}
	for _, want := range []string{"may be running", "SM37", "ZVSP_GIT_IMPORT", "TESTUSER", "git_import_status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q: %v", want, err)
		}
	}
	if started.System != "XYZ" || started.Package != "$ZDEMO" || started.Job != "ZVSP_GIT_IMPORT" || started.JobCount != "" || started.Plan == nil {
		t.Errorf("what is known: %+v", started)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the cause is lost: %v", err)
	}

	ws = &fakeGitWS{commitRefusal: &WSError{Code: "CHECKSUM_MISMATCH", Message: "Nothing was imported."}}
	started, err = cl.StartGitImport(context.Background(), ws, demoZip(t, "PREFIX"), GitImportOptions{Package: "$ZDEMO"})
	var se *GitServiceError
	if started != nil || !errors.As(err, &se) || errors.As(err, &unconfirmed) {
		t.Errorf("a refused commit: %+v, %v", started, err)
	}
}

func TestGitImportStatusParsesTheResult(t *testing.T) {
	ws := &fakeGitWS{status: []map[string]any{
		{"job": "ZVSP_GIT_IMPORT", "job_count": "12345678", "job_found": true, "job_status": "R", "outcome": "pending"},
		{"job": "ZVSP_GIT_IMPORT", "job_count": "12345678", "job_found": true, "job_status": "F", "outcome": "done",
			"job_log": []string{"VSP package=$ZDEMO outcome=imported"},
			"result": map[string]any{
				"outcome": "imported_with_errors", "package": "$ZDEMO", "repo_key": "000000000007", "repo_name": "demo",
				"repo_created": true, "package_created": true, "info_count": 3,
				"log":       []map[string]any{{"type": "E", "text": "Syntax error", "obj_type": "PROG", "obj_name": "ZDEMO_REPORT"}},
				"tadir":     []map[string]any{{"pgmid": "R3TR", "object": "PROG", "obj_name": "ZDEMO_REPORT", "devclass": "$ZDEMO", "created": true}},
				"decisions": []map[string]any{{"obj_type": "PROG", "obj_name": "ZDEMO_REPORT", "action": "add", "decision": "Y"}},
			}},
	}}
	old := gitPollInterval
	gitPollInterval = time.Millisecond
	defer func() { gitPollInterval = old }()
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw")
	st, err := cl.WaitGitImport(context.Background(), ws, "12345678")
	if err != nil {
		t.Fatal(err)
	}
	r := st.Result
	if st.State != GitJobDone || r == nil || r.Outcome != GitImportedWithErrors || r.RepoKey != "000000000007" || !r.RepoCreated || !r.PackageCreated {
		t.Fatalf("status %+v result %+v", st, r)
	}
	if len(r.Log) != 1 || r.Log[0].Type != "E" || r.Log[0].ObjName != "ZDEMO_REPORT" {
		t.Errorf("log %+v", r.Log)
	}
	if len(r.Tadir) != 1 || !r.Tadir[0].Created || r.Tadir[0].DevClass != "$ZDEMO" {
		t.Errorf("tadir %+v", r.Tadir)
	}
	if n := len(ws.actions()); n != 2 {
		t.Errorf("%d status calls, want 2", n)
	}

	// "done" without a result is not believed.
	ws = &fakeGitWS{status: []map[string]any{{"outcome": "done", "job_found": true}}}
	st, err = cl.GitImportStatus(context.Background(), ws, "12345678")
	if err != nil || st.State != GitJobUnknown {
		t.Errorf("done without a result: %+v %v", st, err)
	}
	if _, err := cl.GitImportStatus(context.Background(), ws, "1234; DROP"); err == nil {
		t.Error("a job number that is not one accepted")
	}
}

func TestGitCallNamesAMissingGitService(t *testing.T) {
	ws := &fakeTransportWS{} // answers UNKNOWN_DOMAIN for "git"
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw")
	_, err := cl.StartGitImport(context.Background(), ws, demoZip(t, "PREFIX"), GitImportOptions{Package: "$ZDEMO"})
	if err == nil || !strings.Contains(err.Error(), "abapGit is not installed") {
		t.Errorf("got %v", err)
	}
}

// --- delete -------------------------------------------------------------------

func TestParseGitDeleteItems(t *testing.T) {
	items, err := ParseGitDeleteItems([]any{"prog zdemo_report", "R3TR CLAS ZCL_DEMO", map[string]any{"type": "intf", "name": "zif_demo"}, "PROG ZDEMO_REPORT"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0] != (GitDeleteItem{Type: "PROG", Name: "ZDEMO_REPORT"}) || items[1] != (GitDeleteItem{Type: "CLAS", Name: "ZCL_DEMO"}) || items[2] != (GitDeleteItem{Type: "INTF", Name: "ZIF_DEMO"}) {
		t.Errorf("items %+v", items)
	}
	if items, err := ParseGitDeleteItems("PROG A, PROG B"); err != nil || len(items) != 2 {
		t.Errorf("comma list: %v %v", items, err)
	}
	for _, bad := range []any{nil, "", []any{"ZDEMO"}, []any{"PROGRAM ZDEMO"}, []any{42}, []any{"PROG A B C D"}} {
		if _, err := ParseGitDeleteItems(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestCheckGitDelete(t *testing.T) {
	cases := []struct {
		name    string
		opts    []Option
		pkg, tr string
	}{
		{"read-only", []Option{WithReadOnly()}, "$ZDEMO", ""},
		{"delete disallowed", []Option{WithSafety(SafetyConfig{DisallowedOps: "D"})}, "$ZDEMO", ""},
		{"outside whitelist", []Option{WithAllowedPackages("$ZOTHER")}, "$ZDEMO", ""},
		{"transportable without transport", []Option{WithAllowTransportableEdits()}, "ZDEMO", ""},
		{"transportable without opt-in", nil, "ZDEMO", "TRXK900001"},
		{"local with transport", nil, "$ZDEMO", "TRXK900001"},
	}
	for _, c := range cases {
		cl := NewClient("http://sap.invalid", "TESTUSER", "pw", c.opts...)
		if err := cl.CheckGitDelete(c.pkg, c.tr); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	if err := NewClient("http://sap.invalid", "TESTUSER", "pw", WithAllowedPackages("$ZDEMO")).CheckGitDelete("$zdemo", ""); err != nil {
		t.Errorf("allowed delete refused: %v", err)
	}
}

func pkgContents(pkg string, objs [][2]string, subs []string, repo bool) map[string]any {
	o := make([]map[string]any, 0, len(objs))
	for _, x := range objs {
		dev := pkg
		if strings.HasPrefix(x[1], "@") { // in another package
			dev, x[1] = "$ZOTHER", x[1][1:]
		}
		o = append(o, map[string]any{"pgmid": "R3TR", "object": x[0], "obj_name": x[1], "devclass": dev})
	}
	m := map[string]any{"package": pkg, "exists": true, "objects": o, "subpackages": subs}
	if repo {
		m["repo"] = map[string]any{"key": "000000000001", "name": "demo", "offline": true}
	}
	return m
}

// unknownRepo gives package contents a repository abapGit could not open.
func unknownRepo(m map[string]any) map[string]any {
	m["repo"] = map[string]any{"key": "000000000003", "name": "000000000003", "offline": false, "repo_state": "unknown"}
	return m
}

// noState gives package contents a repository as an older ZADT_VSP reports
// it: no repo_state.
func noState(m map[string]any, offline bool) map[string]any {
	m["repo"] = map[string]any{"key": "000000000004", "name": "old", "offline": offline}
	return m
}

// online makes the repository of package contents an online one.
func online(m map[string]any) map[string]any {
	m["repo"] = map[string]any{"key": "000000000002", "name": "upstream", "offline": false}
	return m
}

// gitDeleteRoute answers the ADT side of deletes: the search the gate uses
// (every name in package pkgOf[name]), LOCK, DELETE.
func gitDeleteRoute(pkgOf map[string]string, uris map[string]string, failDelete map[string]bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			q := strings.ToUpper(strings.Trim(r.URL.Query().Get("query"), "*"))
			var b strings.Builder
			b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">`)
			// A type filter is honoured, as SAP does: PROG means PROG/P only.
			if p, ok := pkgOf[q]; ok && searchTypeMatches(r, "PROG/P") {
				fmt.Fprintf(&b, `<adtcore:objectReference adtcore:uri="%s" adtcore:type="PROG/P" adtcore:name="%s" adtcore:packageName="%s"/>`, uris[q], q, p)
			}
			b.WriteString(`</adtcore:objectReferences>`)
			_, _ = io.WriteString(w, b.String())
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		case r.Method == http.MethodDelete:
			for n, fail := range failDelete {
				if fail && strings.EqualFold(r.URL.Path, uris[n]) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, "in use")
					return
				}
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}
}

// searchTypeMatches says whether a hit of ADT type adtType passes the
// search's objectType filter, as SAP applies it: exactly, after the short
// form is expanded (PROG -> PROG/P, TABL -> TABL/DT).
func searchTypeMatches(r *http.Request, adtType string) bool {
	ot := r.URL.Query().Get("objectType")
	return ot == "" || strings.EqualFold(ot, adtType)
}

func deletedPaths(calls []wireCall) []string {
	var out []string
	for _, c := range calls {
		if c.method == http.MethodDelete {
			out = append(out, c.path)
		}
	}
	return out
}

func TestDeleteGitObjectsScope(t *testing.T) {
	uris := map[string]string{
		"ZDEMO_REPORT": "/sap/bc/adt/programs/programs/zdemo_report",
		"ZDEMO_KEEP":   "/sap/bc/adt/programs/programs/zdemo_keep",
		"ZDEMO_ELSE":   "/sap/bc/adt/programs/programs/zdemo_else",
		"$ZDEMO":       "/sap/bc/adt/packages/%24zdemo",
	}
	pkgOf := map[string]string{"ZDEMO_REPORT": "$ZDEMO", "ZDEMO_KEEP": "$ZDEMO", "ZDEMO_ELSE": "$ZOTHER", "$ZDEMO": "$ZDEMO"}
	rec := &adtRecorder{}
	cl := newStubbedClient(t, rec, gitDeleteRoute(pkgOf, uris, nil), WithAllowedPackages("$ZDEMO"))
	ws := &fakeGitWS{contents: []map[string]any{
		pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_REPORT"}, {"PROG", "ZDEMO_KEEP"}}, nil, true),
		pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_KEEP"}}, nil, true),
	}}
	res, err := cl.DeleteGitObjects(context.Background(), ws, "$ZDEMO", []GitDeleteItem{
		{Type: "PROG", Name: "ZDEMO_REPORT"}, {Type: "PROG", Name: "ZDEMO_ELSE"}, {Type: "DEVC", Name: "$ZDEMO"}, {Type: "PROG", Name: "ZDEMO_ABSENT"},
	}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, o := range res.Objects {
		got[o.Type+" "+o.Name] = o.Status
	}
	want := map[string]string{"PROG ZDEMO_REPORT": "deleted", "PROG ZDEMO_ELSE": "skipped", "DEVC $ZDEMO": "skipped", "PROG ZDEMO_ABSENT": "skipped"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s, want %s", k, got[k], v)
		}
	}
	if d := deletedPaths(rec.snapshot()); len(d) != 1 || d[0] != uris["ZDEMO_REPORT"] {
		t.Errorf("DELETEs %v; want exactly %s", d, uris["ZDEMO_REPORT"])
	}
	// ZDEMO_KEEP remains: even with delete_repo the repository stays, and
	// so does the package.
	if res.RepoDeleted || res.PackageDeleted || len(res.Remaining) != 1 || !strings.Contains(res.RepoNote, "remain") {
		t.Errorf("repo deleted %t (%s), package deleted %t, remaining %v: a package with an object left keeps both",
			res.RepoDeleted, res.RepoNote, res.PackageDeleted, res.Remaining)
	}
	if p := ws.params("delete_repo"); p != nil {
		t.Errorf("delete_repo sent with an object left: %v", p)
	}
}

// The repository is unregistered only on delete_repo, only an offline one,
// only from a package left empty; an online one never, and delete_repo
// with one is refused before anything is deleted. The package goes only
// when no repository is registered for it any more.
func TestDeleteGitObjectsRepositoryRules(t *testing.T) {
	uris := map[string]string{"ZDEMO_REPORT": "/sap/bc/adt/programs/programs/zdemo_report", "$ZDEMO": "/sap/bc/adt/packages/%24ZDEMO"}
	pkgOf := map[string]string{"ZDEMO_REPORT": "$ZDEMO", "$ZDEMO": "$ZDEMO"}
	full := func() map[string]any { return pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_REPORT"}}, nil, true) }
	empty := func() map[string]any { return pkgContents("$ZDEMO", nil, nil, true) }
	cases := []struct {
		name                 string
		contents             []map[string]any
		deleteRepo           bool
		wantErr              string
		wantDeletes          int
		wantActions          string
		repoDeleted, pkgGone bool
		repoNote             string
	}{
		{"online repository, delete_repo: refused before anything", []map[string]any{online(full())}, true,
			"never unregisters an online repository", 0, "package_objects", false, false, ""},
		{"online repository, no delete_repo: kept, and the package with it", []map[string]any{online(full()), online(empty())}, false,
			"", 1, "package_objects,package_objects", false, false, "online"},
		{"offline repository, no delete_repo: kept, and the package with it", []map[string]any{full(), empty()}, false,
			"", 1, "package_objects,package_objects", false, false, "delete_repo"},
		{"offline repository, delete_repo, package empty: unregistered, then the package", []map[string]any{full(), empty(), pkgContents("$ZDEMO", nil, nil, false)}, true,
			"", 2, "package_objects,package_objects,delete_repo,package_objects", true, true, ""},
		{"no repository: the package goes", []map[string]any{pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_REPORT"}}, nil, false), pkgContents("$ZDEMO", nil, nil, false)}, false,
			"", 2, "package_objects,package_objects,package_objects", false, true, "no abapGit repository"},
		// abapGit could not open the repository: its state is unknown, and
		// unknown is online -- never unregistered, the package never deleted.
		{"unknown repository, delete_repo: refused before anything", []map[string]any{unknownRepo(full())}, true,
			"state unknown", 0, "package_objects", false, false, ""},
		{"unknown repository, no delete_repo: kept, and the package with it", []map[string]any{unknownRepo(full()), unknownRepo(empty())}, false,
			"", 1, "package_objects,package_objects", false, false, "state unknown"},
		{"an older ZADT_VSP without a state, offline false: online", []map[string]any{noState(full(), false)}, true,
			"never unregisters", 0, "package_objects", false, false, ""},
		// A repository registered between the reads keeps the package.
		{"a repository appears before the package delete", []map[string]any{pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_REPORT"}}, nil, false), pkgContents("$ZDEMO", nil, nil, false), unknownRepo(empty())}, false,
			"", 1, "package_objects,package_objects,package_objects", false, false, "no abapGit repository"},
	}
	for _, c := range cases {
		rec := &adtRecorder{}
		cl := newStubbedClient(t, rec, gitDeleteRoute(pkgOf, uris, nil), WithAllowedPackages("$ZDEMO"))
		ws := &fakeGitWS{contents: c.contents}
		res, err := cl.DeleteGitObjects(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_REPORT"}}, "", c.deleteRepo)
		switch {
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: error %v, want %q", c.name, err, c.wantErr)
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		}
		if n := len(deletedPaths(rec.snapshot())); n != c.wantDeletes {
			t.Errorf("%s: %d DELETEs, want %d", c.name, n, c.wantDeletes)
		}
		if a := strings.Join(ws.actions(), ","); a != c.wantActions {
			t.Errorf("%s: git actions %s, want %s", c.name, a, c.wantActions)
		}
		if res == nil {
			continue
		}
		if res.RepoDeleted != c.repoDeleted || res.PackageDeleted != c.pkgGone || !strings.Contains(res.RepoNote, c.repoNote) {
			t.Errorf("%s: repo deleted %t (%q), package deleted %t (%q)", c.name, res.RepoDeleted, res.RepoNote, res.PackageDeleted, res.PackageNote)
		}
	}
}

func TestDeleteGitObjectsRemovesTheEmptyPackageLast(t *testing.T) {
	uris := map[string]string{
		"ZDEMO_REPORT": "/sap/bc/adt/programs/programs/zdemo_report",
		"$ZDEMO":       "/sap/bc/adt/packages/%24ZDEMO",
	}
	pkgOf := map[string]string{"ZDEMO_REPORT": "$ZDEMO", "$ZDEMO": "$ZDEMO"}
	rec := &adtRecorder{}
	cl := newStubbedClient(t, rec, gitDeleteRoute(pkgOf, uris, nil), WithAllowedPackages("$ZDEMO"))
	ws := &fakeGitWS{contents: []map[string]any{
		pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_REPORT"}}, nil, true),
		pkgContents("$ZDEMO", nil, nil, true),
		pkgContents("$ZDEMO", nil, nil, false),
	}}
	res, err := cl.DeleteGitObjects(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_REPORT"}}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	d := deletedPaths(rec.snapshot())
	if len(d) != 2 || d[0] != uris["ZDEMO_REPORT"] || d[1] != "/sap/bc/adt/packages/$ZDEMO" || !res.PackageDeleted {
		t.Errorf("DELETEs %v, package deleted %t", d, res.PackageDeleted)
	}
	if acts := strings.Join(ws.actions(), ","); acts != "package_objects,package_objects,delete_repo,package_objects" {
		t.Errorf("git actions %s", acts)
	}
	// Each DELETE is followed by an UNLOCK of the same object: a DELETE
	// leaves its ENQUEUE behind, and abapGit then finds the object locked.
	calls := rec.snapshot()
	for i, c := range calls {
		if c.method != http.MethodDelete {
			continue
		}
		if i+1 >= len(calls) || !isUnlock(calls[i+1]) || calls[i+1].path != c.path {
			dumpCalls(t, calls)
			t.Errorf("DELETE %s is not followed by its UNLOCK", c.path)
		}
	}

	// A subpackage keeps the package.
	rec = &adtRecorder{}
	cl = newStubbedClient(t, rec, gitDeleteRoute(pkgOf, uris, nil), WithAllowedPackages("$ZDEMO"))
	ws = &fakeGitWS{contents: []map[string]any{
		pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_REPORT"}}, nil, true),
		pkgContents("$ZDEMO", nil, []string{"$ZDEMO_SUB"}, true),
	}}
	res, err = cl.DeleteGitObjects(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_REPORT"}}, "", true)
	if err != nil || res.PackageDeleted || res.RepoDeleted || len(deletedPaths(rec.snapshot())) != 1 || ws.params("delete_repo") != nil {
		t.Errorf("a package with a subpackage was deleted: %+v %v", res, err)
	}
}

// When an object cannot be deleted, the repository and the package stay.
func TestDeleteGitObjectsStopsOnFailure(t *testing.T) {
	uris := map[string]string{"ZDEMO_REPORT": "/sap/bc/adt/programs/programs/zdemo_report", "$ZDEMO": "/sap/bc/adt/packages/%24ZDEMO"}
	pkgOf := map[string]string{"ZDEMO_REPORT": "$ZDEMO", "$ZDEMO": "$ZDEMO"}
	rec := &adtRecorder{}
	cl := newStubbedClient(t, rec, gitDeleteRoute(pkgOf, uris, map[string]bool{"ZDEMO_REPORT": true}), WithAllowedPackages("$ZDEMO"))
	ws := &fakeGitWS{contents: []map[string]any{pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_REPORT"}}, nil, true)}}
	res, err := cl.DeleteGitObjects(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_REPORT"}}, "", true)
	if err == nil || res == nil || res.RepoDeleted || res.PackageDeleted {
		t.Fatalf("a failed delete went on: %+v %v", res, err)
	}
	if acts := strings.Join(ws.actions(), ","); acts != "package_objects" {
		t.Errorf("git actions after the failure: %s", acts)
	}
}

// The whitelist and read-only refuse before anything is read or sent; an
// object the package's TADIR has in another package is never deleted, even
// when the gate would allow that package.
func TestDeleteGitObjectsGatesAndForeignObjects(t *testing.T) {
	ws := &fakeGitWS{}
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw", WithReadOnly())
	if _, err := cl.DeleteGitObjects(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_REPORT"}}, "", true); err == nil || len(ws.actions()) != 0 {
		t.Errorf("read-only: %v, %v", err, ws.actions())
	}

	uris := map[string]string{"ZDEMO_ELSE": "/sap/bc/adt/programs/programs/zdemo_else"}
	rec := &adtRecorder{}
	cl = newStubbedClient(t, rec, gitDeleteRoute(map[string]string{"ZDEMO_ELSE": "$ZOTHER"}, uris, nil)) // no whitelist at all
	ws = &fakeGitWS{contents: []map[string]any{pkgContents("$ZDEMO", [][2]string{{"PROG", "@ZDEMO_ELSE"}}, nil, false)}}
	res, _ := cl.DeleteGitObjects(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_ELSE"}}, "", false)
	if d := deletedPaths(rec.snapshot()); len(d) != 0 {
		t.Errorf("an object of another package was deleted: %v (%+v)", d, res)
	}
}

func TestGitObjectURL(t *testing.T) {
	for typ, want := range map[string]string{
		"PROG": "/sap/bc/adt/programs/programs/zdemo",
		"CLAS": "/sap/bc/adt/oo/classes/zdemo",
		"DEVC": "/sap/bc/adt/packages/ZDEMO",
	} {
		if got, ok := GitObjectURL(typ, "ZDEMO"); !ok || got != want {
			t.Errorf("%s: %q", typ, got)
		}
	}
	// SAP addresses classic objects in a namespace in upper case; RAP and
	// DDIC sources stay lower case.
	for typ, want := range map[string]string{
		"CLAS": "/sap/bc/adt/oo/classes/%2FDEMO%2FCL_X",
		"PROG": "/sap/bc/adt/programs/programs/%2FDEMO%2FCL_X",
		"INTF": "/sap/bc/adt/oo/interfaces/%2FDEMO%2FCL_X",
		"FUGR": "/sap/bc/adt/functions/groups/%2FDEMO%2FCL_X",
		"DDLS": "/sap/bc/adt/ddic/ddl/sources/%2Fdemo%2Fcl_x",
	} {
		if u, _ := GitObjectURL(typ, "/DEMO/CL_X"); u != want {
			t.Errorf("namespaced %s: %s, want %s", typ, u, want)
		}
	}
	if _, ok := GitObjectURL("SUSC", "ZDEMO"); ok {
		t.Error("a type with no ADT delete here claimed one")
	}
}

// A DELETE that fails and leaves its lock behind is not tried again: the
// error keeps the SM12 advice.
func TestDeleteGitObjectsKeepsAStrandedLock(t *testing.T) {
	uris := map[string]string{"ZDEMO_REPORT": "/sap/bc/adt/programs/programs/zdemo_report"}
	pkgOf := map[string]string{"ZDEMO_REPORT": "$ZDEMO"}
	base := gitDeleteRoute(pkgOf, uris, map[string]bool{"ZDEMO_REPORT": true})
	route := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Query().Get("_action") == "UNLOCK" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		base(w, r)
	}
	rec := &adtRecorder{}
	cl := newStubbedClient(t, rec, route, WithAllowedPackages("$ZDEMO"))
	ws := &fakeGitWS{contents: []map[string]any{pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_REPORT"}}, nil, false)}}
	res, err := cl.DeleteGitObjects(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_REPORT"}}, "", false)
	if err == nil || res == nil || !strings.Contains(err.Error(), "LOCKED") || !strings.Contains(res.Objects[0].Reason, "LOCKED") {
		t.Fatalf("got %+v, %v", res, err)
	}
	if n := len(deletedPaths(rec.snapshot())); n != 1 {
		t.Errorf("%d DELETEs; a delete that stranded its lock must not be tried again", n)
	}
}

// The ADT address comes from the object itself, not from its TADIR type:
// a PROG that is an include and a TABL that is a structure are deleted at
// their own collections.
func TestDeleteGitObjectsResolvesTheADTAddress(t *testing.T) {
	type obj struct{ typ, adtType, uri string }
	objs := map[string]obj{
		"ZDEMO_INCL":   {"PROG", "PROG/I", "/sap/bc/adt/programs/includes/zdemo_incl"},
		"ZDEMO_STRUCT": {"TABL", "TABL/DS", "/sap/bc/adt/ddic/structures/zdemo_struct"},
	}
	route := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "informationsystem/search"):
			q := strings.ToUpper(strings.Trim(r.URL.Query().Get("query"), "*"))
			var b strings.Builder
			b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">`)
			if o, ok := objs[q]; ok && searchTypeMatches(r, o.adtType) {
				fmt.Fprintf(&b, `<adtcore:objectReference adtcore:uri="%s" adtcore:type="%s" adtcore:name="%s" adtcore:packageName="$ZDEMO"/>`, o.uri, o.adtType, q)
			}
			b.WriteString(`</adtcore:objectReferences>`)
			_, _ = io.WriteString(w, b.String())
		case r.Method == http.MethodPost && r.URL.Query().Get("_action") == "LOCK":
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = io.WriteString(w, testLockXML)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}
	rec := &adtRecorder{}
	cl := newStubbedClient(t, rec, route, WithAllowedPackages("$ZDEMO"))
	ws := &fakeGitWS{contents: []map[string]any{
		pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_INCL"}, {"TABL", "ZDEMO_STRUCT"}}, nil, false),
		pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_KEEP"}}, nil, false),
	}}
	if _, err := cl.DeleteGitObjects(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_INCL"}, {Type: "TABL", Name: "ZDEMO_STRUCT"}}, "", false); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(deletedPaths(rec.snapshot()), ",")
	if want := objs["ZDEMO_INCL"].uri + "," + objs["ZDEMO_STRUCT"].uri; got != want {
		t.Errorf("DELETEs %s, want %s", got, want)
	}
}

// An import's writes do not pass this client's HTTP cache: a terminal
// status empties it, a pending one does not.
func TestGitImportStatusInvalidatesTheCache(t *testing.T) {
	store := NewMemoryResponseStore()
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw", WithCacheStore(store, time.Hour))
	for _, c := range []struct {
		outcome string
		left    int
	}{{"pending", 1}, {"failed", 0}, {"done", 0}} {
		store.Put("GET /sap/bc/adt/programs/programs/zdemo/source/main", &CachedResponse{StatusCode: 200, Expires: time.Now().Add(time.Hour)})
		st := map[string]any{"outcome": c.outcome, "job_found": true}
		if c.outcome == "done" {
			st["result"] = map[string]any{"outcome": "imported", "package": "$ZDEMO"}
		}
		if _, err := cl.GitImportStatus(context.Background(), &fakeGitWS{status: []map[string]any{st}}, "12345678"); err != nil {
			t.Fatal(err)
		}
		if n := store.Len(); n != c.left {
			t.Errorf("%s: %d cache entries left, want %d", c.outcome, n, c.left)
		}
		store.Clear()
	}
}

// Two imports on one connection do not interleave: ZADT_VSP holds one
// upload per session.
func TestStartGitImportOneUploadAtATime(t *testing.T) {
	ws := &fakeGitWS{}
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = cl.StartGitImport(context.Background(), ws, demoZip(t, "PREFIX"), GitImportOptions{Package: "$ZDEMO"})
		}()
	}
	wg.Wait()
	open := false
	for _, a := range ws.actions() {
		switch a {
		case "import_zip:begin":
			if open {
				t.Fatalf("a begin inside another upload: %v", ws.actions())
			}
			open = true
		case "import_zip:commit", "import_zip:abort":
			open = false
		}
	}
}

// gitUploadEntries is how many connections have an upload lock entry.
func gitUploadEntries() int {
	gitUploadsMu.Lock()
	defer gitUploadsMu.Unlock()
	return len(gitUploads)
}

// An object TADIR listed in the package, which the search then finds in
// another package, moved in between: it is not deleted -- not at the address
// the search gives, not at the static one -- even with no whitelist at all.
// A search that fails cannot confirm the package either.
func TestDeleteGitObjectsRefusesAMovedObject(t *testing.T) {
	uris := map[string]string{"ZDEMO_MOVED": "/sap/bc/adt/programs/programs/zdemo_moved"}
	for name, route := range map[string]http.HandlerFunc{
		"moved": gitDeleteRoute(map[string]string{"ZDEMO_MOVED": "$ZOTHER"}, uris, nil),
		"hit without a package": func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "informationsystem/search") {
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprint(w, `<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core"><adtcore:objectReference adtcore:uri="/sap/bc/adt/programs/programs/zdemo_moved" adtcore:type="PROG/P" adtcore:name="ZDEMO_MOVED"/></adtcore:objectReferences>`)
				return
			}
			gitDeleteRoute(nil, uris, nil)(w, r)
		},
		"search window full": func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "informationsystem/search") {
				// A full window of other types: a PROG hit may be past it.
				var b strings.Builder
				b.WriteString(`<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">`)
				for i := 0; i < 1000; i++ {
					fmt.Fprintf(&b, `<adtcore:objectReference adtcore:uri="/sap/bc/adt/ddic/dataelements/zdemo_moved%d" adtcore:type="DTEL/DE" adtcore:name="ZDEMO_MOVED" adtcore:packageName="$ZDEMO"/>`, i)
				}
				b.WriteString(`</adtcore:objectReferences>`)
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprint(w, b.String())
				return
			}
			gitDeleteRoute(nil, uris, nil)(w, r)
		},
		"search fails": func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "informationsystem/search") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			gitDeleteRoute(nil, uris, nil)(w, r)
		},
	} {
		rec := &adtRecorder{}
		cl := newStubbedClient(t, rec, route) // no whitelist: the gate would allow any package
		ws := &fakeGitWS{contents: []map[string]any{pkgContents("$ZDEMO", [][2]string{{"PROG", "ZDEMO_MOVED"}}, nil, false)}}
		res, err := cl.DeleteGitObjects(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "PROG", Name: "ZDEMO_MOVED"}}, "", false)
		if err == nil || res == nil || res.Objects[0].Status != "failed" || res.PackageDeleted {
			t.Fatalf("%s: got %+v, %v", name, res, err)
		}
		if name == "moved" && !strings.Contains(res.Objects[0].Reason, "$ZOTHER") {
			t.Errorf("%s: the reason does not name the new package: %s", name, res.Objects[0].Reason)
		}
		if want := map[string]string{"hit without a package": "did not say which package", "search window full": "may be incomplete"}[name]; want != "" && !strings.Contains(res.Objects[0].Reason, want) {
			t.Errorf("%s: reason %q, want it to say %q", name, res.Objects[0].Reason, want)
		}
		for _, c := range rec.snapshot() {
			if c.method == http.MethodDelete || (c.method == http.MethodPost && c.query.Get("_action") == "LOCK") {
				t.Errorf("%s: %s sent for an object that is not confirmed in the package", name, c)
			}
		}
		if a := strings.Join(ws.actions(), ","); a != "package_objects" {
			t.Errorf("%s: git actions %s", name, a)
		}
	}
}

// A direct caller's lower-case types meet the same checks: "devc" is the
// package, never deleted as an item.
func TestDeleteGitObjectsNormalizesTypes(t *testing.T) {
	uris := map[string]string{
		"ZDEMO_REPORT": "/sap/bc/adt/programs/programs/zdemo_report",
		"$ZDEMO":       "/sap/bc/adt/packages/%24zdemo",
	}
	pkgOf := map[string]string{"ZDEMO_REPORT": "$ZDEMO", "$ZDEMO": "$ZDEMO"}
	rec := &adtRecorder{}
	cl := newStubbedClient(t, rec, gitDeleteRoute(pkgOf, uris, nil), WithAllowedPackages("$ZDEMO"))
	ws := &fakeGitWS{contents: []map[string]any{
		pkgContents("$ZDEMO", [][2]string{{"DEVC", "$ZDEMO"}, {"PROG", "ZDEMO_REPORT"}, {"PROG", "ZDEMO_KEEP"}}, nil, false),
		pkgContents("$ZDEMO", [][2]string{{"DEVC", "$ZDEMO"}, {"PROG", "ZDEMO_KEEP"}}, nil, false),
	}}
	res, err := cl.DeleteGitObjects(context.Background(), ws, "$ZDEMO", []GitDeleteItem{{Type: "devc", Name: "$zdemo"}, {Type: "prog", Name: "zdemo_report"}}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Objects[0].Type != "DEVC" || res.Objects[0].Status != "skipped" || res.Objects[1].Status != "deleted" {
		t.Errorf("outcomes %+v", res.Objects)
	}
	if d := deletedPaths(rec.snapshot()); len(d) != 1 || d[0] != uris["ZDEMO_REPORT"] {
		t.Errorf("DELETEs %v; want only %s", d, uris["ZDEMO_REPORT"])
	}
}

// Transportable, no transport named, and transport choice off: refused by
// the policy, before the zip is read or anything sent.
func TestStartGitImportTransportChoiceOffRefusesFirst(t *testing.T) {
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw", WithAllowTransportableEdits(), WithTransportChoice("off"))
	if err := cl.CheckGitImportPolicy("ZDEMO", "", false); err == nil || !strings.Contains(err.Error(), "name the transport") {
		t.Errorf("policy: %v", err)
	}
	ws := &fakeGitWS{}
	if _, err := cl.StartGitImport(context.Background(), ws, []byte("not even a zip"), GitImportOptions{Package: "ZDEMO"}); err == nil ||
		!strings.Contains(err.Error(), "name the transport") {
		t.Errorf("import: %v", err)
	}
	if n := len(ws.actions()); n != 0 {
		t.Errorf("%d messages sent", n)
	}
	// With a transport named, or choice on, the policy passes.
	if err := cl.CheckGitImportPolicy("ZDEMO", "TRXK900001", false); err != nil {
		t.Errorf("with a transport: %v", err)
	}
	if err := NewClient("http://sap.invalid", "TESTUSER", "pw", WithAllowTransportableEdits()).CheckGitImportPolicy("ZDEMO", "", false); err != nil {
		t.Errorf("choice on: %v", err)
	}
}

// A begin without an answer may have left an upload nobody can abort: the
// connection is reset. A refused begin holds none: nothing is reset.
// An abort or a commit without an answer reset it too.
func TestStartGitImportResetsAnUnconfirmedUpload(t *testing.T) {
	cases := []struct {
		name      string
		ws        *fakeGitWS
		opts      []Option
		wantReset int
		wantActs  string
	}{
		{"begin timed out", &fakeGitWS{beginErr: errors.New("request timeout")}, nil, 1, "import_zip:begin"},
		{"begin cancelled", &fakeGitWS{beginErr: context.Canceled}, nil, 1, "import_zip:begin"},
		{"begin without an assembly id", &fakeGitWS{beginNoID: true}, nil, 1, "import_zip:begin"},
		{"begin refused", &fakeGitWS{beginRefusal: &WSError{Code: "UPLOAD_IN_PROGRESS", Message: "busy"}}, nil, 0, "import_zip:begin"},
		{"abort answered", &fakeGitWS{client: "200"}, []Option{WithClient("001")}, 0, "import_zip:begin,import_zip:abort"},
		{"abort unanswered", &fakeGitWS{client: "200", abortErr: errors.New("request timeout")}, []Option{WithClient("001")}, 1, "import_zip:begin,import_zip:abort"},
		{"commit unanswered", &fakeGitWS{commitErr: errors.New("request timeout")}, nil, 1, ""},
	}
	for _, c := range cases {
		cl := NewClient("http://sap.invalid", "TESTUSER", "pw", c.opts...)
		_, err := cl.StartGitImport(context.Background(), c.ws, demoZip(t, "PREFIX"), GitImportOptions{Package: "$ZDEMO"})
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if got := c.ws.closed(); got != c.wantReset {
			t.Errorf("%s: %d resets, want %d (%v)", c.name, got, c.wantReset, err)
		}
		if c.wantReset > 0 && !strings.Contains(err.Error(), "connection was reset") {
			t.Errorf("%s: the error does not say the connection was reset: %v", c.name, err)
		}
		if c.wantActs != "" {
			if a := strings.Join(c.ws.actions(), ","); a != c.wantActs {
				t.Errorf("%s: actions %s, want %s", c.name, a, c.wantActs)
			}
		}
	}
	if n := gitUploadEntries(); n != 0 {
		t.Errorf("%d upload lock entries left behind", n)
	}
}

// The upload lock of a connection lives while anyone holds or waits for it,
// and goes with the last one; it stays one lock while it is shared.
func TestGitUploadLockIsReleasedWithItsLastUser(t *testing.T) {
	ws := &fakeGitWS{}
	unlock1 := lockGitUpload(ws)
	acquired := make(chan func())
	go func() { acquired <- lockGitUpload(ws) }()
	// The second user waits on the same entry.
	deadline := time.Now().Add(5 * time.Second)
	for {
		gitUploadsMu.Lock()
		users := 0
		if l := gitUploads[ws]; l != nil {
			users = l.users
		}
		gitUploadsMu.Unlock()
		if users == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second user never registered")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-acquired:
		t.Fatal("two users hold the lock at once")
	case <-time.After(20 * time.Millisecond):
	}
	unlock1()
	unlock1() // a second call is harmless
	unlock2 := <-acquired
	if n := gitUploadEntries(); n != 1 {
		t.Errorf("%d entries while the second user holds the lock, want 1", n)
	}
	unlock2()
	if n := gitUploadEntries(); n != 0 {
		t.Errorf("%d entries after the last user, want 0", n)
	}

	// Many imports on many connections leave nothing behind.
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		w := &fakeGitWS{}
		for j := 0; j < 2; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = cl.StartGitImport(context.Background(), w, demoZip(t, "PREFIX"), GitImportOptions{Package: "$ZDEMO"})
			}()
		}
	}
	wg.Wait()
	if n := gitUploadEntries(); n != 0 {
		t.Errorf("%d entries after every import ended, want 0", n)
	}
}

// fakeGitPusher is a git connection whose push channel fails when told to,
// and whose status reads block until cancelled once blockStatus is set.
type fakeGitPusher struct {
	*fakeGitWS
	pushFail    chan struct{}
	blockStatus bool
	cancelled   chan struct{}
}

func (f *fakeGitPusher) SendDomainRequest(ctx context.Context, domain, action string, params map[string]any, timeout time.Duration) (*WSResponse, error) {
	if action == "import_status" && f.blockStatus {
		<-ctx.Done()
		close(f.cancelled)
		return nil, ctx.Err()
	}
	return f.fakeGitWS.SendDomainRequest(ctx, domain, action, params, timeout)
}

func (f *fakeGitPusher) AwaitPush(ctx context.Context, _ string) (*WSResponse, error) {
	select {
	case <-f.pushFail:
		return nil, ErrWebSocketClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeGitPusher) TakePush(string) (*WSResponse, bool) { return nil, false }

// A disconnect while waiting is a failure, not a wait that ran out: a status
// read that fails is returned, and the push channel closing cancels the read
// in flight and is returned. Only the caller's own deadline is a timeout.
func TestWaitGitImportReportsADisconnect(t *testing.T) {
	old := gitPollInterval
	gitPollInterval = time.Millisecond
	defer func() { gitPollInterval = old }()
	cl := NewClient("http://sap.invalid", "TESTUSER", "pw")
	pending := map[string]any{"job": "ZVSP_GIT_IMPORT", "job_count": "12345678", "job_found": true, "job_status": "R", "outcome": "pending"}

	// A status read fails after one pending answer.
	ws := &fakeGitWS{status: []map[string]any{pending}, statusErr: errors.New("not connected")}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	st, err := cl.WaitGitImport(ctx, ws, "12345678")
	cancel()
	if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "not connected") || !strings.Contains(err.Error(), "12345678") {
		t.Errorf("status read failing: %v", err)
	}
	if st == nil || st.State != GitJobPending {
		t.Errorf("the last state read is lost: %+v", st)
	}

	// The push channel closes while a status read is in flight.
	p := &fakeGitPusher{fakeGitWS: &fakeGitWS{}, pushFail: make(chan struct{}), blockStatus: true, cancelled: make(chan struct{})}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { time.Sleep(10 * time.Millisecond); close(p.pushFail) }()
	_, err = cl.WaitGitImport(ctx, p, "12345678")
	if !errors.Is(err, ErrWebSocketClosed) || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "12345678") {
		t.Errorf("push channel closing: %v", err)
	}
	select {
	case <-p.cancelled:
	case <-time.After(time.Second):
		t.Error("the status read in flight was not cancelled")
	}
	if ctx.Err() != nil {
		t.Error("the wait ran until the caller's deadline")
	}

	// The caller's deadline, with the job still pending: a timeout, as before.
	ws = &fakeGitWS{status: []map[string]any{pending}}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	st, err = cl.WaitGitImport(ctx2, ws, "12345678")
	if !errors.Is(err, context.DeadlineExceeded) || st.State != GitJobPending {
		t.Errorf("deadline: %+v, %v", st, err)
	}
}

// A function module of the same name as its group comes first in the search,
// even from another package: the group's own hit gives the address, and the
// module neither stands in for it nor counts as the group having moved.
func TestGitDeleteURLTakesTheObjectNotItsPart(t *testing.T) {
	route := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "informationsystem/search") {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, `<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">`+
				`<adtcore:objectReference adtcore:uri="/sap/bc/adt/functions/groups/zother/fmodules/zdemo_fg" adtcore:type="FUGR/FF" adtcore:name="ZDEMO_FG" adtcore:packageName="$ZOTHER"/>`+
				`<adtcore:objectReference adtcore:uri="/sap/bc/adt/functions/groups/zdemo_fg" adtcore:type="FUGR/F" adtcore:name="ZDEMO_FG" adtcore:packageName="$ZDEMO"/>`+
				`</adtcore:objectReferences>`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
	cl := newStubbedClient(t, &adtRecorder{}, route)
	u, err := cl.gitDeleteURL(context.Background(), "FUGR", "ZDEMO_FG", "$ZDEMO")
	if err != nil || u != "/sap/bc/adt/functions/groups/zdemo_fg" {
		t.Fatalf("got %q, %v; want the group's own address", u, err)
	}
	if !gitHitIsObject("PROG", "PROG/I") || gitHitIsObject("FUGR", "FUGR/FF") || !gitHitIsObject("ZZZZ", "ZZZZ/XY") {
		t.Error("gitHitIsObject")
	}
}
