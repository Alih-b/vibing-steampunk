// Package fakesap is a scripted SAP system for tests that compare what the CLI
// and the MCP server say about the same landscape.
//
// It exists because cmd/vsp and internal/mcp read packages, sources, TADIR,
// TFDIR, the cross-reference tables and D010INC through the same client, and a
// golden test of either is only worth something if both are pointed at the same
// data. One fake, one world, two front ends.
//
// It answers the handful of ADT resources the graph, boundary and health paths
// touch, and nothing else: an unexpected request is answered 404 and recorded,
// so a golden file shows it rather than a test passing over it.
package fakesap

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Object is one repository object of the world.
type Object struct {
	Type    string // TADIR object type: CLAS, PROG, INTF, TABL, ...
	Name    string
	Package string
	// Source is served at the object's source/main. Empty means the object has
	// no source resource.
	Source string
	// SourceStatus, when non-zero, is the HTTP status the source read answers
	// with instead of the source.
	SourceStatus int
	// Revised is the RFC3339 date of the newest version in the revision feed.
	// Empty serves a feed with no entries.
	Revised string
}

// World is the landscape the fake serves.
type World struct {
	Objects []Object
	// Packages maps each package to its parent, for TDEVC. The root's parent is "".
	Packages map[string]string
	// FuncModules maps a function module to its main program (TFDIR.PNAME).
	FuncModules map[string]string
	// Groups maps a function group to its package (TADIR R3TR FUGR).
	Groups map[string]string
	// TVARVCReaders are the includes WBCROSSGT (OTYPE TY) and CROSS (TYPE S)
	// list as reading TVARVC; the first slice is WBCROSSGT, the second CROSS.
	TVARVCReaders [2][]string
	// D010INC rows: master, include.
	D010INC [][2]string
	// Fail lists substrings of a free-SQL statement (case-insensitive) whose
	// query is refused with 500, as a blocked or unauthorised query would be.
	Fail []string
	// UnitTests maps an object or package URI to the unit-test answer:
	// "fail" (one class, two methods, one alert), "pass", or "deny" (403).
	UnitTests map[string]string
	// ATCFindings is the priorities of the findings every ATC run returns.
	ATCFindings []int
	// ATCDeny refuses the ATC run.
	ATCDeny bool
}

// Server is a running fake.
type Server struct {
	*httptest.Server
	world World

	mu  sync.Mutex
	log []string
}

// New starts a fake serving w. It is closed when the test ends.
func New(t testing.TB, w World) *Server {
	t.Helper()
	s := &Server{world: w}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Log returns what the fake was asked, one line per request, canonicalised so
// that it does not depend on map iteration order in the caller: every SQL
// IN-list is exploded into one line per literal, and the lines are sorted.
func (s *Server) Log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.log...)
	sort.Strings(out)
	return out
}

func (s *Server) record(line string) {
	s.mu.Lock()
	s.log = append(s.log, line)
	s.mu.Unlock()
}

var inList = regexp.MustCompile(`(?i)\bIN\s*\(([^)]*)\)`)

// canonicalSQL explodes IN-lists so that a batch's composition, which follows
// Go map order in the callers, does not leak into the log.
func canonicalSQL(sql string) []string {
	sql = strings.Join(strings.Fields(sql), " ")
	m := inList.FindStringSubmatchIndex(sql)
	if m == nil {
		return []string{sql}
	}
	lits := strings.Split(sql[m[2]:m[3]], ",")
	if len(lits) == 1 {
		return []string{sql}
	}
	out := make([]string, 0, len(lits))
	for _, lit := range lits {
		out = append(out, sql[:m[2]]+strings.TrimSpace(lit)+sql[m[3]:])
	}
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-CSRF-Token", "test-token")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	path := r.URL.Path
	body, _ := io.ReadAll(r.Body)

	switch {
	case strings.HasSuffix(path, "/datapreview/freestyle"):
		s.serveSQL(w, string(body))
		return
	case strings.HasSuffix(path, "/repository/nodestructure"):
		s.record("GET nodestructure " + r.URL.Query().Get("parent_name"))
		s.servePackage(w, r.URL.Query().Get("parent_name"))
		return
	case strings.HasSuffix(path, "/versions"):
		s.record("GET " + path)
		s.serveRevisions(w, path)
		return
	case strings.HasSuffix(path, "/source/main"):
		s.record("GET " + path)
		s.serveSource(w, path)
		return
	case strings.HasSuffix(path, "/abapunit/testruns"):
		uri := ""
		if m := regexp.MustCompile(`adtcore:uri="([^"]*)"`).FindStringSubmatch(string(body)); m != nil {
			uri = m[1]
		}
		s.record("POST testruns " + uri)
		s.serveUnitTests(w, uri)
		return
	case strings.HasSuffix(path, "/atc/customizing"):
		s.record("GET atc/customizing")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0"?><atccust:customizing xmlns:atccust="x"><atccust:properties>`+
			`<atccust:property name="systemCheckVariant" value="DEFAULT"/></atccust:properties></atccust:customizing>`)
		return
	case strings.HasSuffix(path, "/atc/worklists") && r.Method == http.MethodPost:
		s.record("POST atc/worklists")
		_, _ = io.WriteString(w, "WL1")
		return
	case strings.HasSuffix(path, "/atc/runs"):
		uri := ""
		if m := regexp.MustCompile(`adtcore:uri="([^"]*)"`).FindStringSubmatch(string(body)); m != nil {
			uri = m[1]
		}
		s.record("POST atc/runs " + uri)
		if s.world.ATCDeny {
			refuse(w, "No authorisation for ATC", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0"?><atcworklist:worklistRun xmlns:atcworklist="x">`+
			`<atcworklist:worklistId>WL1</atcworklist:worklistId></atcworklist:worklistRun>`)
		return
	case strings.Contains(path, "/atc/worklists/"):
		s.record("GET atc/worklists/WL1")
		s.serveWorklist(w)
		return
	}
	s.record("UNEXPECTED " + r.Method + " " + path)
	refuse(w, "not in the fake", http.StatusNotFound)
}

// --- free SQL -------------------------------------------------------------

type table struct {
	cols []string
	rows [][]string
}

func (s *Server) serveSQL(w http.ResponseWriter, sql string) {
	for _, line := range canonicalSQL(sql) {
		s.record("SQL " + line)
	}
	upper := strings.ToUpper(strings.Join(strings.Fields(sql), " "))
	for _, f := range s.world.Fail {
		if strings.Contains(upper, strings.ToUpper(f)) {
			refuse(w, "Data preview refused this query", http.StatusInternalServerError)
			return
		}
	}
	t, ok := s.answer(upper)
	if !ok {
		s.record("UNEXPECTED SQL " + upper)
		refuse(w, "query not in the fake", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, t.xml())
}

func literals(upper string) []string {
	m := inList.FindStringSubmatch(upper)
	if m == nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(m[1], ",") {
		out = append(out, strings.Trim(strings.TrimSpace(l), "'"))
	}
	return out
}

func quoted(upper, after string) string {
	i := strings.Index(upper, after)
	if i < 0 {
		return ""
	}
	rest := upper[i+len(after):]
	rest = strings.TrimLeft(rest, " '")
	if j := strings.IndexAny(rest, "'%"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func (s *Server) answer(upper string) (table, bool) {
	world := s.world
	switch {
	case strings.Contains(upper, "FROM E070"):
		// Ahead of the TADIR cases: the statement nests a TADIR sub-select.
		return table{cols: []string{"LAST_DATE"}, rows: [][]string{{"20190301"}}}, true

	case strings.Contains(upper, "FROM TDEVC"):
		prefix := quoted(upper, "DEVCLASS LIKE")
		t := table{cols: []string{"DEVCLASS", "PARENTCL"}}
		for _, p := range sortedKeys(world.Packages) {
			if strings.HasPrefix(p, prefix) {
				t.rows = append(t.rows, []string{p, world.Packages[p]})
			}
		}
		return t, true

	case strings.Contains(upper, "FROM TADIR WHERE DEVCLASS"):
		want := map[string]bool{}
		if lits := literals(upper); lits != nil {
			for _, l := range lits {
				want[l] = true
			}
		} else {
			want[quoted(upper, "DEVCLASS =")] = true
		}
		t := table{cols: []string{"OBJECT", "OBJ_NAME", "DEVCLASS"}}
		for _, o := range world.Objects {
			if want[o.Package] {
				t.rows = append(t.rows, []string{o.Type, o.Name, o.Package})
			}
		}
		return t, true

	case strings.Contains(upper, "FROM TADIR WHERE PGMID = 'R3TR' AND OBJECT = 'FUGR'"):
		t := table{cols: []string{"OBJ_NAME", "DEVCLASS"}}
		for _, l := range literals(upper) {
			if pkg, ok := world.Groups[l]; ok {
				t.rows = append(t.rows, []string{l, pkg})
			}
		}
		return t, true

	case strings.Contains(upper, "FROM TADIR WHERE PGMID = 'R3TR' AND OBJ_NAME IN"):
		t := table{cols: []string{"OBJECT", "OBJ_NAME", "DEVCLASS"}}
		for _, l := range literals(upper) {
			for _, o := range world.Objects {
				if o.Name == l {
					t.rows = append(t.rows, []string{o.Type, o.Name, o.Package})
				}
			}
		}
		return t, true

	case strings.Contains(upper, "FROM TFDIR"):
		t := table{cols: []string{"FUNCNAME", "PNAME"}}
		for _, l := range literals(upper) {
			if p, ok := world.FuncModules[l]; ok {
				t.rows = append(t.rows, []string{l, p})
			}
		}
		return t, true

	case strings.Contains(upper, "FROM WBCROSSGT WHERE OTYPE = 'TY' AND NAME = 'TVARVC'"):
		return includes(world.TVARVCReaders[0]), true
	case strings.Contains(upper, "FROM CROSS WHERE TYPE = 'S' AND NAME = 'TVARVC'"):
		return includes(world.TVARVCReaders[1]), true

	case strings.Contains(upper, "FROM D010INC"):
		t := table{cols: []string{"MASTER", "INCLUDE", "OBSOLETE_IN_VERSION"}}
		if strings.Contains(upper, "WHERE INCLUDE LIKE") {
			prefix := quoted(upper, "INCLUDE LIKE")
			for _, r := range world.D010INC {
				if strings.HasPrefix(r[1], prefix) {
					t.rows = append(t.rows, []string{r[0], r[1], ""})
				}
			}
			return t, true
		}
		prefix := quoted(upper, "MASTER LIKE")
		for _, r := range world.D010INC {
			if strings.HasPrefix(r[0], prefix) || r[0] == "SAPL"+prefix {
				t.rows = append(t.rows, []string{r[0], r[1], ""})
			}
		}
		return t, true

	}
	return table{}, false
}

func includes(list []string) table {
	t := table{cols: []string{"INCLUDE"}}
	for _, inc := range list {
		t.rows = append(t.rows, []string{inc})
	}
	return t
}

func (t table) xml() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	b.WriteString(`<dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">`)
	for i, c := range t.cols {
		fmt.Fprintf(&b, `<dataPreview:columns><dataPreview:metadata dataPreview:name="%s" dataPreview:type="C" dataPreview:length="40"/><dataPreview:dataSet>`, c)
		for _, r := range t.rows {
			fmt.Fprintf(&b, `<dataPreview:data>%s</dataPreview:data>`, html.EscapeString(r[i]))
		}
		b.WriteString(`</dataPreview:dataSet></dataPreview:columns>`)
	}
	b.WriteString(`</dataPreview:tableData>`)
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- ADT resources --------------------------------------------------------

// workbenchCode is the two-part type the package listing carries.
func workbenchCode(objType string) string {
	switch objType {
	case "CLAS":
		return "CLAS/OC"
	case "INTF":
		return "INTF/OI"
	case "PROG":
		return "PROG/P"
	case "TABL":
		return "TABL/DT"
	case "FUGR":
		return "FUGR/F"
	}
	return objType
}

func (s *Server) servePackage(w http.ResponseWriter, pkg string) {
	pkg = strings.ToUpper(pkg)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA><TREE_CONTENT>`)
	for _, child := range sortedKeys(s.world.Packages) {
		if s.world.Packages[child] == pkg {
			fmt.Fprintf(&b, `<SEU_ADT_REPOSITORY_OBJ_NODE><OBJECT_TYPE>DEVC/K</OBJECT_TYPE><OBJECT_NAME>%s</OBJECT_NAME></SEU_ADT_REPOSITORY_OBJ_NODE>`, html.EscapeString(child))
		}
	}
	for _, o := range s.world.Objects {
		if o.Package == pkg {
			fmt.Fprintf(&b, `<SEU_ADT_REPOSITORY_OBJ_NODE><OBJECT_TYPE>%s</OBJECT_TYPE><OBJECT_NAME>%s</OBJECT_NAME></SEU_ADT_REPOSITORY_OBJ_NODE>`,
				workbenchCode(o.Type), html.EscapeString(o.Name))
		}
	}
	b.WriteString(`</TREE_CONTENT></DATA></asx:values></asx:abap>`)
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, b.String())
}

// objectOf finds the object a resource path addresses.
func (s *Server) objectOf(path string) (Object, bool) {
	segs := strings.Split(strings.ToUpper(path), "/")
	for _, o := range s.world.Objects {
		for i, seg := range segs {
			if seg != strings.ToUpper(o.Name) || i == 0 {
				continue
			}
			kind := segs[i-1]
			switch {
			case o.Type == "CLAS" && kind == "CLASSES",
				o.Type == "INTF" && kind == "INTERFACES",
				o.Type == "PROG" && kind == "PROGRAMS":
				return o, true
			}
		}
	}
	return Object{}, false
}

func (s *Server) serveSource(w http.ResponseWriter, path string) {
	o, ok := s.objectOf(path)
	if !ok || o.Source == "" {
		refuse(w, "Resource does not exist", http.StatusNotFound)
		return
	}
	if o.SourceStatus != 0 {
		refuse(w, "No authorisation to display "+o.Name, o.SourceStatus)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, o.Source)
}

func (s *Server) serveRevisions(w http.ResponseWriter, path string) {
	o, ok := s.objectOf(path)
	if !ok {
		refuse(w, "Resource does not exist", http.StatusNotFound)
		return
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><atom:feed xmlns:atom="http://www.w3.org/2005/Atom" xmlns:adtcore="http://www.sap.com/adt/core">`)
	if o.Revised != "" {
		fmt.Fprintf(&b, `<atom:entry><atom:id>1</atom:id><atom:title>Active</atom:title><atom:updated>%s</atom:updated>`+
			`<atom:author><atom:name>DEVELOPER</atom:name></atom:author>`+
			`<atom:content src="%s?version=1" type="text/plain"/></atom:entry>`, o.Revised, path)
	}
	b.WriteString(`</atom:feed>`)
	w.Header().Set("Content-Type", "application/atom+xml")
	_, _ = io.WriteString(w, b.String())
}

func (s *Server) serveUnitTests(w http.ResponseWriter, uri string) {
	switch s.world.UnitTests[uri] {
	case "deny":
		refuse(w, "No authorisation for ABAP Unit", http.StatusForbidden)
		return
	case "fail":
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">`+
			`<program adtcore:uri="/x" adtcore:type="CLAS/OC" adtcore:name="ZCL_GOLD_TEST"><testClasses>`+
			`<testClass adtcore:uri="/x" adtcore:type="CLAS/OL" adtcore:name="LTCL_GOLD"><testMethods>`+
			`<testMethod adtcore:uri="/x" adtcore:type="CLAS/OM" adtcore:name="WORKS" executionTime="0.01" unit="s"/>`+
			`<testMethod adtcore:uri="/x" adtcore:type="CLAS/OM" adtcore:name="BREAKS" executionTime="0.01" unit="s">`+
			`<alerts><alert kind="failedAssertion" severity="critical"><title>Expected 1, got 2</title></alert></alerts></testMethod>`+
			`</testMethods></testClass></testClasses></program></aunit:runResult>`)
		return
	case "pass":
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">`+
			`<program adtcore:uri="/x" adtcore:type="CLAS/OC" adtcore:name="ZCL_GOLD_TEST"><testClasses>`+
			`<testClass adtcore:uri="/x" adtcore:type="CLAS/OL" adtcore:name="LTCL_GOLD"><testMethods>`+
			`<testMethod adtcore:uri="/x" adtcore:type="CLAS/OM" adtcore:name="WORKS" executionTime="0.01" unit="s"/>`+
			`</testMethods></testClass></testClasses></program></aunit:runResult>`)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit"></aunit:runResult>`)
}

func (s *Server) serveWorklist(w http.ResponseWriter) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><atcworklist:worklist xmlns:atcworklist="x" xmlns:atcobject="y" xmlns:atcfinding="z" xmlns:adtcore="w" atcworklist:id="WL1"><atcworklist:objects>`)
	if len(s.world.ATCFindings) > 0 {
		b.WriteString(`<atcobject:object adtcore:uri="/sap/bc/adt/oo/classes/zcl_gold_a" adtcore:type="CLAS" adtcore:name="ZCL_GOLD_A" adtcore:packageName="$ZGOLD"><atcobject:findings>`)
		for i, p := range s.world.ATCFindings {
			fmt.Fprintf(&b, `<atcfinding:finding adtcore:uri="/f/%d" atcfinding:location="/sap/bc/adt/oo/classes/zcl_gold_a/source/main#start=%d,1" atcfinding:priority="%d" atcfinding:checkId="C%d" atcfinding:checkTitle="Check %d" atcfinding:messageTitle="Finding %d"/>`, i, i+1, p, i, i, i)
		}
		b.WriteString(`</atcobject:findings></atcobject:object>`)
	}
	b.WriteString(`</atcworklist:objects></atcworklist:worklist>`)
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, b.String())
}

// refuse answers with an error status. Unlike http.Error it adds no trailing
// newline, which would otherwise end up inside every reason a caller quotes.
func refuse(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, msg)
}
