// Package mcp provides the MCP server implementation for ABAP ADT tools.
// handlers_git_import.go imports an abapGit offline zip into a package, and
// deletes what such an import left, through ZADT_VSP's git domain.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// gitService is ZADT_VSP's git domain on this server's own connection.
// Tests replace it.
func (s *Server) gitService(ctx context.Context) (adt.GitService, error) {
	if s.gitWS != nil {
		return s.gitWS(ctx)
	}
	if err := s.ensureDebugWSClient(ctx); err != nil {
		return nil, fmt.Errorf("the abapGit import needs ZADT_VSP with ZCL_VSP_GIT_SERVICE and abapGit on the system (vsp install zadt-vsp): %w", err)
	}
	return s.debugWSClient, nil
}

const (
	gitImportDefaultWait = 5 * time.Minute
	gitImportMaxWait     = 30 * time.Minute
)

// handleGitImportZip imports an abapGit offline zip into a package:
//
//	SAP(action="system", params={"type": "git_import_zip", "file_path": "/in/demo.zip",
//	    "package": "$ZDEMO", "repo_name": "demo", "overwrite": false})
//
// or with "zip_base64" instead of file_path. Every gate runs before the zip
// is read or ZADT_VSP dialled: --read-only, the operation filter, the
// target package in --allowed-packages, and for a transportable package
// --allow-transportable-edits with a transport (named, or chosen as for any
// write). Then every package the zip maps a file to must be allowed too.
// The import runs as background job ZVSP_GIT_IMPORT; this waits for it
// (wait_seconds, default 300; 0 answers at once with the job) and answers
// with the outcome, the E/W/A log, the TADIR rows created or changed and the
// repository key. git_import_status reads a job that is still running.
func (s *Server) handleGitImportZip(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	pkg := getStringParam(args, "package")
	transport := strings.ToUpper(strings.TrimSpace(transportParam(args)))

	overwrite, _ := getBoolParam(args, "overwrite")
	if v := strings.ToLower(getStringParam(args, "overwrite")); v == "true" {
		overwrite = true
	}

	// Policy first, before the zip is even looked at.
	if err := s.adtClient.CheckGitImportPolicy(pkg, transport, overwrite); err != nil {
		return newToolResultError(err.Error()), nil
	}

	filePath := getStringParam(args, "file_path")
	b64 := getStringParam(args, "zip_base64")
	if b64 == "" {
		b64 = getStringParam(args, "base64")
	}
	var data []byte
	var err error
	switch {
	case filePath != "" && b64 != "":
		return newToolResultError("give the zip either as file_path or as zip_base64, not both"), nil
	case filePath != "":
		data, err = adt.ReadGitZip(filePath)
	case b64 != "":
		data, err = adt.DecodeGitZipBase64(b64)
	default:
		return newToolResultError("the zip is required: file_path (a local abapGit offline zip) or zip_base64"), nil
	}
	if err != nil {
		return newToolResultError("zip: " + err.Error()), nil
	}
	plan, err := adt.AnalyzeGitZip(data, pkg)
	if err != nil {
		return newToolResultError(err.Error()), nil
	}
	if err = s.adtClient.CheckGitImportPlan(plan); err != nil {
		return newToolResultError(err.Error()), nil
	}

	wait := gitImportDefaultWait
	if f, ok := getFloatParam(args, "wait_seconds"); ok {
		if f < 0 {
			return newToolResultError("wait_seconds must not be negative"), nil
		}
		wait = min(time.Duration(f*float64(time.Second)), gitImportMaxWait)
	}

	ws, err := s.gitService(ctx)
	if err != nil {
		return newToolResultError(err.Error()), nil
	}
	started, err := s.adtClient.StartGitImport(ctx, ws, data, adt.GitImportOptions{
		Package: pkg, RepoName: getStringParam(args, "repo_name"), Overwrite: overwrite, Transport: transport,
	})
	var unconfirmed *adt.GitImportUnconfirmedError
	if errors.As(err, &unconfirmed) {
		// The zip was sent and the commit got no answer: the job may be
		// running. What is known, as an error result.
		res := newToolResultJSON(map[string]any{
			"status":    adt.GitJobUnknown,
			"system":    started.System,
			"client":    started.Client,
			"package":   started.Package,
			"job":       started.Job,
			"transport": started.Transport,
			"packages":  plan.Packages,
			"note":      started.Note,
		})
		res.IsError = true
		return res, nil
	}
	if err != nil {
		return newToolResultError(err.Error()), nil
	}
	out := map[string]any{
		"status":    adt.GitJobPending,
		"system":    started.System,
		"client":    started.Client,
		"package":   started.Package,
		"job":       started.Job,
		"jobCount":  started.JobCount,
		"transport": started.Transport,
		"packages":  plan.Packages,
	}
	if started.Note != "" {
		out["note"] = started.Note
	}
	if wait == 0 {
		out["note"] = strings.TrimSpace(fmt.Sprintf("%v the import runs as job %s %s; its outcome: git_import_status job=%s",
			orDefault(started.Note, ""), started.Job, started.JobCount, started.JobCount))
		return newToolResultJSON(out), nil
	}
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	st, werr := s.adtClient.WaitGitImport(wctx, ws, started.JobCount)
	return gitImportStatusResult(out, st, werr), nil
}

// gitImportStatusResult folds a job's state into out: status is the
// import's outcome once there is one (imported, imported_with_errors,
// refused, failed), else the job's state (pending, failed, unknown). A
// refusal or a failure is an error result.
func gitImportStatusResult(out map[string]any, st *adt.GitImportStatus, werr error) *mcp.CallToolResult {
	if st != nil {
		out["status"] = st.State
		out["jobStatus"] = st.JobStatus
		if len(st.JobLog) > 0 {
			out["jobLog"] = st.JobLog
		}
		if st.Note != "" {
			out["note"] = st.Note
		}
		if r := st.Result; r != nil {
			out["status"] = r.Outcome
			out["code"] = r.Code
			out["message"] = r.Message
			out["repoKey"] = r.RepoKey
			out["repoName"] = r.RepoName
			out["repoCreated"] = r.RepoCreated
			out["packageCreated"] = r.PackageCreated
			out["log"] = r.Log
			out["tadir"] = r.Tadir
			out["decisions"] = r.Decisions
			if r.Transport != "" {
				out["transport"] = r.Transport
			}
		}
	}
	if werr != nil && !errors.Is(werr, context.DeadlineExceeded) {
		out["error"] = werr.Error()
	}
	res := newToolResultJSON(out)
	switch out["status"] {
	case adt.GitRefused, adt.GitFailed, adt.GitJobUnknown:
		res.IsError = true
	}
	if werr != nil && !errors.Is(werr, context.DeadlineExceeded) {
		res.IsError = true
	}
	return res
}

// handleGitImportStatus reads the state and result of an import job. It
// changes nothing.
//
//	SAP(action="system", params={"type": "git_import_status", "job": "12345678"})
func (s *Server) handleGitImportStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	job := strings.TrimSpace(getStringParam(args, "job"))
	if job == "" {
		job = strings.TrimSpace(getStringParam(args, "job_count"))
	}
	if job == "" {
		return newToolResultError("job is required: the number git_import_zip reported"), nil
	}
	ws, err := s.gitService(ctx)
	if err != nil {
		return newToolResultError(err.Error()), nil
	}
	st, err := s.adtClient.GitImportStatus(ctx, ws, job)
	if err != nil {
		return newToolResultError(err.Error()), nil
	}
	return gitImportStatusResult(map[string]any{"job": st.Job, "jobCount": st.JobCount}, st, nil), nil
}

// handleGitDeleteObjects deletes exactly the listed objects of a package,
// then, with delete_repo: true, the offline abapGit repository registered
// for it once the package is empty, then the package if it is empty and no
// repository is registered for it:
//
//	SAP(action="system", params={"type": "git_delete_objects", "package": "$ZDEMO",
//	    "objects": ["PROG ZDEMO_REPORT", "CLAS ZCL_DEMO"], "delete_repo": true})
//
// An object is deleted only if the package's TADIR has it; nothing outside
// the package, and no package but the emptied one itself, is ever deleted.
// Every delete goes through DeleteObject's gate. A repository registration
// is dropped only on delete_repo, only an offline one; an online one never
// (delete_repo with one is refused before anything is deleted).
func (s *Server) handleGitDeleteObjects(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	pkg := getStringParam(args, "package")
	transport := strings.ToUpper(strings.TrimSpace(transportParam(args)))
	if err := s.adtClient.CheckGitDelete(pkg, transport); err != nil {
		return newToolResultError(err.Error()), nil
	}
	raw, ok := args["objects"]
	if !ok {
		raw = args["object"]
	}
	items, err := adt.ParseGitDeleteItems(raw)
	if err != nil {
		return newToolResultError(err.Error()), nil
	}
	ws, err := s.gitService(ctx)
	if err != nil {
		return newToolResultError(err.Error()), nil
	}
	deleteRepo, _ := getBoolParam(args, "delete_repo")
	if v := strings.ToLower(getStringParam(args, "delete_repo")); v == "true" {
		deleteRepo = true
	}
	res, err := s.adtClient.DeleteGitObjects(ctx, ws, pkg, items, transport, deleteRepo)
	if err != nil {
		if res == nil {
			return newToolResultError(err.Error()), nil
		}
		out := newToolResultJSON(map[string]any{"error": err.Error(), "result": res})
		out.IsError = true
		return out, nil
	}
	return newToolResultJSON(res), nil
}
