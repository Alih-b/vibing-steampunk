package embedded

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// ZCL_VSP_TRANSPORT_SERVICE adds a request to an import buffer and must never
// be able to do more: no import, no other tp command, no file outside
// DIR_TRANS/cofiles and DIR_TRANS/data. These checks read the source that vsp
// install deploys (src/, which TestEmbeddedSourcesMatchSrc keeps equal to the
// embedded copy) and fail on any statement that would widen what it can do.

// transportServiceFunctions are the only function modules the service may
// call. Everything else -- TRINT_TP_INTERFACE, TMS_TP_IMPORT, the TMS_MGR_*
// and TMS_CI_* dispatchers, CTS_API_* -- is out.
var transportServiceFunctions = map[string]bool{
	"EPS_GET_DIRECTORY_PATH":     true,
	"EPS_GET_FILE_ATTRIBUTES":    true,
	"EPS_OPEN_OUTPUT_FILE":       true,
	"EPS_WRITE_BLOCK":            true,
	"EPS_CLOSE_FILE":             true,
	"EPS_DELETE_FILE":            true,
	"EPS_OPEN_INPUT_FILE":        true,
	"EPS2_GET_DIRECTORY_LISTING": true,
	"TMS_TP_MAINTAIN_BUFFER":     true,
	"TMS_TP_SHOW_BUFFER":         true,
	// The tp step runs as a background job: an APC session may not make the
	// synchronous RFC that starts tp.
	"JOB_OPEN":   true,
	"JOB_SUBMIT": true,
	"JOB_CLOSE":  true,
	// The job step's parameters: a protected variant per job (an APC
	// session may not SUBMIT), deleted once its job has ended.
	"RS_CREATE_VARIANT":    true,
	"RS_VARIANT_DELETE":    true,
	"GET_JOB_RUNTIME_INFO": true,
	// The request's lock (lock object E_TRKORR) around check-and-write.
	"ENQUEUE_E_TRKORR": true,
	"DEQUEUE_E_TRKORR": true,
	// add_status reads the job's log (read-only).
	"BP_JOBLOG_READ": true,
}

// tpCommands are tp commands (LSTPACON) and TMS buffer commands. Only
// ADDTOBUFFER may appear as a literal in the service, once.
var tpCommands = []string{
	"ADDTOBUFFER", "IMPORT", "IMPSYNC", "IMPND", "CMD", "R3I", "R3H", "TST",
	"IMPORTPREVIEW", "POSTLANGUAGEIMPORT", "DELIVER", "PUT", "VCSSYNCHRONIZE",
	"LOCKSYS", "UNLOCKSYS", "LOCK_EU", "UNLOCK_EU", "LOCK_DDL", "UNLOCK_DDL",
	"DELFROMBUFFER", "CLEANBUFFER", "PREPAREBUFFER", "FILLCLIENT", "MODCLIENT",
	"MODBUFFER", "SETSTOPMARK", "DELSTOPMARK", "MVSTOPMARK", "SETSYNCMARK",
	"DELSYNCMARK", "SETACTIVE", "LSETACTIVE", "SETINACTIVE", "LSETINACTIVE",
	"SUSPEND", "RECOVERBUFFER", "CHECKIN", "CHECKINMOVE", "CHECKOUT", "CLEAROLD",
	"WRITELOG", "SHOWBUFFER", "COUNT", "EXPORT", "EXPWBO", "CONNECT",
}

// abapStatements splits ABAP source into statements: comments removed,
// periods inside literals and templates ignored. A period outside them ends
// a statement even when code follows at once ("x = 1.DELETE ..."), unless
// it sits between two digits; inside a string template, \| and the other
// backslash escapes do not end it.
func abapStatements(src string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "*") {
			continue
		}
		rs := []rune(line)
		var quote rune
		for i := 0; i < len(rs); i++ {
			r := rs[i]
			if quote != 0 {
				cur.WriteRune(r)
				if quote == '|' && r == '\\' && i+1 < len(rs) {
					i++
					cur.WriteRune(rs[i])
					continue
				}
				if r == quote {
					quote = 0
				}
				continue
			}
			switch r {
			case '\'', '`', '|':
				quote = r
				cur.WriteRune(r)
			case '"':
				// Rest of the line is a comment.
				i = len(rs)
			case '.':
				if i > 0 && i+1 < len(rs) && unicode.IsDigit(rs[i-1]) && unicode.IsDigit(rs[i+1]) {
					cur.WriteRune(r)
					continue
				}
				out = append(out, strings.Join(strings.Fields(cur.String()), " "))
				cur.Reset()
			default:
				cur.WriteRune(r)
			}
		}
		cur.WriteRune(' ')
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, strings.Join(strings.Fields(s), " "))
	}
	return out
}

var (
	callFunctionRe = regexp.MustCompile(`(?i)^CALL FUNCTION (\S+)`)
	quotedLitRe    = regexp.MustCompile(`'([^']*)'|` + "`([^`]*)`")
	bindingRe      = func(name string) *regexp.Regexp {
		return regexp.MustCompile(`(?i)\b` + name + `\s*=\s*(\S+)`)
	}
)

// exportingParams returns the formal parameters bound in a CALL FUNCTION
// statement's EXPORTING section.
func exportingParams(upStmt string) []string {
	i := strings.Index(upStmt, " EXPORTING ")
	if i < 0 {
		return nil
	}
	sec := upStmt[i+len(" EXPORTING "):]
	for _, end := range []string{" IMPORTING ", " TABLES ", " CHANGING ", " EXCEPTIONS "} {
		if j := strings.Index(sec, end); j >= 0 {
			sec = sec[:j]
		}
	}
	var out []string
	for _, m := range regexp.MustCompile(`(\w+)\s*=`).FindAllStringSubmatch(sec, -1) {
		out = append(out, m[1])
	}
	return out
}

// methodStatements returns the statements of one method's implementation.
func methodStatements(stmts []string, method string) []string {
	var out []string
	in := false
	for _, st := range stmts {
		up := strings.ToUpper(st)
		switch {
		case up == "METHOD "+method:
			in = true
		case in && up == "ENDMETHOD":
			return out
		case in:
			out = append(out, st)
		}
	}
	return out
}

// checkTransportService returns every rule src breaks.
func checkTransportService(src string) []string {
	var bad []string
	stmts := abapStatements(src)
	maintain, show := 0, 0
	for _, st := range stmts {
		up := strings.ToUpper(st)
		if m := callFunctionRe.FindStringSubmatch(st); m != nil {
			name := m[1]
			if !strings.HasPrefix(name, "'") || !strings.HasSuffix(name, "'") {
				bad = append(bad, "dynamic CALL FUNCTION "+name+": the function called must be a literal")
				continue
			}
			name = strings.ToUpper(strings.Trim(name, "'"))
			// Every call runs here, in this system and this task: no RFC
			// destination (another system), no new task, no background or
			// update task.
			for _, kw := range []string{" DESTINATION ", " STARTING NEW TASK", " IN BACKGROUND TASK", " IN BACKGROUND UNIT", " IN UPDATE TASK"} {
				if strings.Contains(up+" ", kw) {
					bad = append(bad, "CALL FUNCTION '"+name+"' with"+kw+": every call must run here, synchronously")
				}
			}
			if !transportServiceFunctions[name] {
				bad = append(bad, "CALL FUNCTION '"+name+"' is not one the transport service may call")
			}
			exporting := up
			if i := strings.Index(up, " IMPORTING "); i >= 0 {
				exporting = up[:i]
			} else if i := strings.Index(up, " TABLES "); i >= 0 {
				exporting = up[:i]
			} else if i := strings.Index(up, " EXCEPTIONS "); i >= 0 {
				exporting = up[:i]
			}
			switch name {
			case "TMS_TP_MAINTAIN_BUFFER":
				maintain++
				if m := bindingRe("IV_TP_COMMAND").FindStringSubmatch(up); m == nil || m[1] != "'ADDTOBUFFER'" {
					bad = append(bad, "TMS_TP_MAINTAIN_BUFFER: iv_tp_command must be the literal 'ADDTOBUFFER'")
				}
				if m := bindingRe("IV_SYSTEM_NAME").FindStringSubmatch(up); m == nil || m[1] != "LV_SYSTEM" {
					bad = append(bad, "TMS_TP_MAINTAIN_BUFFER: iv_system_name must be lv_system (sy-sysid)")
				}
				// An allow-list, not a deny-list: exactly these three.
				for _, p := range exportingParams(up) {
					if p != "IV_TP_COMMAND" && p != "IV_SYSTEM_NAME" && p != "IV_REQUEST" {
						bad = append(bad, "TMS_TP_MAINTAIN_BUFFER: "+p+" must not be passed (only iv_tp_command, iv_system_name, iv_request)")
					}
				}
				if regexp.MustCompile(`\bTT_BUFFER\s*=`).MatchString(up) {
					bad = append(bad, "TMS_TP_MAINTAIN_BUFFER: TT_BUFFER must not be passed")
				}
			case "TMS_TP_SHOW_BUFFER":
				show++
				if m := bindingRe("IV_SYSTEM_NAME").FindStringSubmatch(up); m == nil || m[1] != "LV_SYSTEM" {
					bad = append(bad, "TMS_TP_SHOW_BUFFER: iv_system_name must be lv_system (sy-sysid)")
				}
				for _, p := range []string{"IV_CLEAR_LOCKS", "IV_READ_LOCKS", "IV_COUNT_ONLY"} {
					if regexp.MustCompile(`\b` + p + `\s*=`).MatchString(up) {
						bad = append(bad, "TMS_TP_SHOW_BUFFER: "+p+" must not be passed")
					}
				}
			case "RS_CREATE_VARIANT", "RS_VARIANT_DELETE":
				key := map[string]string{"RS_CREATE_VARIANT": "CURR_REPORT", "RS_VARIANT_DELETE": "REPORT"}[name]
				if m := bindingRe(key).FindStringSubmatch(up); m == nil || m[1] != "'ZVSP_TRANSPORT_BUFFER'" {
					bad = append(bad, name+": only variants of 'ZVSP_TRANSPORT_BUFFER'")
				}
			case "JOB_SUBMIT":
				if m := bindingRe("VARIANT").FindStringSubmatch(up); m == nil || m[1] != "LV_VARIANT" {
					bad = append(bad, "JOB_SUBMIT: variant must be lv_variant, the job's own")
				}
				// The step runs as the caller, or JOB_SUBMIT's default.
				for _, m := range bindingRe("AUTHCKNAM").FindAllStringSubmatch(up, -1) {
					if m[1] != "SY-UNAME" {
						bad = append(bad, "JOB_SUBMIT: authcknam must be sy-uname (or omitted)")
					}
				}
				// The job runs this service's own step and nothing else.
				if m := bindingRe("REPORT").FindStringSubmatch(up); m == nil || m[1] != "'ZVSP_TRANSPORT_BUFFER'" {
					bad = append(bad, "JOB_SUBMIT: report must be the literal 'ZVSP_TRANSPORT_BUFFER'")
				}
				for _, p := range []string{"COMMANDNAME", "EXTPGM_NAME", "EXTPGM_PARAM"} {
					if regexp.MustCompile(`\b` + p + `\s*=`).MatchString(up) {
						bad = append(bad, "JOB_SUBMIT: "+p+" must not be passed")
					}
				}
			case "EPS_OPEN_OUTPUT_FILE", "EPS_DELETE_FILE":
				// The directory is the logical one from dir_of, never a path.
				if m := bindingRe("DIR_NAME").FindStringSubmatch(exporting); m == nil || m[1] != "LV_DIR" {
					bad = append(bad, name+": dir_name must be lv_dir (from dir_of)")
				}
				if strings.Contains(exporting, "IV_LONG_DIR_NAME") {
					bad = append(bad, name+": iv_long_dir_name must not be passed (it would bypass $TR_COFI/$TR_DATA)")
				}
				if name == "EPS_OPEN_OUTPUT_FILE" {
					if m := bindingRe("OVERWRITE_MODE").FindStringSubmatch(exporting); m == nil || m[1] != "SPACE" {
						bad = append(bad, "EPS_OPEN_OUTPUT_FILE: overwrite_mode must be space (never overwrite)")
					}
				}
			}
		}
		// lv_system is the system tp is told to act on. It is declared,
		// set from sy-sysid, and handed to the two TMS calls -- nothing else.
		if regexp.MustCompile(`\bLV_SYSTEM\b`).MatchString(up) {
			switch {
			case regexp.MustCompile(`^DATA\b.*\bLV_SYSTEM TYPE STPA-SYSNAME\b`).MatchString(up):
			case up == "LV_SYSTEM = SY-SYSID":
			case callFunctionRe.MatchString(st) && strings.Count(up, "LV_SYSTEM") == 1 && bindingRe("IV_SYSTEM_NAME").FindStringSubmatch(up) != nil:
			default:
				bad = append(bad, "lv_system used other than declared, set from sy-sysid, or passed as iv_system_name: "+st)
			}
		}
		// No SUBMIT: an APC session may not execute one, and the job step is
		// scheduled through JOB_SUBMIT.
		if strings.HasPrefix(up, "SUBMIT ") || strings.Contains(up, " SUBMIT ") {
			bad = append(bad, "SUBMIT in the transport service: "+st)
		}
		// The variant that carries the step's parameters is protected.
		if strings.HasPrefix(up, "LS_VARID = VALUE #(") && !strings.Contains(up, "PROTECTED = 'X'") {
			bad = append(bad, "the job's variant must be protected: "+st)
		}
		// No cluster or table write: nothing is handed over through the
		// database.
		if strings.Contains(up, " TO DATABASE ") || strings.Contains(up, " FROM DATABASE ") {
			bad = append(bad, "database cluster access in the transport service: "+st)
		}
		// File statements outside the EPS layer: only reading is allowed.
		for _, kw := range []string{"DELETE DATASET", "TRANSFER ", "CALL 'SYSTEM'", "CALL TRANSACTION", "GENERATE SUBROUTINE", "INSERT REPORT", "EXEC SQL"} {
			if strings.HasPrefix(up, kw) || strings.Contains(up, " "+kw) {
				bad = append(bad, "statement not allowed in the transport service: "+st)
			}
		}
		if strings.HasPrefix(up, "OPEN DATASET") && !strings.Contains(up, " FOR INPUT ") {
			bad = append(bad, "OPEN DATASET other than FOR INPUT: "+st)
		}
	}
	if maintain != 1 {
		bad = append(bad, "TMS_TP_MAINTAIN_BUFFER must be called exactly once")
	}
	if show != 1 {
		bad = append(bad, "TMS_TP_SHOW_BUFFER must be called exactly once (the one buffer read)")
	}

	// Literals: one 'ADDTOBUFFER', no other tp command, and no $TR_ directory
	// but cofiles and data.
	var addLits int
	dirs := map[string]bool{}
	for _, st := range stmts {
		for _, m := range quotedLitRe.FindAllStringSubmatch(st, -1) {
			lit := strings.ToUpper(strings.TrimSpace(m[1] + m[2]))
			if lit == "ADDTOBUFFER" {
				addLits++
				continue
			}
			for _, cmd := range tpCommands {
				if lit == cmd {
					bad = append(bad, "tp command literal '"+cmd+"' in the transport service")
				}
			}
			if strings.HasPrefix(lit, "$TR") {
				dirs[lit] = true
			}
		}
	}
	if addLits != 1 {
		bad = append(bad, "the literal 'ADDTOBUFFER' must appear exactly once, as TMS_TP_MAINTAIN_BUFFER's command")
	}
	for d := range dirs {
		if d != "$TR_COFI" && d != "$TR_DATA" && d != "$TR_BUFF" {
			bad = append(bad, "logical directory "+d+" in the transport service: only $TR_COFI, $TR_DATA and (read) $TR_BUFF")
		}
	}

	// Writes and deletes take their directory from dir_of, and dir_of knows
	// cofiles and data only. The buffer directory is read, never written.
	dirOf := 0
	for _, st := range stmts {
		up := strings.ToUpper(st)
		if strings.HasPrefix(up, "RV_DIR =") {
			dirOf++
			if up != "RV_DIR = COND #( WHEN IV_KIND = `COFILE` THEN '$TR_COFI' ELSE '$TR_DATA' )" {
				bad = append(bad, "dir_of may map to $TR_COFI and $TR_DATA only: "+st)
			}
		}
		if strings.HasPrefix(up, "LV_DIR =") && !regexp.MustCompile(`^LV_DIR = DIR_OF\( \w+ \)$`).MatchString(up) {
			bad = append(bad, "lv_dir set other than from dir_of: "+st)
		}
	}
	if dirOf != 1 {
		bad = append(bad, "dir_of must have exactly one assignment")
	}

	// show_buffer and add_status read and do nothing else.
	readers := []string{"HANDLE_SHOW_BUFFER", "READ_BUFFER_FILE", "BUFFER_LINES", "HANDLE_ADD_STATUS"}
	reads := make([]string, 0, 64*len(readers))
	for _, m := range readers {
		reads = append(reads, methodStatements(stmts, m)...)
	}
	for _, st := range reads {
		up := strings.ToUpper(st)
		if m := callFunctionRe.FindStringSubmatch(st); m != nil {
			if n := strings.ToUpper(strings.Trim(m[1], "'")); n != "EPS_OPEN_INPUT_FILE" && n != "EPS_CLOSE_FILE" && n != "BP_JOBLOG_READ" {
				bad = append(bad, "a read path calls "+n+"; it may only read files and the job log")
			}
		}
		for _, kw := range []string{"START_JOB(", "DATABASE", "MS_PENDING", "COMMIT WORK", "INSERT ", "UPDATE ", "MODIFY ", "DELETE_FILE(", "ROLLBACK_WRITTEN("} {
			if strings.Contains(up, kw) {
				bad = append(bad, "a read path does more than read: "+st)
			}
		}
	}
	sort.Strings(bad)
	return bad
}

func transportServiceSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "src", "zcl_vsp_transport_service.clas.abap"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// checkTransportJobProgram returns every rule the job program breaks: it is a
// shim that calls run_job and does nothing else.
func checkTransportJobProgram(src string) []string {
	var bad []string
	for _, st := range abapStatements(src) {
		switch strings.ToUpper(st) {
		case "", "REPORT ZVSP_TRANSPORT_BUFFER", "START-OF-SELECTION",
			"PARAMETERS: P_REQ TYPE TRKORR, P_SHAC TYPE C LENGTH 44 LOWER CASE, P_SHAD TYPE C LENGTH 44 LOWER CASE, P_PUSH TYPE C LENGTH 60 LOWER CASE",
			"ZCL_VSP_TRANSPORT_SERVICE=>RUN_JOB( IV_REQUEST = P_REQ IV_COFILE_SHA = P_SHAC IV_DATA_SHA = P_SHAD IV_PUSH_ID = P_PUSH )":
		default:
			bad = append(bad, "statement in ZVSP_TRANSPORT_BUFFER beyond calling run_job: "+st)
		}
	}
	return bad
}

func TestTransportJobProgramIsAShim(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "src", "zvsp_transport_buffer.prog.abap"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range checkTransportJobProgram(string(b)) {
		t.Error(e)
	}
	if len(checkTransportJobProgram(string(b)+"\nPARAMETERS p_sys TYPE sysysid.\n")) == 0 {
		t.Error("the guard accepted another parameter in the job program")
	}
	if len(checkTransportJobProgram(strings.Replace(string(b), "iv_push_id = p_push ).", "iv_push_id = p_push ).\n  SUBMIT zother AND RETURN.", 1))) == 0 {
		t.Error("the guard accepted a SUBMIT in the job program")
	}
}

func TestTransportServiceOnlyAddsToBuffer(t *testing.T) {
	src := transportServiceSource(t)
	for _, b := range checkTransportService(src) {
		t.Error(b)
	}
	// And the copy vsp install deploys is the same class.
	for _, o := range GetObjects() {
		if o.Name == "ZCL_VSP_TRANSPORT_SERVICE" {
			if strings.ReplaceAll(o.Source, "\r\n", "\n") != strings.ReplaceAll(src, "\r\n", "\n") {
				t.Error("the embedded ZCL_VSP_TRANSPORT_SERVICE differs from src/: go generate ./embedded/abap")
			}
			return
		}
	}
	t.Error("ZCL_VSP_TRANSPORT_SERVICE is not among the objects vsp install deploys")
}

// TestTransportServiceGuardBites mutates the real source the ways a change
// could widen it, and requires the guard to object to each.
func TestTransportServiceGuardBites(t *testing.T) {
	src := transportServiceSource(t)
	mutations := map[string]struct{ old, new string }{
		"another tp command":                      {`iv_tp_command      = 'ADDTOBUFFER'`, `iv_tp_command      = 'IMPORT'`},
		"command from a variable":                 {`iv_tp_command      = 'ADDTOBUFFER'`, `iv_tp_command      = lv_cmd`},
		"another system":                          {"iv_system_name     = lv_system\n          iv_request", "iv_system_name     = lv_sid\n          iv_request"},
		"lv_system not sy-sysid":                  {"    lv_system = sy-sysid.\n\n    CALL FUNCTION 'GET_JOB_RUNTIME_INFO'", "    lv_system = lv_sid.\n\n    CALL FUNCTION 'GET_JOB_RUNTIME_INFO'"},
		"lv_system changed later":                 {"    lv_trkorr = ls_res-request.\n", "    lv_trkorr = ls_res-request.\n    CONCATENATE lv_sid space INTO lv_system.\n"},
		"tp options":                              {"iv_request         = lv_trkorr\n", "iv_request         = lv_trkorr\n              iv_tp_options      = lv_msg\n"},
		"tp step on another system (DESTINATION)": {"      CALL FUNCTION 'TMS_TP_MAINTAIN_BUFFER'\n", "      CALL FUNCTION 'TMS_TP_MAINTAIN_BUFFER' DESTINATION 'NONE'\n"},
		"job step as another user":                {"        authcknam         = sy-uname\n", "        authcknam         = 'DDIC'\n"},
		"tp parameter outside the allow-list":     {"iv_request         = lv_trkorr\n", "iv_request         = lv_trkorr\n              iv_prid_text       = lv_msg\n"},
		"buffer read in a new task":               {"    CALL FUNCTION 'TMS_TP_SHOW_BUFFER'\n", "    CALL FUNCTION 'TMS_TP_SHOW_BUFFER' STARTING NEW TASK 'T'\n"},
		"job runs another report":                 {"        report            = 'ZVSP_TRANSPORT_BUFFER'\n", "        report            = 'RSBDCSUB'\n"},
		"variant not protected":                   {"environmnt = 'B' protected = 'X'", "environmnt = 'B' protected = space"},
		"a SUBMIT after all":                      {"    ev_jobcount = lv_jobcount.\n", "    ev_jobcount = lv_jobcount.\n    SUBMIT ('ZVSP_TRANSPORT_BUFFER') VIA JOB lv_jobname NUMBER lv_jobcount AND RETURN.\n"},
		"another report's variant":                {"        curr_report               = 'ZVSP_TRANSPORT_BUFFER'\n", "        curr_report               = 'RSBDCSUB'\n"},
		"a ticket through the database":           {"    ev_jobcount = lv_jobcount.\n", "    ev_jobcount = lv_jobcount.\n    EXPORT p = lv_req TO DATABASE indx(zt) ID lv_req.\n"},
		"a second tp FM":                          {"    CALL FUNCTION 'TMS_TP_SHOW_BUFFER'", "    CALL FUNCTION 'TRINT_TP_INTERFACE'"},
		"dynamic call":                            {"CALL FUNCTION 'EPS_DELETE_FILE'", "CALL FUNCTION lv_fm"},
		"overwrite":                               {"overwrite_mode         = space", "overwrite_mode         = 'F'"},
		"a caller's directory":                    {"dir_name               = lv_dir\n            overwrite_mode", "dir_name               = lv_dir\n            iv_long_dir_name       = lv_path\n            overwrite_mode"},
		"another logical dir":                     {"THEN '$TR_COFI' ELSE '$TR_DATA'", "THEN '$TR_COFI' ELSE '$TR_BUFFER'"},
		"dir_of maps the buffer dir":              {"THEN '$TR_COFI' ELSE '$TR_DATA'", "THEN '$TR_COFI' ELSE '$TR_BUFF'"},
		"show_buffer starts a job":                {"    lv_name = sy-sysid.\n    lv_buffer_dir", "    start_job( EXPORTING iv_action = `SHOW` iv_request = lv_request ).\n    lv_name = sy-sysid.\n    lv_buffer_dir"},
		"show_buffer reads via tp":                {"    lv_name = sy-sysid.\n    lv_buffer_dir", "    CALL FUNCTION 'TMS_TP_SHOW_BUFFER' EXPORTING iv_system_name = lv_system.\n    lv_name = sy-sysid.\n    lv_buffer_dir"},
		"plain file write":                        {"CLOSE DATASET lv_dataset.\n          ELSE.", "CLOSE DATASET lv_dataset.\n            TRANSFER lv_chunk TO lv_dataset.\n          ELSE."},
		"show buffer clears locks": {"iv_system_name     = lv_system\n      IMPORTING\n        ev_tp_cmd_strg     = lv_cmd\n        ev_tp_ret_code     = lv_rc\n        ev_tp_message      = lv_msg\n      TABLES\n        tt_stdout          = et_stdout",
			"iv_system_name     = lv_system\n        iv_clear_locks     = 'X'\n      IMPORTING\n        ev_tp_cmd_strg     = lv_cmd\n        ev_tp_ret_code     = lv_rc\n        ev_tp_message      = lv_msg\n      TABLES\n        tt_stdout          = et_stdout"},
	}
	if len(checkTransportService(src)) != 0 {
		t.Fatal("the unmutated source must pass first")
	}
	for name, m := range mutations {
		if !strings.Contains(src, m.old) {
			t.Errorf("%s: the source no longer contains %q; update the mutation", name, m.old)
			continue
		}
		mutated := strings.Replace(src, m.old, m.new, 1)
		if len(checkTransportService(mutated)) == 0 {
			t.Errorf("%s: the guard accepted the mutated source", name)
		}
	}
}

// step=abort discards only the upload it names: an abort with another
// assembly id (a stale client, a second one) must not clear the assembly.
func TestTransportServiceAbortChecksAssemblyID(t *testing.T) {
	stmts := abapStatements(transportServiceSource(t))
	for _, st := range methodStatements(stmts, "HANDLE_UPLOAD_FILES") {
		if strings.Contains(strings.ToUpper(st), "CLEAR MS_ASSEMBLY") {
			t.Errorf("handle_upload_files clears the assembly itself: %s", st)
		}
	}
	body := methodStatements(stmts, "UPLOAD_ABORT")
	guard, clear := -1, -1
	for i, st := range body {
		up := strings.ToUpper(st)
		if guard < 0 && strings.HasPrefix(up, "IF MS_ASSEMBLY-ID IS INITIAL OR LV_ID <> MS_ASSEMBLY-ID") {
			guard = i
		}
		if clear < 0 && up == "CLEAR MS_ASSEMBLY" {
			clear = i
		}
	}
	if guard < 0 || clear < 0 || clear < guard {
		t.Errorf("upload_abort must check the assembly id before clearing: guard at %d, clear at %d", guard, clear)
	}
}

// show_buffer must not take a directory or attribute read failure for "no
// buffer file": that would report an empty queue that may not be empty.
// Absence comes only from probe_file, which concludes it only from a
// directory listing without the file.
func TestShowBufferReportsReadFailures(t *testing.T) {
	stmts := abapStatements(transportServiceSource(t))
	show := strings.ToUpper(strings.Join(append(methodStatements(stmts, "HANDLE_SHOW_BUFFER"), methodStatements(stmts, "READ_BUFFER_FILE")...), "\n"))
	if !strings.Contains(show, "PROBE_FILE(") {
		t.Error("the buffer file read does not use probe_file")
	}
	for _, bad := range []string{"LV_SUBRC <> 7", "LV_SUBRC = 7", "LV_SUBRC = 8", "LV_SUBRC <> 8"} {
		if strings.Contains(show, bad) {
			t.Errorf("show_buffer special-cases an EPS exception (%s)", bad)
		}
	}
	probe := strings.ToUpper(strings.Join(methodStatements(stmts, "PROBE_FILE"), "\n"))
	if !strings.Contains(probe, "CALL FUNCTION 'EPS2_GET_DIRECTORY_LISTING'") ||
		!strings.Contains(probe, "IF LV_SUBRC = 7 OR ( LV_SUBRC = 0 AND NOT LINE_EXISTS( LT_LIST[ NAME = LV_NAME ] ) )") {
		t.Error("probe_file does not conclude absence from a directory listing")
	}
	exists := strings.ToUpper(strings.Join(methodStatements(stmts, "FILE_EXISTS"), "\n"))
	if !strings.Contains(exists, "PROBE_FILE(") {
		t.Error("file_exists does not use probe_file")
	}
}

// The add is asynchronous: no pending slot that a lost answer could leave
// BUSY, no polled result store; an upload committed and never handed to a
// job is rolled back when abandoned; and "queued" is only ever concluded
// from a buffer read.
func TestTransportServiceAsyncOutcome(t *testing.T) {
	src := transportServiceSource(t)
	up := strings.ToUpper(src)
	for _, gone := range []string{"MS_PENDING", "'BUSY'", "BUFFER_RESULT", "INDX(ZU)"} {
		if strings.Contains(up, gone) {
			t.Errorf("%s is still in the service", gone)
		}
	}
	stmts := abapStatements(src)
	for _, m := range []string{"ZIF_VSP_SERVICE~ON_DISCONNECT", "UPLOAD_BEGIN"} {
		if !strings.Contains(strings.ToUpper(strings.Join(methodStatements(stmts, m), "\n")), "ROLLBACK_WRITTEN( )") {
			t.Errorf("%s does not roll back an abandoned upload", m)
		}
	}
	// run_job: every "queued" follows a buffer read showing the request.
	job := methodStatements(stmts, "RUN_JOB")
	queued := 0
	for i, st := range job {
		if strings.ToUpper(st) == "LS_RES-OUTCOME = `QUEUED`" {
			queued++
			prev := strings.ToUpper(strings.Join(job[max(0, i-2):i], "\n"))
			if !strings.Contains(prev, "LINE_EXISTS( LT_BUFFER[ TRKORR = LV_TRKORR ] )") && !strings.Contains(prev, "IF LS_RES-IN_BUFFER = ABAP_TRUE") {
				t.Errorf("run_job concludes queued without the buffer: %s", prev)
			}
		}
	}
	if queued == 0 {
		t.Error("run_job never concludes queued")
	}
	// add_status: queued only WHEN the buffer file has the request.
	status := strings.ToUpper(strings.Join(methodStatements(stmts, "HANDLE_ADD_STATUS"), "\n"))
	if n := strings.Count(status, "`QUEUED`"); n != 2 || strings.Count(status, "WHEN LV_IN_BUFFER = ABAP_TRUE THEN `QUEUED`") != 2 {
		t.Errorf("add_status says queued other than from the buffer file (%d)", n)
	}
}

// The job gets its work as step parameters bound at scheduling (no INDX
// ticket), and adds only files that still have the SHA-256 the upload wrote.
func TestJobChecksFilesBeforeAdd(t *testing.T) {
	stmts := abapStatements(transportServiceSource(t))
	start := strings.ToUpper(strings.Join(methodStatements(stmts, "START_JOB"), "\n"))
	for _, want := range []string{"SELNAME = 'P_REQ'", "LOW = IV_REQUEST", "SELNAME = 'P_SHAC'", "LOW = SHA_B64( IV_COFILE_SHA )",
		"SELNAME = 'P_SHAD'", "LOW = SHA_B64( IV_DATA_SHA )", "VARIANT = LV_VARIANT"} {
		if !strings.Contains(start, want) {
			t.Errorf("start_job does not bind %q to the job step", want)
		}
	}
	job := methodStatements(stmts, "RUN_JOB")
	check, add := -1, -1
	for i, st := range job {
		up := strings.ToUpper(st)
		if check < 0 && strings.Contains(up, "SHA_B64( SHA256( LV_CONTENT ) ) <>") {
			check = i
		}
		if add < 0 && strings.HasPrefix(up, "CALL FUNCTION 'TMS_TP_MAINTAIN_BUFFER'") {
			add = i
		}
	}
	if check < 0 || add < 0 || check > add {
		t.Errorf("run_job must compare the files' SHA-256 before ADDTOBUFFER (check at %d, add at %d)", check, add)
	}
}

// indexOf returns the index of the first statement containing sub, or -1.
func indexOf(stmts []string, sub string) int {
	for i, st := range stmts {
		if strings.Contains(strings.ToUpper(st), sub) {
			return i
		}
	}
	return -1
}

// Check-and-write and check-and-add hold the request's lock; deletes take
// back only files that still have the SHA-256 this upload wrote, and report
// a failed delete (review round 2, item 6; codex #2).
func TestTransportServiceLocksAndCheckedDeletes(t *testing.T) {
	stmts := abapStatements(transportServiceSource(t))
	commit := methodStatements(stmts, "UPLOAD_COMMIT")
	lock, write, unlock := indexOf(commit, "LOCK_REQUEST( LS_UP-REQUEST )"), indexOf(commit, "WRITE_PAIR("), indexOf(commit, "UNLOCK_REQUEST( LS_UP-REQUEST )")
	if lock < 0 || write < lock || unlock < write {
		t.Errorf("upload_commit: lock %d, write_pair %d, unlock %d -- the writes must be inside the lock", lock, write, unlock)
	}
	pair := methodStatements(stmts, "WRITE_PAIR")
	if c, w := indexOf(pair, "FILE_EXISTS("), indexOf(pair, "WRITE_FILE("); c < 0 || w < c {
		t.Errorf("write_pair must check absence before writing (check %d, write %d)", c, w)
	}

	job := methodStatements(stmts, "RUN_JOB")
	jl, jr, ja, ju := indexOf(job, "LOCK_REQUEST( IV_REQUEST = LV_TRKORR"), indexOf(job, "READ_BUFFER("), indexOf(job, "CALL FUNCTION 'TMS_TP_MAINTAIN_BUFFER'"), indexOf(job, "UNLOCK_REQUEST( LV_TRKORR )")
	if jl < 0 || jr < jl || ja < jr || ju < ja {
		t.Errorf("run_job: lock %d, buffer read %d, add %d, unlock %d -- check and add must be inside the lock", jl, jr, ja, ju)
	}

	del := strings.ToUpper(strings.Join(methodStatements(stmts, "DELETE_FILE"), "\n"))
	if !strings.Contains(del, "IF SHA256( LV_CONTENT ) <> TO_UPPER( IV_SHA )") {
		t.Error("delete_file does not compare the SHA-256 before deleting")
	}
	if !regexp.MustCompile(`CALL FUNCTION 'EPS_DELETE_FILE'[^\n]*\nIF SY-SUBRC <> 0\nRV_ERROR =`).MatchString(del) {
		t.Error("delete_file does not check EPS_DELETE_FILE's result")
	}
	for _, m := range []string{"ROLLBACK_WRITTEN", "RUN_JOB"} {
		for _, st := range methodStatements(stmts, m) {
			if up := strings.ToUpper(st); strings.Contains(up, "DELETE_FILE(") && !strings.Contains(up, "IV_SHA =") {
				t.Errorf("%s deletes without the SHA-256 it wrote: %s", m, st)
			}
		}
	}
}

// The push: each WebSocket binds to its own extension of AMC ZVSP_TRANSPORT
// /buffer when it opens; the job publishes there, on that extension only,
// after every outcome it logs.
func TestTransportPush(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "src", "zcl_vsp_apc_handler.clas.abap"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.ToUpper(strings.Join(methodStatements(abapStatements(string(b)), "IF_APC_WSP_EXTENSION~ON_START"), "\n"))
	if !strings.Contains(start, "BIND_AMC_MESSAGE_CONSUMER( I_APPLICATION_ID = 'ZVSP_TRANSPORT' I_CHANNEL_ID = '/BUFFER' I_CHANNEL_EXTENSION_ID = CONV #( MV_SESSION_ID ) )") {
		t.Error("on_start does not bind the WebSocket to its own extension of ZVSP_TRANSPORT /buffer")
	}
	// Any failure of the bind -- not only cx_apc_error -- leaves the session
	// up without push (critic, round 3 #1).
	if !regexp.MustCompile(`BIND_AMC_MESSAGE_CONSUMER\([^\n]*\)\nLV_PUSH = ABAP_TRUE\nCATCH CX_ROOT( ##CATCH_ALL)?\nLV_PUSH = ABAP_FALSE\nENDTRY`).MatchString(start) {
		t.Errorf("on_start must catch cx_root around the AMC bind and go on without push:\n%s", start)
	}

	src := transportServiceSource(t)
	stmts := abapStatements(src)
	if n := strings.Count(strings.ToUpper(src), "CREATE_MESSAGE_PRODUCER("); n != 1 {
		t.Errorf("%d AMC producers; want the one in publish", n)
	}
	pub := strings.ToUpper(strings.Join(methodStatements(stmts, "PUBLISH"), "\n"))
	if !strings.Contains(pub, "I_APPLICATION_ID = C_AMC_APP I_CHANNEL_ID = C_AMC_CHANNEL I_CHANNEL_EXTENSION_ID = CONV #( IV_PUSH_ID )") {
		t.Error("publish must send on ZVSP_TRANSPORT /buffer, on the uploading session's extension")
	}
	job := methodStatements(stmts, "RUN_JOB")
	for i, st := range job {
		if strings.ToUpper(st) == "JOB_LOG( LS_RES )" && (i+1 >= len(job) || !strings.HasPrefix(strings.ToUpper(job[i+1]), "PUBLISH( IS_RESULT = LS_RES")) {
			t.Errorf("run_job logs an outcome at statement %d without publishing it", i)
		}
	}
}

// The AMC application lets the transport service send and the APC handler
// bind, and no one else.
func TestAMCApplicationDefinition(t *testing.T) {
	d := AMCApplicationDefinition
	for _, want := range []string{"<AMC_APPL>ZVSP_TRANSPORT</AMC_APPL>", "<CHANNEL_ID>/buffer</CHANNEL_ID>", "<MESSAGE_TYPE_ID>TEXT</MESSAGE_TYPE_ID>",
		"<OBJ_NAME>ZCL_VSP_TRANSPORT_SERVICE</OBJ_NAME><ACTIVITY>S</ACTIVITY>", "<OBJ_NAME>ZCL_VSP_APC_HANDLER</OBJ_NAME><ACTIVITY>C</ACTIVITY>"} {
		if !strings.Contains(d, want) {
			t.Errorf("definition lacks %s", want)
		}
	}
	if n := strings.Count(d, "<AMC_ADTCONTENTAUTHORITIES>"); n != 2 {
		t.Errorf("%d authorities; want exactly two", n)
	}
}

// The job step runs only with its own variant: VSP<job number>, protected,
// created by the user the job runs as, changed by no one else. That is
// checked before anything else -- no file read, no lock, no tp call
// (critic, round 3 #2).
func TestJobRunsOnlyWithItsOwnVariant(t *testing.T) {
	job := methodStatements(abapStatements(transportServiceSource(t)), "RUN_JOB")
	check := indexOf(job, "IF SY-SLSET <> LV_OWN_VARIANT OR LV_VARIANT_FOUND = ABAP_FALSE OR LS_VARID-PROTECTED <> 'X' OR LS_VARID-ENAME <> SY-UNAME OR ( LS_VARID-AENAME IS NOT INITIAL AND LS_VARID-AENAME <> SY-UNAME )")
	if check < 0 {
		t.Fatal("run_job does not check that it runs with its own protected variant")
	}
	if own := indexOf(job, "DATA(LV_OWN_VARIANT) = CONV RSVAR-VARIANT( |VSP{ LV_JOBCOUNT }| )"); own < 0 || own > check {
		t.Error("the variant checked must be VSP<own job number>")
	}
	if sel := indexOf(job, "FROM VARID"); sel < 0 || sel > check {
		t.Error("the variant's protection and owner must be read from VARID before the check")
	}
	for _, later := range []string{"READ_DIR_FILE(", "LOCK_REQUEST(", "READ_BUFFER(", "CALL FUNCTION 'TMS_TP_MAINTAIN_BUFFER'", "DELETE_FILE("} {
		if i := indexOf(job, later); i >= 0 && i < check {
			t.Errorf("%s comes before the variant check", later)
		}
	}
	// The refusal returns at once.
	if check+6 >= len(job) || strings.ToUpper(job[check+6]) != "RETURN" {
		t.Errorf("the variant check does not refuse: %v", job[check:min(len(job), check+7)])
	}
}

// A rollback deletes the data file only once the cofile is gone, so a cofile
// is never left without its data file; and rollback_written reports what it
// did -- the lock's and each delete's result -- instead of assuming it
// (critic, round 3 #3 and #4).
func TestRollbackKeepsPairsAndTellsTheTruth(t *testing.T) {
	stmts := abapStatements(transportServiceSource(t))
	for method, guard := range map[string]string{"RUN_JOB": "IF LV_DEL1 IS INITIAL", "ROLLBACK_WRITTEN": "IF LV_DEL1 IS INITIAL", "WRITE_PAIR": "IF LV_CLEANUP IS INITIAL"} {
		body := methodStatements(stmts, method)
		for i, st := range body {
			up := strings.ToUpper(st)
			if strings.Contains(up, "DELETE_FILE( IV_KIND = `DATA`") && strings.Contains(up, "IV_SHA") {
				if i == 0 || strings.ToUpper(body[i-1]) != guard {
					t.Errorf("%s deletes the data file without first making sure the cofile is gone: %s", method, st)
				}
			}
		}
	}
	rb := strings.ToUpper(strings.Join(methodStatements(stmts, "ROLLBACK_WRITTEN"), "\n"))
	for _, want := range []string{"DATA(LV_LOCK) = LOCK_REQUEST( MS_WRITTEN-REQUEST )", "IF LV_LOCK IS NOT INITIAL",
		"EV_ROLLED_BACK = XSDBOOL( LV_DEL1 IS INITIAL AND LV_DEL2 IS INITIAL )"} {
		if !strings.Contains(rb, want) {
			t.Errorf("rollback_written lacks %q", want)
		}
	}
	add := strings.ToUpper(strings.Join(methodStatements(stmts, "HANDLE_ADD_TO_BUFFER"), "\n"))
	if !strings.Contains(add, "WHEN LV_ROLLED_BACK = ABAP_TRUE THEN `ADD_FAILED_ROLLED_BACK` ELSE `ADD_FAILED_FILES_KEPT`") {
		t.Error("add_to_buffer claims a rollback without checking it happened")
	}
}

// The ABAP cofile check, like the Go one, requires all thirteen header
// fields (PR #296 review).
func TestValidateCofileHeaderFieldsABAP(t *testing.T) {
	body := strings.ToUpper(strings.Join(methodStatements(abapStatements(transportServiceSource(t)), "VALIDATE_COFILE"), "\n"))
	if !strings.Contains(body, "IF LINES( LT_TOK ) < 13") {
		t.Error("validate_cofile does not require 13 header fields")
	}
	if !strings.Contains(body, "LOOP AT LT_TOK INTO DATA(LV_COUNT) FROM 5 TO 13") {
		t.Error("validate_cofile does not check the nine object counts")
	}
}

// write_pair tells a complete cleanup from an incomplete one, so the client
// never takes "nothing was left" for granted (PR #296 review).
func TestWritePairReportsFilesLeft(t *testing.T) {
	body := strings.ToUpper(strings.Join(methodStatements(abapStatements(transportServiceSource(t)), "WRITE_PAIR"), "\n"))
	for _, want := range []string{
		"EV_CODE = COND #( WHEN LV_CLEANUP IS INITIAL THEN `WRITE_FAILED` ELSE `WRITE_FAILED_FILES_LEFT` )",
		"EV_CODE = COND #( WHEN LV_CLEANUP IS INITIAL AND LV_CLEANUP2 IS INITIAL THEN `WRITE_FAILED` ELSE `WRITE_FAILED_FILES_LEFT` )",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("write_pair lacks %s", want)
		}
	}
}

// The status call believes a job number only for its own request: the job's
// log, or before that its variant's text, must name the request; another
// request's job is refused, an untied one gives no verdict. The job checks
// that its variant's text agrees with P_REQ (PR #296 review).
func TestStatusTiesJobToRequest(t *testing.T) {
	stmts := abapStatements(transportServiceSource(t))
	st := strings.ToUpper(strings.Join(methodStatements(stmts, "HANDLE_ADD_STATUS"), "\n"))
	for _, want := range []string{
		"IF LS_LOG-TEXT CP 'VSP REQUEST=*'",
		"INTO DATA(LV_VSP) DATA(LV_REQ_WORD) DATA(LV_TAIL)",
		"LV_TIED_TO = TO_UPPER( SUBSTRING_AFTER( VAL = LV_REQ_WORD SUB = '=' ) )",
		"FROM VARIT",
		"IF LV_TIED_TO IS NOT INITIAL AND LV_TIED_TO <> LV_REQUEST",
		"IV_CODE = 'JOB_NOT_FOR_REQUEST'",
		"ELSEIF LV_JOB_FOUND = ABAP_FALSE OR LV_TIED_TO IS INITIAL",
	} {
		if !strings.Contains(st, want) {
			t.Errorf("add_status lacks %s", want)
		}
	}
	job := strings.ToUpper(strings.Join(methodStatements(stmts, "RUN_JOB"), "\n"))
	if !strings.Contains(job, "IF SY-SUBRC <> 0 OR LV_VTEXT <> |VSP UPLOAD { TO_UPPER( IV_REQUEST ) }|") {
		t.Error("run_job does not check that its variant's text names its request")
	}
	log := strings.ToUpper(strings.Join(methodStatements(stmts, "JOB_LOG"), "\n"))
	if !strings.Contains(log, "LV_LINE = |VSP REQUEST={ IS_RESULT-REQUEST } OUTCOME=") {
		t.Error("the job log does not name the request first")
	}
}
