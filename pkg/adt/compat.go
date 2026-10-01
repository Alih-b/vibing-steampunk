package adt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Two SAP releases answer the same ADT request differently, and the differences
// are not documented anywhere a caller can read. They are also not the kind of
// thing a feature flag captures: the resource exists or it does not, and it
// accepts a content type or refuses it, and both vary by release in ways that
// have already cost this project a wrong fix.
//
// Measured so far:
//   - a function group's objectstructure answers 200 on 7.57 and 404 on 7.50,
//     which does not advertise the relation at all
//   - the repository node structure answers 406 to every vendor content type on
//     7.57 and 200 to all of them on 7.50; only */* works on both
//   - a function group's metadata answers 406 to application/xml on 7.57 and
//     200 on 7.50
//
// This probe asks those questions on purpose, so that a system's answers are a
// table one can read and diff rather than a surprise inside a feature.

// CompatOutcome is what a system did with one request.
type CompatOutcome string

const (
	// CompatOK means the resource answered.
	CompatOK CompatOutcome = "ok"
	// CompatAbsent means the resource is not there — a 404 on a path the
	// caller spelled correctly is a statement about the release.
	CompatAbsent CompatOutcome = "absent"
	// CompatNotAcceptable means the resource exists but refused every content
	// type offered. This is the failure that reads as "broken" and is not.
	CompatNotAcceptable CompatOutcome = "not-acceptable"
	// CompatForbidden separates "this system does not have it" from "this user
	// may not see it", which otherwise look identical from the outside.
	CompatForbidden CompatOutcome = "forbidden"
	// CompatUnauthorized means the session did not carry.
	CompatUnauthorized CompatOutcome = "unauthorized"
	// CompatError is anything else, including a transport failure.
	CompatError CompatOutcome = "error"
	// CompatSkipped means the check never ran, for want of an object to run it
	// against. Reporting that as absent would put a missing feature on the
	// system's record when the fault is the probe's.
	CompatSkipped CompatOutcome = "skipped"
)

// CompatDepth selects how much to ask.
//
// The quick pass answers "can this harness work here at all, and by which
// route" in a couple of seconds, which is what a caller wants at the start of a
// session. The full pass walks the rest of the surface and is for the moment
// something behaves differently on one system than another.
type CompatDepth string

const (
	// CompatQuick asks only what decides routing.
	CompatQuick CompatDepth = "quick"
	// CompatFull asks everything.
	CompatFull CompatDepth = "full"
)

// CompatCheck is one question to ask a system.
type CompatCheck struct {
	// ID is stable across runs so two systems can be diffed on it.
	ID string
	// Quick marks a check that runs in the quick pass — those whose answer
	// changes which route a caller should take.
	Quick bool
	// What the check is for, in a few words.
	Purpose string
	Method  string
	// Path may contain {group}, {class}, {program} and {package}, filled from
	// CompatTargets.
	Path  string
	Query url.Values
	// Accepts are tried in order until one answers. Recording which one worked
	// is the point: a resource that only answers to */* is a portability
	// constraint on every caller that touches it.
	Accepts []string
	Body    []byte
	// NeedsCSRF marks a request the server will refuse without a token.
	NeedsCSRF bool
}

// CompatResult is one check's answer from one system.
type CompatResult struct {
	ID       string        `json:"id"`
	Purpose  string        `json:"purpose"`
	Outcome  CompatOutcome `json:"outcome"`
	Status   int           `json:"status"`
	Accepted string        `json:"accepted,omitempty"`
	// Refused lists the content types this system turned down, which is the
	// part a future caller needs and cannot guess.
	Refused []string      `json:"refused,omitempty"`
	Detail  string        `json:"detail,omitempty"`
	Elapsed time.Duration `json:"-"`
}

// CompatTargets names objects the probe may read. Checks needing a target it
// does not have are skipped rather than reported as failures.
type CompatTargets struct {
	FunctionGroup string
	Class         string
	Program       string
	Package       string
}

// CompatReport is everything one system answered.
type CompatReport struct {
	System   string         `json:"system"`
	Depth    string         `json:"depth"`
	Release  string         `json:"release"`
	Database string         `json:"database"`
	Results  []CompatResult `json:"results"`
	// DebuggerSurface is the set of debugger resources this release advertises
	// in its own discovery document, sorted. It is here because most of the
	// debugger cannot be probed with a request — /debugger/stack answers 404 on
	// every release until a program is actually stopped — so the only thing
	// comparable across systems without stopping one is what each says it has.
	DebuggerSurface []string `json:"debuggerSurface,omitempty"`
}

// vendor content types, tried in the order a caller would.
const (
	acceptAny = "*/*"
	acceptXML = "application/xml"
)

// CompatChecks returns the questions worth asking. Each one exists because a
// release answered it unexpectedly, or because a caller depends on it.
func CompatChecks() []CompatCheck {
	nodeQuery := url.Values{}
	nodeQuery.Set("parent_type", "FUGR/F")
	nodeQuery.Set("parent_name", "{group}")
	nodeQuery.Set("withShortDescriptions", "true")

	return []CompatCheck{
		{
			ID: "core.discovery", Quick: true, Purpose: "ADT reachable at all",
			Method: http.MethodGet, Path: "/sap/bc/adt/core/discovery",
			Accepts: []string{"application/atomsvc+xml", acceptXML, acceptAny},
		},
		{
			ID: "discovery", Purpose: "which ADT collections this system offers",
			Method: http.MethodGet, Path: "/sap/bc/adt/discovery",
			Accepts: []string{"application/atomsvc+xml", acceptXML, acceptAny},
		},
		{
			ID: "fugr.metadata", Quick: true, Purpose: "function group metadata; the 406 axis",
			Method: http.MethodGet, Path: "/sap/bc/adt/functions/groups/{group}",
			Accepts: []string{
				"application/vnd.sap.adt.functions.groups.v3+xml",
				"application/vnd.sap.adt.functions.groups.v2+xml",
				acceptXML, acceptAny,
			},
		},
		{
			ID: "fugr.objectstructure", Purpose: "module list via objectstructure; absent before S/4",
			Method: http.MethodGet, Path: "/sap/bc/adt/functions/groups/{group}/objectstructure",
			Accepts: []string{"application/vnd.sap.adt.objectstructure.v2+xml", acceptXML, acceptAny},
		},
		{
			ID: "repository.nodestructure", Quick: true, Purpose: "module list via the object tree; only */* works everywhere",
			Method: http.MethodPost, Path: "/sap/bc/adt/repository/nodestructure", Query: nodeQuery,
			Accepts: []string{
				"application/vnd.sap.adt.repository.nodestructure.v1+xml",
				acceptXML, acceptAny,
			},
			NeedsCSRF: true,
		},
		{
			ID: "class.source", Purpose: "read a class",
			Method: http.MethodGet, Path: "/sap/bc/adt/oo/classes/{class}/source/main",
			Accepts: []string{"text/plain", acceptAny},
		},
		{
			ID: "program.source", Purpose: "read a program",
			Method: http.MethodGet, Path: "/sap/bc/adt/programs/programs/{program}/source/main",
			Accepts: []string{"text/plain", acceptAny},
		},
		{
			ID: "package", Purpose: "read a package",
			Method: http.MethodGet, Path: "/sap/bc/adt/packages/{package}",
			Accepts: []string{"application/vnd.sap.adt.packages.v1+xml", acceptXML, acceptAny},
		},
		{
			ID: "checkruns", Purpose: "syntax check",
			Method: http.MethodGet, Path: "/sap/bc/adt/checkruns/reporters",
			Accepts: []string{"application/vnd.sap.adt.reporters+xml", acceptXML, acceptAny},
		},
		{
			ID: "datapreview", Quick: true, Purpose: "free SQL; how vsp reads tables",
			Method: http.MethodGet, Path: "/sap/bc/adt/datapreview/freestyle",
			Accepts: []string{acceptAny},
		},
		{
			ID: "debugger.listeners", Purpose: "the debugger's listener resource",
			Method: http.MethodGet, Path: "/sap/bc/adt/debugger/listeners",
			Accepts: []string{acceptAny},
		},
		{
			ID: "debugger.breakpoints", Quick: true, Purpose: "breakpoints over ADT, no Z code needed",
			Method: http.MethodGet, Path: "/sap/bc/adt/debugger/breakpoints",
			Accepts: []string{acceptAny},
		},
		// The breakpoint kinds a release supports, each its own resource and
		// each answerable with no debug session held. These are the only part
		// of the debugger surface that can be compared across systems without
		// stopping a program, and what they list differs by release — which is
		// exactly what a caller setting a non-line breakpoint needs to know
		// before it tries.
		{
			ID: "debugger.bp.statements", Purpose: "statement breakpoints the release knows",
			Method: http.MethodGet, Path: "/sap/bc/adt/debugger/breakpoints/statements",
			Accepts: []string{acceptAny},
		},
		{
			ID: "debugger.bp.conditions", Purpose: "conditional breakpoint support",
			Method: http.MethodGet, Path: "/sap/bc/adt/debugger/breakpoints/conditions",
			Accepts: []string{acceptAny},
		},
		{
			ID: "debugger.bp.messagetypes", Purpose: "message breakpoints",
			Method: http.MethodGet, Path: "/sap/bc/adt/debugger/breakpoints/messagetypes",
			Accepts: []string{acceptAny},
		},
		{
			ID: "debugger.bp.validations", Purpose: "breakpoint validation rules",
			Method: http.MethodGet, Path: "/sap/bc/adt/debugger/breakpoints/validations",
			Accepts: []string{acceptAny},
		},
		{
			ID: "debugger.bp.vit", Purpose: "VIT breakpoints; absent before 7.5x",
			Method: http.MethodGet, Path: "/sap/bc/adt/debugger/breakpoints/vit",
			Accepts: []string{acceptAny},
		},
		{
			ID: "atc.runs", Purpose: "ATC checks",
			Method: http.MethodGet, Path: "/sap/bc/adt/atc/runs",
			Accepts: []string{acceptAny},
		},
		{
			ID: "cts.transports", Quick: true, Purpose: "transport requests",
			Method: http.MethodGet, Path: "/sap/bc/adt/cts/transportrequests",
			Accepts: []string{"application/vnd.sap.adt.transportorganizertree.v1+xml", acceptXML, acceptAny},
		},
		{
			ID: "textelements", Purpose: "i18n texts",
			Method: http.MethodGet, Path: "/sap/bc/adt/textelements/programs/{program}",
			Accepts: []string{acceptAny},
		},
		{
			ID: "soap.rfc", Quick: true, Purpose: "RFC over HTTP, where the gateway is closed",
			Method: http.MethodGet, Path: "/sap/bc/soap/rfc",
			Accepts: []string{acceptAny},
		},
		{
			ID: "apc.zadt_vsp", Quick: true, Purpose: "the WebSocket tunnel, if ZADT_VSP is installed",
			Method: http.MethodGet, Path: "/sap/bc/apc/sap/zadt_vsp",
			Accepts: []string{acceptAny},
		},
	}
}

// RunCompatProbe asks every check and returns what the system said.
func (c *Client) RunCompatProbe(ctx context.Context, targets CompatTargets, depth CompatDepth) *CompatReport {
	report := &CompatReport{Depth: string(depth)}
	if info, err := c.GetSystemInfo(ctx); err == nil {
		report.System, report.Release, report.Database = info.SystemID, info.SAPRelease, info.DatabaseSystem
	}

	for _, check := range CompatChecks() {
		if depth == CompatQuick && !check.Quick {
			continue
		}
		path, pathOK := fillTargets(check.Path, targets)
		query, queryOK := fillQueryTargets(check.Query, targets)
		if !pathOK || !queryOK {
			report.Results = append(report.Results, CompatResult{
				ID: check.ID, Purpose: check.Purpose, Outcome: CompatSkipped,
				Detail: "no object of the required kind was found to probe with",
			})
			continue
		}
		if check.ID == apcCheckID {
			report.Results = append(report.Results, c.probeAPCTunnel(ctx, check))
			continue
		}
		report.Results = append(report.Results, c.runCompatCheck(ctx, check, path, query))
	}
	report.DebuggerSurface = c.debuggerSurface(ctx)
	return report
}

// debuggerSurface lists the debugger resources the discovery document
// advertises. An unreachable or unreadable discovery yields nothing rather than
// an error: the surface is extra information, not a check that can fail.
func (c *Client) debuggerSurface(ctx context.Context) []string {
	res, err := c.transport.Request(ctx, "/sap/bc/adt/discovery", &RequestOptions{
		Method: http.MethodGet,
		Accept: acceptAny,
	})
	if err != nil || res == nil {
		return nil
	}
	seen := map[string]bool{}
	body := string(res.Body)
	const marker = `href="/sap/bc/adt/debugger`
	for {
		i := strings.Index(body, marker)
		if i < 0 {
			break
		}
		rest := body[i+len(`href="`):]
		j := strings.Index(rest, `"`)
		if j < 0 {
			break
		}
		seen[rest[:j]] = true
		body = rest[j:]
	}
	out := make([]string, 0, len(seen))
	for href := range seen {
		out = append(out, href)
	}
	sort.Strings(out)
	return out
}

// apcCheckID names the check that cannot be asked with an ordinary request.
const apcCheckID = "apc.zadt_vsp"

// probeAPCTunnel opens the WebSocket rather than sending a GET.
//
// A GET to an APC node answers 501: the node is there, it simply does not speak
// that. Reading 501 as "present" would report a tunnel that works even when the
// ABAP behind it does not compile — which is exactly the state one of these
// systems was in this morning, answering 500 with a syntax error in a class the
// caller had never heard of. Only the handshake distinguishes the two.
func (c *Client) probeAPCTunnel(ctx context.Context, check CompatCheck) CompatResult {
	result := CompatResult{ID: check.ID, Purpose: check.Purpose}
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()

	ws := c.NewDebugWebSocketClient()

	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if err := ws.Connect(dialCtx); err != nil {
		detail := firstLine(err.Error())
		switch {
		case strings.Contains(detail, "404"):
			result.Outcome, result.Detail = CompatAbsent, "ZADT_VSP is not installed"
		case strings.Contains(detail, "500"):
			result.Outcome = CompatError
			result.Detail = "installed but not runnable — the APC application raised: " + detail
		default:
			result.Outcome, result.Detail = CompatError, detail
		}
		return result
	}
	defer ws.Close()

	result.Outcome, result.Accepted = CompatOK, "websocket upgrade"
	return result
}

// runCompatCheck tries each content type until one answers, and records which.
func (c *Client) runCompatCheck(ctx context.Context, check CompatCheck, path string, query url.Values) CompatResult {
	result := CompatResult{ID: check.ID, Purpose: check.Purpose, Outcome: CompatError}
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()

	for _, accept := range check.Accepts {
		resp, err := c.transport.Request(ctx, path, &RequestOptions{
			Method: check.Method,
			Query:  query,
			Accept: accept,
			Body:   check.Body,
		})
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		var apiErr *APIError
		if err != nil && errors.As(err, &apiErr) {
			status = apiErr.StatusCode
		}
		result.Status = status

		switch {
		case err == nil:
			result.Outcome, result.Accepted = CompatOK, accept
			return result
		case status == http.StatusNotAcceptable:
			// Keep trying: the next content type may be the one it wants.
			result.Refused = append(result.Refused, accept)
			result.Outcome = CompatNotAcceptable
		case status == http.StatusNotFound:
			result.Outcome = CompatAbsent
			return result
		case status == http.StatusForbidden:
			result.Outcome = CompatForbidden
			result.Detail = firstLine(err.Error())
			return result
		case status == http.StatusUnauthorized:
			result.Outcome = CompatUnauthorized
			return result
		case status >= 400 && status < 500:
			// A 405 or a 400 still proves the resource is there, which is what
			// this probe is asking. The method or the payload is our problem.
			result.Outcome, result.Accepted = CompatOK, accept
			result.Detail = fmt.Sprintf("exists; %d to %s", status, check.Method)
			return result
		default:
			result.Outcome = CompatError
			result.Detail = firstLine(err.Error())
			return result
		}
	}
	return result
}

// fillTargets substitutes the objects a check needs, reporting whether it can
// run at all.
func fillTargets(path string, t CompatTargets) (string, bool) {
	replacements := map[string]string{
		"{group}":   t.FunctionGroup,
		"{class}":   t.Class,
		"{program}": t.Program,
		"{package}": t.Package,
	}
	for token, value := range replacements {
		if !strings.Contains(path, token) {
			continue
		}
		if value == "" {
			return "", false
		}
		path = strings.ReplaceAll(path, token, url.PathEscape(value))
	}
	return path, true
}

func fillQueryTargets(q url.Values, t CompatTargets) (url.Values, bool) {
	if len(q) == 0 {
		return nil, true
	}
	out := url.Values{}
	for key, values := range q {
		for _, v := range values {
			filled, ok := fillTargets(v, t)
			if !ok {
				return nil, false
			}
			// fillTargets escapes for a path segment; a query value is escaped
			// by the encoder, so undo it here rather than double-encoding.
			if unescaped, err := url.PathUnescape(filled); err == nil {
				filled = unescaped
			}
			out.Add(key, filled)
		}
	}
	return out, true
}

func firstLine(s string) string {
	// SAP's refusals arrive as a whole HTML error page on one line. Quoting the
	// first 120 characters of that yields a doctype and a stylesheet, which
	// tells the reader nothing; the status code above it already carries the
	// meaning.
	if i := strings.Index(s, "<html"); i >= 0 {
		s = strings.TrimSpace(s[:i])
		s = strings.TrimSuffix(strings.TrimSpace(s), ":")
	}
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return strings.TrimSpace(s)
}

// Text renders the report as a table.
func (r *CompatReport) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "System %s, release %s", orUnknown(r.System), orUnknown(r.Release))
	if r.Database != "" {
		fmt.Fprintf(&b, ", %s", r.Database)
	}
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "%-26s %-15s %-6s %s\n", "CHECK", "OUTCOME", "HTTP", "ACCEPTED / NOTE")
	b.WriteString(strings.Repeat("-", 100) + "\n")
	for _, res := range r.Results {
		note := res.Accepted
		if res.Detail != "" {
			note = strings.TrimSpace(note + " " + res.Detail)
		}
		if len(res.Refused) > 0 {
			note = fmt.Sprintf("%s (refused %d)", note, len(res.Refused))
		}
		fmt.Fprintf(&b, "%-26s %-15s %-6s %s\n", res.ID, res.Outcome, statusText(res.Status), note)
	}
	if len(r.DebuggerSurface) > 0 {
		fmt.Fprintf(&b, "\nDebugger resources advertised (%d):\n", len(r.DebuggerSurface))
		for _, href := range r.DebuggerSurface {
			fmt.Fprintf(&b, "  %s\n", href)
		}
	}
	return b.String()
}

func statusText(status int) string {
	if status == 0 {
		return "-"
	}
	return fmt.Sprint(status)
}

func orUnknown(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// DiffCompatReports lists the checks on which two systems disagree, which is
// the only part worth reading when comparing releases.
func DiffCompatReports(a, b *CompatReport) string {
	index := func(r *CompatReport) map[string]CompatResult {
		m := make(map[string]CompatResult, len(r.Results))
		for _, res := range r.Results {
			m[res.ID] = res
		}
		return m
	}
	left, right := index(a), index(b)

	ids := make([]string, 0, len(left))
	for id := range left {
		ids = append(ids, id)
	}
	for id := range right {
		if _, seen := left[id]; !seen {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	var out strings.Builder
	fmt.Fprintf(&out, "%-26s %-24s %s\n", "CHECK", orUnknown(a.System), orUnknown(b.System))
	out.WriteString(strings.Repeat("-", 84) + "\n")
	differences := 0
	for _, id := range ids {
		l, r := left[id], right[id]
		if l.Outcome == r.Outcome && l.Accepted == r.Accepted {
			continue
		}
		differences++
		fmt.Fprintf(&out, "%-26s %-24s %s\n", id, describe(l), describe(r))
	}
	if differences == 0 {
		out.WriteString("(the two systems agree on every check)\n")
	}
	if surface := diffDebuggerSurface(a, b); surface != "" {
		out.WriteString("\n" + surface)
	}
	return out.String()
}

// diffDebuggerSurface reports which debugger resources one release has and the
// other does not. A resource missing here is a feature that cannot be reached
// on that system no matter how the request is shaped, so it belongs in the
// comparison even though no check can ask for it directly.
func diffDebuggerSurface(a, b *CompatReport) string {
	if len(a.DebuggerSurface) == 0 && len(b.DebuggerSurface) == 0 {
		return ""
	}
	in := func(list []string, want string) bool {
		for _, got := range list {
			if got == want {
				return true
			}
		}
		return false
	}
	all := append(append([]string{}, a.DebuggerSurface...), b.DebuggerSurface...)
	sort.Strings(all)

	var out strings.Builder
	fmt.Fprintf(&out, "%-46s %-12s %s\n", "DEBUGGER RESOURCE", orUnknown(a.System), orUnknown(b.System))
	out.WriteString(strings.Repeat("-", 84) + "\n")
	differences, last := 0, ""
	for _, href := range all {
		if href == last {
			continue
		}
		last = href
		left, right := in(a.DebuggerSurface, href), in(b.DebuggerSurface, href)
		if left == right {
			continue
		}
		differences++
		fmt.Fprintf(&out, "%-46s %-12s %s\n", href, yesNo(left), yesNo(right))
	}
	if differences == 0 {
		return ""
	}
	return out.String()
}

func yesNo(present bool) string {
	if present {
		return "yes"
	}
	return "—"
}

func describe(r CompatResult) string {
	if r.ID == "" {
		return "not run"
	}
	if r.Outcome == CompatOK && r.Accepted != "" {
		return fmt.Sprintf("%s via %s", r.Outcome, shortAccept(r.Accepted))
	}
	return string(r.Outcome)
}

// shortAccept trims the vendor prefix so a table stays readable.
func shortAccept(accept string) string {
	accept = strings.TrimPrefix(accept, "application/vnd.sap.adt.")
	return strings.TrimPrefix(accept, "application/")
}

// --- Routing -----------------------------------------------------------------
//
// A probe that only reports status codes leaves the reader to work out what to
// do with them. These turn the answers into the decision a caller actually
// faces: for each capability, which route this system supports, and which one
// to prefer.

// CompatRoute is what a capability can travel over on a given system.
type CompatRoute struct {
	Capability string   `json:"capability"`
	Preferred  string   `json:"preferred"`
	Available  []string `json:"available,omitempty"`
	Note       string   `json:"note,omitempty"`
}

// Routes derives the transport advice from what the system answered.
func (r *CompatReport) Routes() []CompatRoute {
	ok := func(id string) bool {
		for _, res := range r.Results {
			if res.ID == id {
				return res.Outcome == CompatOK
			}
		}
		return false
	}
	accepted := func(id string) string {
		for _, res := range r.Results {
			if res.ID == id {
				return res.Accepted
			}
		}
		return ""
	}

	var routes []CompatRoute

	// Reading and editing ride plain ADT everywhere; the only question is
	// whether ADT answered at all.
	source := CompatRoute{Capability: "read/edit"}
	if ok("core.discovery") {
		source.Preferred, source.Available = "adt-http", []string{"adt-http"}
	} else {
		source.Preferred, source.Note = "none", "ADT did not answer; nothing else here will work"
	}
	routes = append(routes, source)

	// The debugger has two routes and they are not equivalent: the ADT one
	// needs nothing installed, the tunnel needs ZADT_VSP but survives a closed
	// HTTP port.
	debug := CompatRoute{Capability: "debug"}
	if ok("debugger.breakpoints") {
		debug.Available = append(debug.Available, "adt-http")
	}
	if ok("apc.zadt_vsp") {
		debug.Available = append(debug.Available, "zadt-vsp-ws")
	}
	switch {
	case len(debug.Available) == 0:
		debug.Preferred, debug.Note = "none", "no debugger resource and no APC tunnel"
	case ok("debugger.breakpoints"):
		debug.Preferred = "adt-http"
		debug.Note = "nothing to install; the tunnel is the fallback where HTTP is closed"
	default:
		debug.Preferred, debug.Note = "zadt-vsp-ws", "ADT debugger resource did not answer"
	}
	routes = append(routes, debug)

	// RFC is the one that varies most, and the reason this probe exists.
	rfc := CompatRoute{Capability: "rfc"}
	if ok("apc.zadt_vsp") {
		rfc.Available = append(rfc.Available, "zadt-vsp-ws")
	}
	if ok("soap.rfc") {
		rfc.Available = append(rfc.Available, "soap-rfc")
	}
	switch {
	case len(rfc.Available) == 0:
		rfc.Preferred = "gateway-only"
		rfc.Note = "no HTTP route; needs port 3300 and an RFC password"
	case ok("apc.zadt_vsp"):
		rfc.Preferred = "zadt-vsp-ws"
		rfc.Note = "carries the session; SOAP is stateless and cannot hold one"
	default:
		rfc.Preferred = "soap-rfc"
		rfc.Note = "stateless: one ABAP session per call, so no attach or step"
	}
	routes = append(routes, rfc)

	// Table reads have one route, but knowing it failed is worth saying,
	// because most of the analysis features are built on it.
	sql := CompatRoute{Capability: "table-read"}
	if ok("datapreview") {
		sql.Preferred, sql.Available = "adt-datapreview", []string{"adt-datapreview"}
	} else {
		sql.Preferred, sql.Note = "none", "free SQL refused; package analysis will be limited"
	}
	routes = append(routes, sql)

	// The module list is where the releases diverge, and the advice is the
	// opposite on each.
	skipped := func(id string) bool {
		for _, res := range r.Results {
			if res.ID == id {
				return res.Outcome == CompatSkipped
			}
		}
		return false
	}

	modules := CompatRoute{Capability: "function-module-list"}
	if ok("fugr.objectstructure") {
		modules.Available = append(modules.Available, "objectstructure")
	}
	if ok("repository.nodestructure") {
		modules.Available = append(modules.Available, "nodestructure")
	}
	switch {
	case ok("repository.nodestructure"):
		modules.Preferred = "nodestructure"
		if a := accepted("repository.nodestructure"); a != "" && a != acceptAny {
			modules.Note = "accepts " + shortAccept(a)
		} else {
			modules.Note = "only */* is accepted here"
		}
	case ok("fugr.objectstructure"):
		modules.Preferred, modules.Note = "objectstructure", "node structure did not answer"
	case skipped("repository.nodestructure") || skipped("fugr.objectstructure"):
		modules.Preferred, modules.Note = "unknown", "not probed: no function group was found to ask about"
	default:
		modules.Preferred, modules.Note = "none", "no way to list a group's modules"
	}
	routes = append(routes, modules)

	transports := CompatRoute{Capability: "transports"}
	if ok("cts.transports") {
		transports.Preferred, transports.Available = "adt-cts", []string{"adt-cts"}
	} else {
		transports.Preferred, transports.Note = "none", "CTS resource refused; local objects only"
	}
	routes = append(routes, transports)

	return routes
}

// RoutingText renders the advice.
func (r *CompatReport) RoutingText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s %-18s %s\n", "CAPABILITY", "PREFERRED", "NOTE")
	b.WriteString(strings.Repeat("-", 92) + "\n")
	for _, route := range r.Routes() {
		note := route.Note
		if len(route.Available) > 1 {
			note = fmt.Sprintf("also %s. %s", strings.Join(route.Available[1:], ", "), note)
		}
		fmt.Fprintf(&b, "%-22s %-18s %s\n", route.Capability, route.Preferred, strings.TrimSpace(note))
	}
	return b.String()
}

// DiffRoutes lists the capabilities two systems would route differently, which
// is what decides whether one code path can serve both.
func DiffRoutes(a, b *CompatReport) string {
	index := func(r *CompatReport) map[string]CompatRoute {
		m := map[string]CompatRoute{}
		for _, route := range r.Routes() {
			m[route.Capability] = route
		}
		return m
	}
	left, right := index(a), index(b)

	var out strings.Builder
	fmt.Fprintf(&out, "%-22s %-24s %-24s %s\n", "CAPABILITY", orUnknown(a.System), orUnknown(b.System), "")
	out.WriteString(strings.Repeat("-", 84) + "\n")
	same := 0
	for _, route := range a.Routes() {
		other := right[route.Capability]
		if route.Preferred == other.Preferred {
			same++
			continue
		}
		fmt.Fprintf(&out, "%-22s %-24s %-24s %s\n",
			route.Capability, route.Preferred, other.Preferred, "differs")
	}
	_ = left
	fmt.Fprintf(&out, "\n%d of %d capabilities route the same way.\n", same, len(a.Routes()))
	return out.String()
}
