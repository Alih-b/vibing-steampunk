package graph

import (
	"sort"
	"strings"
)

// Complete reports whether every object in scope was read. A verdict from an
// incomplete scan is still useful and must not be presented as a whole one.
func (r *CrossingReport) Complete() bool {
	return r.SourceAttempted == 0 || r.SourceRead == r.SourceAttempted
}

// Missed is how many objects in scope could not be read.
func (r *CrossingReport) Missed() int {
	if r.SourceAttempted <= r.SourceRead {
		return 0
	}
	return r.SourceAttempted - r.SourceRead
}

// Caveat is the sentence a text report must carry when the scan was partial,
// or the empty string when it was not.
func (r *CrossingReport) Caveat() string {
	if r.Complete() {
		return ""
	}
	return "Read " + itoa(r.SourceRead) + " of " + itoa(r.SourceAttempted) +
		" objects in scope: " + itoa(r.Missed()) +
		" could not be read, so a crossing in any of them is missing from this report."
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// CrossingDirection classifies the direction of a cross-package dependency.
type CrossingDirection string

const (
	CrossUpward     CrossingDirection = "UPWARD"      // child → parent/ancestor — OK
	CrossUpwardSkip CrossingDirection = "UPWARD_SKIP" // child → grandparent+ (skipping levels) — WARN
	CrossCommon     CrossingDirection = "COMMON"      // anything → common/shared package (_00) — OK
	CrossSibling    CrossingDirection = "SIBLING"     // between siblings at same level — BAD
	CrossDownward   CrossingDirection = "DOWNWARD"    // parent → child — BAD
	CrossCommonDown CrossingDirection = "COMMON_DOWN" // common package → specific module — BAD
	CrossExternal   CrossingDirection = "EXTERNAL"    // different hierarchy entirely — INFO
	CrossSame       CrossingDirection = "SAME"        // same package — OK (not a crossing)
	CrossStandard   CrossingDirection = "STANDARD"    // standard SAP — ignore
)

// CrossingEntry represents a single directional crossing.
type CrossingEntry struct {
	SourceObject  string            `json:"sourceObject"`
	SourceType    string            `json:"sourceType,omitempty"`
	SourcePackage string            `json:"sourcePackage"`
	TargetObject  string            `json:"targetObject"`
	TargetType    string            `json:"targetType,omitempty"`
	TargetPackage string            `json:"targetPackage"`
	Direction     CrossingDirection `json:"direction"`
	EdgeKind      string            `json:"edgeKind"`
	RefDetail     string            `json:"refDetail,omitempty"`
}

// CrossingReport is the result of a directional boundary crossing analysis.
type CrossingReport struct {
	RootPackage     string `json:"rootPackage"`
	PackagesScanned int    `json:"packagesScanned"`
	// ObjectsScanned counts objects actually read. It used to count objects
	// attempted, so a package where a third of the programs answered 404 still
	// reported every one of them as scanned.
	ObjectsScanned int `json:"objectsScanned"`
	// SourceRead and SourceAttempted count objects whose source the scan tried
	// to read, and Unreadable names the ones it could not. They are separate
	// from ObjectsScanned on purpose: that counts nodes in the graph, which
	// includes targets discovered through edges and is therefore not a figure
	// about coverage. Two numbers that mean different things must not share a
	// name, which is how a header saying 167 came to sit above a caveat saying
	// 167 of 222.
	//
	// Fields rather than a sentence, because this report is also emitted as
	// JSON, where prose is not a caveat but a parse error.
	//
	// The direction of the error is worth keeping in mind: an object that could
	// not be read contributes no edges, and an edge never extracted cannot
	// cross a boundary. So this can only ever hide crossings, never invent
	// them, which makes a clean verdict exactly the case where it matters most.
	SourceRead      int      `json:"sourceRead,omitempty"`
	SourceAttempted int      `json:"sourceAttempted,omitempty"`
	Unreadable      []string `json:"unreadable,omitempty"`

	Entries []CrossingEntry `json:"entries,omitempty"`

	// Counts by direction
	Upward     int `json:"upward"`
	UpwardSkip int `json:"upwardSkip"`
	Common     int `json:"common"`
	Sibling    int `json:"sibling"`
	Downward   int `json:"downward"`
	CommonDown int `json:"commonDown"`
	External   int `json:"external"`
	Dynamic    int `json:"dynamic"`

	// Circular: pairs of sibling packages with deps in both directions
	Circular []string `json:"circular,omitempty"`
}

// CrossingOptions configures the directional crossing analysis.
type CrossingOptions struct {
	// CommonPatterns identifies "common" packages by suffix (default: ["_00"])
	CommonPatterns []string
	// TestPatterns identifies test packages — sibling crossings from test packages are OK.
	// A package is a test package when its name ends with a pattern, or with the
	// pattern plus "S": $ZLLM_TEST and $ZLLM_TESTS are, $ZLLM_TESTING is not.
	// The exemption waives a violation, so it matches the name's end as
	// CommonPatterns does; it used to match anywhere in the name.
	TestPatterns []string
	// UpwardSkipThreshold: how many levels of skip before flagging (default: 2)
	UpwardSkipThreshold int
}

func defaultCrossingOptions() *CrossingOptions {
	return &CrossingOptions{
		CommonPatterns:      []string{"_00"},
		TestPatterns:        []string{"_TEST"},
		UpwardSkipThreshold: 2,
	}
}

// ClassifyCrossing determines the direction of a dependency from sourcePackage to targetPackage
// within a package hierarchy defined by scope.
func ClassifyCrossing(sourcePackage, targetPackage string, scope *PackageScope, opts *CrossingOptions) CrossingDirection {
	if opts == nil {
		opts = defaultCrossingOptions()
	}

	src := strings.ToUpper(strings.TrimSpace(sourcePackage))
	tgt := strings.ToUpper(strings.TrimSpace(targetPackage))

	if src == tgt {
		return CrossSame
	}

	// External: target not in scope
	if !scope.InScope(tgt) {
		return CrossExternal
	}

	// Is target a "common" package?
	if isCommonPackage(tgt, opts.CommonPatterns) {
		// But if source is also common and target is not → COMMON_DOWN
		if isCommonPackage(src, opts.CommonPatterns) && !isCommonPackage(tgt, opts.CommonPatterns) {
			return CrossCommonDown
		}
		return CrossCommon
	}

	// Is source a "common" package pointing to a non-common sibling?
	if isCommonPackage(src, opts.CommonPatterns) {
		return CrossCommonDown
	}

	// Check ancestry using the hierarchy map
	// Is target an ancestor of source? → UPWARD
	if isAncestor(tgt, src, scope.Hierarchy) {
		depth := ancestorDepth(tgt, src, scope.Hierarchy)
		if depth > opts.UpwardSkipThreshold {
			return CrossUpwardSkip
		}
		return CrossUpward
	}

	// Is target a descendant of source? → DOWNWARD
	if isAncestor(src, tgt, scope.Hierarchy) {
		return CrossDownward
	}

	// Fallback: try prefix-based hierarchy when TDEVC hierarchy is incomplete
	if len(scope.Hierarchy) == 0 || !hasHierarchyEntry(src, scope.Hierarchy) {
		return classifyByPrefix(src, tgt, opts)
	}

	// Same level, different branch → SIBLING
	return CrossSibling
}

// isAncestor returns true if 'ancestor' is an ancestor of 'child' in the hierarchy.
func isAncestor(ancestor, child string, hierarchy map[string]string) bool {
	current := child
	for i := 0; i < 20; i++ { // safety limit
		parent, ok := hierarchy[current]
		if !ok || parent == "" {
			return false
		}
		if parent == ancestor {
			return true
		}
		current = parent
	}
	return false
}

// ancestorDepth returns how many levels up 'ancestor' is from 'child'.
func ancestorDepth(ancestor, child string, hierarchy map[string]string) int {
	current := child
	depth := 0
	for i := 0; i < 20; i++ {
		parent, ok := hierarchy[current]
		if !ok || parent == "" {
			return depth
		}
		depth++
		if parent == ancestor {
			return depth
		}
		current = parent
	}
	return depth
}

func hasHierarchyEntry(pkg string, hierarchy map[string]string) bool {
	if _, ok := hierarchy[pkg]; ok {
		return true
	}
	// Also check if pkg is a parent of anything
	for _, parent := range hierarchy {
		if parent == pkg {
			return true
		}
	}
	return false
}

// classifyByPrefix uses package name prefix matching when TDEVC hierarchy is unavailable.
func classifyByPrefix(src, tgt string, opts *CrossingOptions) CrossingDirection {
	// Target is a prefix of source → UPWARD (tgt is parent of src by name)
	if strings.HasPrefix(src, tgt+"_") || strings.HasPrefix(src, tgt) && len(src) > len(tgt) {
		return CrossUpward
	}

	// Source is a prefix of target → DOWNWARD
	if strings.HasPrefix(tgt, src+"_") || strings.HasPrefix(tgt, src) && len(tgt) > len(src) {
		return CrossDownward
	}

	// Find common prefix to determine if siblings
	commonRoot := longestCommonPrefix(src, tgt)
	if commonRoot != "" && (strings.HasSuffix(commonRoot, "_") || commonRoot == src[:strings.LastIndex(src, "_")+1]) {
		return CrossSibling
	}

	return CrossSibling // default for same-hierarchy non-ancestor/descendant
}

func longestCommonPrefix(a, b string) string {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		if a[i] != b[i] {
			return a[:i]
		}
	}
	return a[:minLen]
}

func isCommonPackage(pkg string, patterns []string) bool {
	upper := strings.ToUpper(pkg)
	for _, p := range patterns {
		if strings.HasSuffix(upper, strings.ToUpper(p)) {
			return true
		}
	}
	return false
}

// AnalyzeCrossings performs directional boundary analysis on a graph within a scope.
func AnalyzeCrossings(g *Graph, scope *PackageScope, opts *CrossingOptions) *CrossingReport {
	if opts == nil {
		opts = defaultCrossingOptions()
	}

	report := &CrossingReport{
		RootPackage:     scope.RootPackage,
		PackagesScanned: len(scope.Packages),
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	// Find all nodes in scope. The result does not depend on the order they are
	// walked in (see analyzeCrossingsFrom); they are sorted anyway so that the
	// walk itself, and the guessed packages it writes into the graph, happen
	// the same way on every run.
	var scopeNodes []string
	for _, n := range g.nodes {
		if scope.InScope(n.Package) {
			scopeNodes = append(scopeNodes, n.ID)
		}
	}
	sort.Strings(scopeNodes)
	report.ObjectsScanned = len(scopeNodes)

	g.analyzeCrossingsFrom(scopeNodes, scope, opts, report)
	return report
}

// analyzeCrossingsFrom walks the out-edges of nodeIDs and fills in report's
// entries, counts and circular pairs. The caller holds g.mu.
//
// Its answer is the same whatever order nodeIDs come in, and that is a
// property the tests check by walking one graph both ways:
//
//   - A pair is one source node and one target node, by ID — type and name.
//     It used to be by name alone, so CLAS:Z_DUP → CLAS:Z_TARGET and
//     PROG:Z_DUP → PROG:Z_TARGET were one pair, and whichever was walked first
//     hid the other, a downward violation included.
//   - When several edges join the same pair, the worst direction is kept
//     (CrossingDirectionOrder), not the first one seen.
//   - A test package's sibling crossing is exempt whether the target's package
//     was looked up or guessed from its name. The guessed path used to skip
//     the exemption, so the verdict on a test package's call depended on
//     whether an earlier edge had already written the guess into the graph.
//   - The counts and the circular pairs are computed from the kept entries,
//     so they cannot disagree with them.
func (g *Graph) analyzeCrossingsFrom(nodeIDs []string, scope *PackageScope, opts *CrossingOptions, report *CrossingReport) {
	best := make(map[[2]string]CrossingEntry)
	keep := func(from, to string, e CrossingEntry) {
		key := [2]string{from, to}
		if old, ok := best[key]; ok && directionRank(old.Direction) <= directionRank(e.Direction) {
			return
		}
		best[key] = e
	}

	for _, nodeID := range nodeIDs {
		fromNode := g.nodes[nodeID]
		for _, e := range g.outEdges[nodeID] {
			toNode := g.nodes[e.To]

			// Dynamic calls
			if e.Kind == EdgeDynamic {
				report.Dynamic++
				continue
			}

			if toNode == nil || fromNode == nil {
				continue
			}

			// Standard SAP
			if IsStandardObject(toNode.Name) {
				continue
			}

			srcPkg := strings.ToUpper(fromNode.Package)
			tgtPkg := strings.ToUpper(toNode.Package)

			if srcPkg == "" || srcPkg == tgtPkg {
				continue
			}

			var direction CrossingDirection
			if tgtPkg == "" {
				// Unresolved package on a custom object: guess it from the
				// name. The guess is written into the graph, as it always was;
				// a later edge to this node then finds the same package by
				// lookup and is classified the same way.
				direction = CrossExternal
				if guessed := GuessPackageFromName(toNode.Name); guessed != "" {
					toNode.Package = guessed
					tgtPkg = guessed
					direction = ClassifyCrossing(srcPkg, tgtPkg, scope, opts)
				}
				if direction == CrossSame {
					continue
				}
			} else {
				direction = ClassifyCrossing(srcPkg, tgtPkg, scope, opts)
			}

			// Skip test package sibling crossings — tests are expected to reach into siblings
			if direction == CrossSibling && isTestPackage(srcPkg, opts.TestPatterns) {
				continue
			}

			keep(fromNode.ID, toNode.ID, CrossingEntry{
				SourceObject:  fromNode.Name,
				SourceType:    fromNode.Type,
				SourcePackage: srcPkg,
				TargetObject:  toNode.Name,
				TargetType:    toNode.Type,
				TargetPackage: tgtPkg,
				Direction:     direction,
				EdgeKind:      string(e.Kind),
				RefDetail:     e.RefDetail,
			})
		}
	}

	// Track sibling direction pairs for circular detection
	siblingPairs := make(map[string]map[string]bool) // srcPkg → set of tgtPkgs
	for _, entry := range best {
		report.Entries = append(report.Entries, entry)
		switch entry.Direction {
		case CrossUpward:
			report.Upward++
		case CrossUpwardSkip:
			report.UpwardSkip++
		case CrossCommon:
			report.Common++
		case CrossSibling:
			report.Sibling++
			if siblingPairs[entry.SourcePackage] == nil {
				siblingPairs[entry.SourcePackage] = make(map[string]bool)
			}
			siblingPairs[entry.SourcePackage][entry.TargetPackage] = true
		case CrossDownward:
			report.Downward++
		case CrossCommonDown:
			report.CommonDown++
		default:
			report.External++
		}
	}

	// Detect circular sibling dependencies. Each pair is named once, with the
	// alphabetically first package on the left, and the list is sorted.
	for srcPkg, targets := range siblingPairs {
		for tgtPkg := range targets {
			if srcPkg < tgtPkg && siblingPairs[tgtPkg][srcPkg] {
				report.Circular = append(report.Circular, srcPkg+" <-> "+tgtPkg)
			}
		}
	}
	sort.Strings(report.Circular)

	sortCrossingEntries(report.Entries)
}

// CrossingDirectionOrder ranks the directions from worst to most benign: the
// three that break the hierarchy (sibling, downward, common-to-specific), then
// a crossing out of the hierarchy altogether, then the ones that follow it.
// Reports list their entries in this order.
var CrossingDirectionOrder = []CrossingDirection{
	CrossSibling, CrossDownward, CrossCommonDown,
	CrossExternal, CrossUpward, CrossUpwardSkip, CrossCommon,
}

func directionRank(d CrossingDirection) int {
	for i, o := range CrossingDirectionOrder {
		if o == d {
			return i
		}
	}
	return len(CrossingDirectionOrder)
}

// sortCrossingEntries puts the entries in the order a reader triages them:
// worst direction first (CrossingDirectionOrder), then by source package and
// object, then by target package and object, then by edge kind and detail. The
// order used to be whatever the graph's maps handed out, so the same package
// printed its crossings differently on every run, and a diff of two runs
// showed changes where there were none.
func sortCrossingEntries(entries []CrossingEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if ra, rb := directionRank(a.Direction), directionRank(b.Direction); ra != rb {
			return ra < rb
		}
		for _, k := range [][2]string{
			{a.SourcePackage, b.SourcePackage},
			{a.SourceObject, b.SourceObject},
			{a.SourceType, b.SourceType},
			{a.TargetPackage, b.TargetPackage},
			{a.TargetObject, b.TargetObject},
			{a.TargetType, b.TargetType},
			{a.EdgeKind, b.EdgeKind},
			{a.RefDetail, b.RefDetail},
		} {
			if k[0] != k[1] {
				return k[0] < k[1]
			}
		}
		return false
	})
}

// GuessPackageFromName infers a likely package name from an object name.
// ZCL_LLM_00_CACHE → $ZLLM_00, ZIF_RAY_00_NODE → $ZRAY_00, ZRAY_00_CCLM → $ZRAY_00
func GuessPackageFromName(objName string) string {
	upper := strings.ToUpper(strings.TrimSpace(objName))
	if upper == "" || !strings.HasPrefix(upper, "Z") {
		return ""
	}
	// Strip type prefixes: ZCL_, ZIF_, ZCX_, ZTT_
	core := upper
	prefixes := []string{"ZCL_", "ZIF_", "ZCX_", "ZTT_"}
	for _, p := range prefixes {
		if strings.HasPrefix(core, p) {
			core = core[len(p):]
			break
		}
	}
	// core is now e.g. "LLM_00_CACHE" or "RAY_00_CCLM"
	// For names that didn't have a type prefix (ZRAY_00_CCLM), strip leading Z
	if strings.HasPrefix(core, "Z") {
		core = core[1:]
	}
	// Find project prefix + module: first two _-separated segments
	parts := strings.SplitN(core, "_", 3)
	if len(parts) < 2 {
		return ""
	}
	return "$Z" + parts[0] + "_" + parts[1]
}

func isTestPackage(pkg string, patterns []string) bool {
	upper := strings.ToUpper(pkg)
	for _, p := range patterns {
		p = strings.ToUpper(p)
		if strings.HasSuffix(upper, p) || strings.HasSuffix(upper, p+"S") {
			return true
		}
	}
	return false
}
