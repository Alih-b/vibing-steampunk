package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/oisee/vibing-steampunk/pkg/adt"
	"github.com/oisee/vibing-steampunk/pkg/graph"
	"github.com/oisee/vibing-steampunk/pkg/graph/adtsource"
)

type healthScope struct {
	Kind       string `json:"kind"`
	Package    string `json:"package,omitempty"`
	ObjectType string `json:"object_type,omitempty"`
	ObjectName string `json:"object_name,omitempty"`
}

type healthSummary struct {
	Status   string `json:"status"`
	Headline string `json:"headline"`
}

type healthSignal struct {
	Status  string         `json:"status"`
	Details map[string]any `json:"details,omitempty"`
	// Unsearched names what this signal could not check. A health report is
	// read for reassurance, which makes it the worst place in the codebase to
	// drop a failure: every check that did not run comes back looking like a
	// check that found nothing, and the object is pronounced healthy on the
	// strength of questions nobody managed to ask.
	Unsearched []adt.Unsearched `json:"unsearched,omitempty"`
	// Note is the same gap in a sentence, for a reader who skims statuses.
	Note string `json:"note,omitempty"`
}

// incomplete reports whether this signal's status was reached without all the
// evidence. Used by the summary, which must not say GOOD over a partial sweep.
func (h healthSignal) incomplete() bool {
	return len(h.Unsearched) > 0 || h.Note != "" || h.Status == "ERROR"
}

type healthResult struct {
	Scope   healthScope             `json:"scope"`
	Summary healthSummary           `json:"summary"`
	Signals map[string]healthSignal `json:"signals"`
	// Notes repeats the gaps at the top level. An agent reading this JSON has
	// no stderr and may never open the individual signals; the caveat has to be
	// where the verdict is.
	Notes []string `json:"notes,omitempty"`
}

func (s *Server) handleHealth(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := request.GetArguments()
	pkg := strings.ToUpper(strings.TrimSpace(getStringParam(args, "package")))
	objType := strings.ToUpper(strings.TrimSpace(getStringParam(args, "object_type")))
	objName := strings.ToUpper(strings.TrimSpace(getStringParam(args, "object_name")))
	parent := strings.ToUpper(strings.TrimSpace(getStringParam(args, "parent")))

	if s.adtClient == nil {
		return newToolResultError("SAP connection required for health"), nil
	}
	if pkg == "" && (objType == "" || objName == "") {
		return newToolResultError("provide either package or object_type + object_name"), nil
	}

	result := &healthResult{
		Signals: make(map[string]healthSignal),
	}

	if pkg != "" {
		result.Scope = healthScope{Kind: "package", Package: pkg}
		s.populatePackageHealth(ctx, pkg, result)
	} else {
		result.Scope = healthScope{Kind: "object", ObjectType: objType, ObjectName: objName}
		s.populateObjectHealth(ctx, objType, objName, parent, result)
	}

	result.Summary = summarizeHealth(result.Signals)
	result.Notes = healthNotes(result.Signals)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return newToolResultError(fmt.Sprintf("JSON marshal error: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) populatePackageHealth(ctx context.Context, pkg string, result *healthResult) {
	result.Signals["tests"] = s.collectPackageTests(ctx, pkg)
	result.Signals["atc"] = s.collectPackageATC(ctx, pkg)
	result.Signals["boundaries"] = s.collectPackageBoundaries(ctx, pkg)
	result.Signals["staleness"] = s.collectPackageStaleness(ctx, pkg)
}

func (s *Server) populateObjectHealth(ctx context.Context, objType, objName, parent string, result *healthResult) {
	result.Signals["tests"] = s.collectObjectTests(ctx, objType, objName)
	result.Signals["atc"] = s.collectObjectATC(ctx, objType, objName)
	result.Signals["boundaries"] = s.collectObjectBoundaries(ctx, objType, objName, parent)
	result.Signals["staleness"] = s.collectObjectStaleness(ctx, objType, objName, parent)
}

func (s *Server) collectObjectTests(ctx context.Context, objType, objName string) healthSignal {
	objectURL := buildHealthObjectURL(objType, objName, "")
	if objectURL == "" {
		return healthSignal{Status: "UNKNOWN"}
	}
	result, err := s.adtClient.RunUnitTests(ctx, objectURL, nil)
	if err != nil {
		return healthSignal{Status: "ERROR", Details: map[string]any{"message": err.Error()}}
	}
	status, details := adtsource.UnitTestVerdict(result)
	return healthSignal{Status: status, Details: details}
}

func (s *Server) collectPackageTests(ctx context.Context, pkg string) healthSignal {
	content, err := s.adtClient.GetPackage(ctx, pkg)
	if err != nil {
		return healthSignal{Status: "ERROR", Details: map[string]any{"message": err.Error()}}
	}

	var testClasses []adt.PackageObject
	for _, obj := range content.Objects {
		if adtsource.SourceKind(obj.Type) == "CLAS" && graph.IsTestCaller(obj.Name, "") {
			testClasses = append(testClasses, obj)
		}
	}
	if len(testClasses) == 0 {
		return healthSignal{Status: "NONE"}
	}

	limit := 5
	if len(testClasses) < limit {
		limit = len(testClasses)
	}
	totalClasses := 0
	totalMethods := 0
	totalAlerts := 0
	ran := 0
	var missed []adt.Unsearched
	for _, obj := range testClasses[:limit] {
		objectURL := buildHealthObjectURL("CLAS", obj.Name, "")
		result, err := s.adtClient.RunUnitTests(ctx, objectURL, nil)
		if err != nil {
			// A test run that never started raises no alerts, and no alerts is
			// what a passing test looks like from here. Skipping it silently is
			// how this report tells someone their package is fine.
			missed = append(missed, adt.Unsearched{Object: obj.Name, Reason: err.Error()})
			continue
		}
		ran++
		c, m, a := adtsource.UnitTestCounts(result)
		totalClasses += c
		totalMethods += m
		totalAlerts += a
	}

	status := "PASS"
	if totalClasses == 0 {
		status = "NONE"
	}
	if totalAlerts > 0 {
		status = "FAIL"
	}
	// "NONE" claims the package has no tests. When nothing ran, what we know is
	// that we could not find out — a different fact, and the opposite one to
	// act on, so it does not get to borrow the reassuring word.
	if ran == 0 {
		status = "ERROR"
	}
	signal := healthSignal{Status: status, Details: map[string]any{
		"test_classes_found": len(testClasses),
		"test_classes_run":   ran,
		"test_classes_tried": limit,
		"classes":            totalClasses,
		"methods":            totalMethods,
		"alerts":             totalAlerts,
	}}
	signal.Unsearched = missed
	signal.Note = adt.UnsearchedNote(missed, limit, "test class")
	// The cap is not a failure, but it is still a reason this PASS covers less
	// than it sounds like it does.
	if len(testClasses) > limit {
		signal.Note = joinNotes(signal.Note, fmt.Sprintf(
			"only %d of the %d test classes in this package were run, so a passing status here is not a passing package.",
			limit, len(testClasses)))
	}
	return signal
}

// joinNotes puts two caveats in one field without producing a stray newline
// when one of them is absent.
func joinNotes(notes ...string) string {
	var kept []string
	for _, n := range notes {
		if n != "" {
			kept = append(kept, n)
		}
	}
	return strings.Join(kept, "\n")
}

func (s *Server) collectObjectATC(ctx context.Context, objType, objName string) healthSignal {
	objectURL := buildHealthObjectURL(objType, objName, "")
	if objectURL == "" {
		return healthSignal{Status: "UNKNOWN"}
	}
	result, err := s.adtClient.RunATCCheck(ctx, objectURL, "", 100)
	if err != nil {
		return healthSignal{Status: "ERROR", Details: map[string]any{"message": err.Error()}}
	}
	status, details := adtsource.ATCVerdict(result)
	return healthSignal{Status: status, Details: details}
}

func (s *Server) collectPackageATC(ctx context.Context, pkg string) healthSignal {
	objectURL := "/sap/bc/adt/packages/" + strings.ToLower(pkg)
	result, err := s.adtClient.RunATCCheck(ctx, objectURL, "", 200)
	if err != nil {
		return healthSignal{Status: "ERROR", Details: map[string]any{"message": err.Error()}}
	}
	status, details := adtsource.ATCVerdict(result)
	return healthSignal{Status: status, Details: details}
}

func (s *Server) collectObjectBoundaries(ctx context.Context, objType, objName, parent string) healthSignal {
	if objType != "CLAS" && objType != "PROG" && objType != "INTF" {
		return healthSignal{Status: "UNKNOWN"}
	}
	source, err := s.adtClient.GetSource(ctx, objType, objName, nil)
	if err != nil || source == "" {
		return healthSignal{Status: "ERROR", Details: map[string]any{"message": "failed to read source"}}
	}

	g := graph.New()
	nodeID := graph.NodeID(objType, objName)
	g.AddNode(&graph.Node{ID: nodeID, Name: objName, Type: objType})
	g.AddSourceDeps(nodeID, source)
	unresolved := s.resolvePackages(ctx, g)
	n := g.GetNode(nodeID)
	if n == nil || n.Package == "" {
		return healthSignal{Status: "UNKNOWN", Unsearched: unresolved, Note: unresolvedPackageNote(unresolved)}
	}
	report := g.CheckBoundaries(n.Package, &graph.BoundaryOptions{IncludeDynamic: true})
	status := "CLEAN"
	if report.Violations > 0 {
		status = "VIOLATIONS"
	}
	return healthSignal{
		Status: status,
		Details: map[string]any{
			"violations":       report.Violations,
			"crossed_packages": report.CrossedPackages,
			"dynamic":          report.Dynamic,
		},
		Unsearched: unresolved,
		Note:       unresolvedPackageNote(unresolved),
	}
}

func (s *Server) collectPackageBoundaries(ctx context.Context, pkg string) healthSignal {
	// The same scan check_boundaries makes. This collector had its own loop,
	// which took only classes, programs and interfaces and dropped every other
	// entry without a word — a package of function groups calling across
	// packages came back with nothing read, or with the classes beside them
	// read and the groups left out of a verdict that looked complete.
	scan, err := s.packageGraph(ctx, pkg, 1)
	if err != nil {
		return healthSignal{Status: "ERROR", Details: map[string]any{"message": err.Error()}}
	}
	g, count, missed, skipped := scan.Graph, scan.Read, scan.Unreadable, scan.Truncated
	unresolved := s.resolvePackages(ctx, g)
	report := g.CheckBoundaries(pkg, &graph.BoundaryOptions{IncludeDynamic: true})
	status := "CLEAN"
	if report.Violations > 0 {
		status = "VIOLATIONS"
	}
	// Nothing was read at all, so CLEAN would be a verdict on an empty graph.
	// This is not hypothetical: a package whose listing holds only sub-packages
	// scans zero objects and used to report "CLEAN, 0 violations", which reads
	// as a pass.
	if count == 0 {
		status = "UNKNOWN"
		if len(missed) > 0 {
			status = "ERROR"
		}
	}
	signal := healthSignal{Status: status, Details: map[string]any{
		"scanned_objects":   count,
		"violations":        report.Violations,
		"crossed_packages":  report.CrossedPackages,
		"violating_objects": report.ViolatingObjects,
	}}
	signal.Unsearched = append(missed, unresolved...)
	signal.Note = joinNotes(
		adt.UnsearchedNote(missed, count+len(missed), "object"),
		unresolvedPackageNote(unresolved),
	)
	if skipped > 0 {
		signal.Note = joinNotes(signal.Note, fmt.Sprintf(
			"only the first %d source-bearing objects were scanned; %d more were not looked at.", scan.Cap, skipped))
	}
	if count == 0 {
		signal.Note = joinNotes(signal.Note,
			"no source-bearing objects were read in this package, so there is no boundary verdict here — not a clean one.")
	}
	return signal
}

func (s *Server) collectObjectStaleness(ctx context.Context, objType, objName, parent string) healthSignal {
	revs, err := s.adtClient.GetRevisions(ctx, objType, objName, &adt.GetSourceOptions{Parent: parent})
	if err != nil {
		return healthSignal{Status: "ERROR", Details: map[string]any{"message": err.Error()}}
	}
	return stalenessFromRevisions(revs)
}

func (s *Server) collectPackageStaleness(ctx context.Context, pkg string) healthSignal {
	content, err := s.adtClient.GetPackage(ctx, pkg)
	if err != nil {
		return healthSignal{Status: "ERROR", Details: map[string]any{"message": err.Error()}}
	}

	var newest time.Time
	checked := 0
	attempted := 0
	var missed []adt.Unsearched
	for _, obj := range content.Objects {
		objType := adtsource.SourceKind(obj.Type)
		if objType != "CLAS" && objType != "PROG" && objType != "INTF" {
			continue
		}
		if checked >= 10 {
			break
		}
		attempted++
		revs, err := s.adtClient.GetRevisions(ctx, objType, obj.Name, nil)
		if err != nil {
			// Staleness is a maximum over dates. An object whose history could
			// not be read contributes no date, so the answer skews old, and
			// "this package has not been touched in two years" is precisely the
			// conclusion someone deletes code on.
			missed = append(missed, adt.Unsearched{Object: objType + " " + obj.Name, Reason: err.Error()})
			continue
		}
		if len(revs) == 0 {
			continue
		}
		// A date the server sent but we cannot parse is a local decoding
		// problem, not a failed question, and it is left as it was.
		tm, err := time.Parse(time.RFC3339, revs[0].Date)
		if err != nil {
			continue
		}
		if tm.After(newest) {
			newest = tm
		}
		checked++
	}
	note := adt.UnsearchedNote(missed, attempted, "object")
	if newest.IsZero() {
		return healthSignal{Status: "UNKNOWN", Unsearched: missed, Note: note}
	}
	signal := stalenessFromTime(newest, checked)
	signal.Unsearched = missed
	signal.Note = note
	return signal
}

// stalenessFromRevisions and stalenessFromTime wrap the shared verdicts in the
// MCP signal type.
func stalenessFromRevisions(revs []adt.Revision) healthSignal {
	status, details := adtsource.RevisionsVerdict(revs)
	return healthSignal{Status: status, Details: details}
}

func stalenessFromTime(tm time.Time, checked int) healthSignal {
	status, details := adtsource.StalenessVerdict(tm, checked)
	return healthSignal{Status: status, Details: details}
}

func summarizeHealth(signals map[string]healthSignal) healthSummary {
	if signals["tests"].Status == "FAIL" {
		return healthSummary{Status: "BAD", Headline: "Unit tests are failing"}
	}
	if signals["boundaries"].Status == "VIOLATIONS" {
		return healthSummary{Status: "WARN", Headline: "Boundary violations detected"}
	}
	if signals["atc"].Status == "FINDINGS" {
		return healthSummary{Status: "WARN", Headline: "ATC findings detected"}
	}
	if signals["staleness"].Status == "STALE" {
		return healthSummary{Status: "WARN", Headline: "Object or package appears stale"}
	}
	// Everything above is something we found. Below this line the report has
	// found nothing, and there are two ways to find nothing: because there is
	// nothing there, and because the looking failed. Only the first of them is
	// good news, and "No major health issues detected" claims the first while
	// being true of both.
	if gaps := incompleteSignalNames(signals); len(gaps) > 0 {
		return healthSummary{
			Status:   "UNKNOWN",
			Headline: "Nothing was found wrong, but " + strings.Join(gaps, " and ") + " could not be checked in full — see notes",
		}
	}
	return healthSummary{Status: "GOOD", Headline: "No major health issues detected"}
}

// incompleteSignalNames lists, in a stable order, the signals that reached their
// status without all the evidence.
func incompleteSignalNames(signals map[string]healthSignal) []string {
	var names []string
	for _, name := range []string{"tests", "atc", "boundaries", "staleness"} {
		if signals[name].incomplete() {
			names = append(names, name)
		}
	}
	return names
}

// healthNotes lifts every signal's caveat to the top of the document, prefixed
// with the signal it belongs to. A reader who trusts the summary and stops
// there has to meet the gaps on the way.
func healthNotes(signals map[string]healthSignal) []string {
	var notes []string
	for _, name := range []string{"tests", "atc", "boundaries", "staleness"} {
		sig := signals[name]
		if sig.Note != "" {
			notes = append(notes, name+": "+sig.Note)
		} else if sig.Status == "ERROR" {
			msg, _ := sig.Details["message"].(string)
			notes = append(notes, name+": this check failed, so it is not evidence of health. "+msg)
		}
	}
	return notes
}

func buildHealthObjectURL(objType, objName, parent string) string {
	switch objType {
	case "CLAS", "PROG", "INTF", "FUGR":
		return buildADTObjectURL(objType, objName)
	case "FUNC":
		if parent == "" {
			return ""
		}
		return fmt.Sprintf("/sap/bc/adt/functions/groups/%s/fmodules/%s", strings.ToLower(parent), strings.ToLower(objName))
	default:
		return ""
	}
}
