package mcp

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// gitFakeWS answers ZADT_VSP's git domain and records every call.
type gitFakeWS struct {
	mu      sync.Mutex
	actions []string
	dials   int
	// result is what import_status reports once the job is done.
	result map[string]any
	// pkg is what package_objects reports.
	pkg map[string]any
	// commitErr fails the commit without an answer.
	commitErr error
	// statusErr fails import_status without an answer.
	statusErr error
}

func (f *gitFakeWS) SendDomainRequest(_ context.Context, domain, action string, params map[string]any, _ time.Duration) (*adt.WSResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if step, ok := params["step"].(string); ok {
		action += ":" + step
	}
	f.actions = append(f.actions, domain+"."+action)
	var data any
	switch action {
	case "import_zip:begin":
		data = map[string]any{"assembly_id": "A1", "package": params["package"], "system": "XYZ", "client": "100"}
	case "import_zip:commit":
		if f.commitErr != nil {
			return nil, f.commitErr
		}
		data = map[string]any{"status": "pending", "job": "ZVSP_GIT_IMPORT", "job_count": "12345678"}
	case "package_objects":
		data = f.pkg
	case "import_status":
		if f.statusErr != nil {
			return nil, f.statusErr
		}
		data = map[string]any{"job": "ZVSP_GIT_IMPORT", "job_count": params["job"], "job_found": true, "job_status": "F",
			"outcome": "done", "result": f.result}
	default:
		data = map[string]any{}
	}
	raw, _ := json.Marshal(data)
	return &adt.WSResponse{Success: true, Data: raw}, nil
}

func (f *gitFakeWS) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.actions...)
}

func gitServer(t *testing.T, cfg func(*Config)) (*Server, *gitFakeWS) {
	t.Helper()
	t.Chdir(t.TempDir())
	c := &Config{BaseURL: "https://dev.example.local:44300", Client: "100", Username: "TESTUSER", Password: "unused", Mode: "hyperfocused"}
	if cfg != nil {
		cfg(c)
	}
	s := NewServer(c)
	ws := &gitFakeWS{result: map[string]any{
		"outcome": "imported", "package": "$ZDEMO", "repo_key": "000000000007", "repo_name": "demo", "repo_created": true,
		"log":   []any{map[string]any{"type": "W", "text": "a warning", "obj_type": "PROG", "obj_name": "ZDEMO_REPORT"}},
		"tadir": []any{map[string]any{"pgmid": "R3TR", "object": "PROG", "obj_name": "ZDEMO_REPORT", "devclass": "$ZDEMO", "created": true}},
	}}
	s.gitWS = func(context.Context) (adt.GitService, error) {
		ws.mu.Lock()
		ws.dials++
		ws.mu.Unlock()
		return ws, nil
	}
	return s, ws
}

func gitDemoZip(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for n, c := range map[string]string{
		".abapgit.xml": `<?xml version="1.0" encoding="utf-8"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>` +
			`<MASTER_LANGUAGE>E</MASTER_LANGUAGE><STARTING_FOLDER>/src/</STARTING_FOLDER><FOLDER_LOGIC>PREFIX</FOLDER_LOGIC></DATA></asx:values></asx:abap>`,
		"src/package.devc.xml":          "<x/>",
		"src/zdemo_report.prog.abap":    "REPORT zdemo_report.",
		"src/zdemo_report.prog.xml":     "<x/>",
		"src/sub/zdemo_sub.prog.abap":   "REPORT zdemo_sub.",
		"src/sub/package.devc.xml":      "<x/>",
		"src/sub/zdemo_sub.prog.xml":    "<x/>",
		"src/sub/x/zdemo_x.prog.xml":    "<x/>",
		"src/sub/x/package.devc.xml":    "<x/>",
		"src/sub/x/zdemo_x.prog.abap":   "REPORT zdemo_x.",
		"src/sub/x/zdemo_x.prog.abap.b": "",
	} {
		f, _ := w.Create(n)
		_, _ = f.Write([]byte(c))
	}
	_ = w.Close()
	p := filepath.Join(t.TempDir(), "demo.zip")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func callGit(t *testing.T, s *Server, typ string, params map[string]any) *mcp.CallToolResult {
	t.Helper()
	p := map[string]any{"type": typ}
	for k, v := range params {
		p[k] = v
	}
	res, err := s.handleUniversalTool(context.Background(), newRequest(map[string]any{"action": "system", "params": p}))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Every gate refuses before the zip is read and before ZADT_VSP is dialled.
func TestGitImportZipGates(t *testing.T) {
	zipPath := gitDemoZip(t)
	cases := map[string]struct {
		cfg    func(*Config)
		params map[string]any
		want   string
	}{
		"read-only": {func(c *Config) { c.ReadOnly = true }, map[string]any{"package": "$ZDEMO", "file_path": zipPath}, "read-only"},
		"target not allowed": {func(c *Config) { c.AllowedPackages = []string{"$ZOTHER"} },
			map[string]any{"package": "$ZDEMO", "file_path": zipPath}, "blocked by safety"},
		"subpackage not allowed": {func(c *Config) { c.AllowedPackages = []string{"$ZDEMO", "$ZDEMO_SUB"} },
			map[string]any{"package": "$ZDEMO", "file_path": zipPath}, "$ZDEMO_SUB_X"},
		"transportable without opt-in": {nil, map[string]any{"package": "ZDEMO", "file_path": zipPath, "transport": "TRXK900001"}, "transportable"},
		"no zip":                       {nil, map[string]any{"package": "$ZDEMO"}, "zip is required"},
		"both":                         {nil, map[string]any{"package": "$ZDEMO", "file_path": zipPath, "zip_base64": "AAAA"}, "not both"},
		"no package":                   {nil, map[string]any{"file_path": zipPath}, "package is required"},
		"missing file":                 {nil, map[string]any{"package": "$ZDEMO", "file_path": filepath.Join(t.TempDir(), "none.zip")}, "zip:"},
		// The file does not exist: the refusal must come before it is read.
		"transportable, no transport, choice off": {func(c *Config) { c.AllowTransportableEdits = true; c.TransportChoice = "off" },
			map[string]any{"package": "ZDEMO", "file_path": filepath.Join(t.TempDir(), "none.zip")}, "name the transport"},
		"overwrite, deletes disabled": {func(c *Config) { c.DisallowedOps = "D" },
			map[string]any{"package": "$ZDEMO", "file_path": zipPath, "overwrite": true}, "blocked"},
	}
	for name, c := range cases {
		s, ws := gitServer(t, c.cfg)
		res := callGit(t, s, "git_import_zip", c.params)
		if !res.IsError || !strings.Contains(uploadResultText(res), c.want) {
			t.Errorf("%s: %s", name, uploadResultText(res))
		}
		if ws.dials != 0 || len(ws.calls()) != 0 {
			t.Errorf("%s: ZADT_VSP dialled (%d) or called %v before the refusal", name, ws.dials, ws.calls())
		}
	}
}

func TestGitImportZipHappyPath(t *testing.T) {
	s, ws := gitServer(t, func(c *Config) { c.AllowedPackages = []string{"$ZDEMO*"} })
	res := callGit(t, s, "git_import_zip", map[string]any{"package": "$zdemo", "file_path": gitDemoZip(t), "repo_name": "demo", "wait_seconds": 10.0})
	text := uploadResultText(res)
	if res.IsError {
		t.Fatalf("error: %s", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != "imported" || out["repoKey"] != "000000000007" || out["jobCount"] != "12345678" {
		t.Errorf("out %v", out)
	}
	if tadir, _ := out["tadir"].([]any); len(tadir) != 1 {
		t.Errorf("tadir %v", out["tadir"])
	}
	if log, _ := out["log"].([]any); len(log) != 1 {
		t.Errorf("log %v", out["log"])
	}
	calls := strings.Join(ws.calls(), ",")
	if !strings.HasPrefix(calls, "git.import_zip:begin,git.import_zip:chunk,git.import_zip:commit,git.import_status") {
		t.Errorf("calls %s", calls)
	}
}

// A refusal on the system is an error result that says what was refused.
func TestGitImportZipRefusalIsAnError(t *testing.T) {
	s, ws := gitServer(t, nil)
	ws.result = map[string]any{"outcome": "refused", "code": "REPO_EXISTS", "message": "Package $ZDEMO already versioned. Nothing was imported.", "package": "$ZDEMO"}
	data, _ := os.ReadFile(gitDemoZip(t))
	res := callGit(t, s, "git_import_zip", map[string]any{"package": "$ZDEMO", "zip_base64": base64.StdEncoding.EncodeToString(data)})
	text := uploadResultText(res)
	if !res.IsError || !strings.Contains(text, "REPO_EXISTS") || !strings.Contains(text, `"status": "refused"`) {
		t.Errorf("refusal: %v %s", res.IsError, text)
	}
}

func TestGitImportZipNoWait(t *testing.T) {
	s, ws := gitServer(t, nil)
	res := callGit(t, s, "git_import_zip", map[string]any{"package": "$ZDEMO", "file_path": gitDemoZip(t), "wait_seconds": 0.0})
	text := uploadResultText(res)
	if res.IsError || !strings.Contains(text, `"status": "pending"`) || !strings.Contains(text, "git_import_status job=12345678") {
		t.Errorf("no wait: %s", text)
	}
	for _, c := range ws.calls() {
		if c == "git.import_status" {
			t.Error("asked for the status although told not to wait")
		}
	}
}

func TestGitDeleteObjectsGates(t *testing.T) {
	cases := map[string]struct {
		cfg    func(*Config)
		params map[string]any
	}{
		"read-only":       {func(c *Config) { c.ReadOnly = true }, map[string]any{"package": "$ZDEMO", "objects": []any{"PROG ZDEMO_REPORT"}}},
		"not allowed":     {func(c *Config) { c.AllowedPackages = []string{"$ZOTHER"} }, map[string]any{"package": "$ZDEMO", "objects": []any{"PROG ZDEMO_REPORT"}}},
		"no objects":      {nil, map[string]any{"package": "$ZDEMO"}},
		"bad object":      {nil, map[string]any{"package": "$ZDEMO", "objects": []any{"ZDEMO_REPORT"}}},
		"transportable":   {func(c *Config) { c.AllowTransportableEdits = true }, map[string]any{"package": "ZDEMO", "objects": []any{"PROG ZDEMO_REPORT"}}},
		"delete disabled": {func(c *Config) { c.DisallowedOps = "D" }, map[string]any{"package": "$ZDEMO", "objects": []any{"PROG ZDEMO_REPORT"}}},
	}
	for name, c := range cases {
		s, ws := gitServer(t, c.cfg)
		res := callGit(t, s, "git_delete_objects", c.params)
		if !res.IsError {
			t.Errorf("%s: accepted: %s", name, uploadResultText(res))
		}
		if ws.dials != 0 {
			t.Errorf("%s: ZADT_VSP dialled before the refusal", name)
		}
	}
}

// The status read is allowed under read-only.
func TestGitImportStatusUnderReadOnly(t *testing.T) {
	s, _ := gitServer(t, func(c *Config) { c.ReadOnly = true })
	res := callGit(t, s, "git_import_status", map[string]any{"job": "12345678"})
	if res.IsError || !strings.Contains(uploadResultText(res), `"status": "imported"`) {
		t.Errorf("status: %s", uploadResultText(res))
	}
}

// A commit without an answer: the job may be running, and the answer says
// so, as an error, with what is known.
func TestGitImportZipCommitWithoutAnswer(t *testing.T) {
	s, ws := gitServer(t, nil)
	ws.commitErr = context.DeadlineExceeded
	res := callGit(t, s, "git_import_zip", map[string]any{"package": "$ZDEMO", "file_path": gitDemoZip(t)})
	text := uploadResultText(res)
	if !res.IsError || !strings.Contains(text, "may be running") || !strings.Contains(text, "SM37") || !strings.Contains(text, `"status": "unknown"`) {
		t.Errorf("got %s", text)
	}
	for _, c := range ws.calls() {
		if c == "git.import_status" {
			t.Error("asked for a status without a job number")
		}
	}
}

// delete_repo with an online repository is refused before anything is
// deleted, as a bool and as a string.
func TestGitDeleteObjectsRefusesAnOnlineRepository(t *testing.T) {
	for _, flag := range []any{true, "true"} {
		s, ws := gitServer(t, nil)
		ws.pkg = map[string]any{"package": "$ZDEMO", "exists": true,
			"objects": []any{map[string]any{"pgmid": "R3TR", "object": "PROG", "obj_name": "ZDEMO_REPORT", "devclass": "$ZDEMO"}},
			"repo":    map[string]any{"key": "000000000002", "name": "upstream", "offline": false}}
		res := callGit(t, s, "git_delete_objects", map[string]any{"package": "$ZDEMO", "objects": []any{"PROG ZDEMO_REPORT"}, "delete_repo": flag})
		if !res.IsError || !strings.Contains(uploadResultText(res), "online") {
			t.Errorf("%v: %s", flag, uploadResultText(res))
		}
		if got := strings.Join(ws.calls(), ","); got != "git.package_objects" {
			t.Errorf("%v: calls %s", flag, got)
		}
	}
}

// The connection lost while waiting is a failure with the job number, not
// a pending import whose wait ran out.
func TestGitImportZipWaitFailureIsAnError(t *testing.T) {
	s, ws := gitServer(t, nil)
	ws.statusErr = errors.New("not connected")
	res := callGit(t, s, "git_import_zip", map[string]any{"package": "$ZDEMO", "file_path": gitDemoZip(t), "wait_seconds": 10.0})
	text := uploadResultText(res)
	if !res.IsError || !strings.Contains(text, "not connected") || !strings.Contains(text, "12345678") || !strings.Contains(text, `"error"`) {
		t.Errorf("got %s", text)
	}
}

// keep_order, as a bool or the string "true", deletes in the order given;
// without it, or false, users go before what they use.
func TestGitDeleteOptionsKeepOrder(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{{nil, false}, {false, false}, {"false", false}, {true, true}, {"true", true}, {"TRUE", true}}
	for _, c := range cases {
		args := map[string]any{}
		if c.v != nil {
			args["keep_order"] = c.v
		}
		opts, err := gitDeleteOptions(args, "")
		if err != nil {
			t.Fatalf("%v: %v", c.v, err)
		}
		if opts.KeepOrder != c.want {
			t.Errorf("keep_order %#v: KeepOrder %t, want %t", c.v, opts.KeepOrder, c.want)
		}
	}
}
