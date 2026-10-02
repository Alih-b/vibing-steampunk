package graph

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// BoundaryVerdict classifies a dependency relationship.
type BoundaryVerdict string

const (
	VerdictStandard    BoundaryVerdict = "STANDARD"     // SAP standard object → OK
	VerdictSamePackage BoundaryVerdict = "SAME_PACKAGE" // Same package → OK
	VerdictAllowed     BoundaryVerdict = "ALLOWED"      // Whitelisted Z-package → OK
	VerdictViolation   BoundaryVerdict = "VIOLATION"    // Cross-package Z* dep → PROBLEM
	VerdictDynamic     BoundaryVerdict = "DYNAMIC"      // Dynamic call, unresolvable → WARNING
	VerdictUnknown     BoundaryVerdict = "UNKNOWN"      // Package not resolved
)

// BoundaryEntry is a single dependency with its verdict.
type BoundaryEntry struct {
	From      *Node           `json:"from"`
	To        *Node           `json:"to"`
	Edge      *Edge           `json:"edge"`
	Verdict   BoundaryVerdict `json:"verdict"`
	TargetPkg string          `json:"target_package"`
}

// BoundaryReport is the result of a package boundary analysis.
type BoundaryReport struct {
	RootPackage string          `json:"root_package"`
	Whitelist   []string        `json:"whitelist"`
	TotalDeps   int             `json:"total_deps"`
	Entries     []BoundaryEntry `json:"entries"`

	// Aggregated counts
	Standard    int `json:"standard_count"`
	SamePackage int `json:"same_package_count"`
	Allowed     int `json:"allowed_count"`
	Violations  int `json:"violation_count"`
	Dynamic     int `json:"dynamic_count"`
	Unknown     int `json:"unknown_count"`

	// Violation detail: which packages are crossed
	CrossedPackages map[string]int `json:"crossed_packages"`
	// Objects that have violations
	ViolatingObjects []string `json:"violating_objects"`
}

// BoundaryOptions configures the boundary analysis.
type BoundaryOptions struct {
	// Whitelist of allowed Z-packages (supports glob: "Z*_COMMON", "$ZUTIL*")
	Whitelist []string
	// IncludeStandard includes standard deps in the report (default: false, they're just counted)
	IncludeStandard bool
	// IncludeDynamic includes dynamic call warnings (default: true)
	IncludeDynamic bool
	// EdgeKinds to consider (nil = all)
	EdgeKinds []EdgeKind
}

// CheckBoundaries analyzes whether dependencies of nodes in rootPackage
// cross package boundaries, with configurable whitelist.
func (g *Graph) CheckBoundaries(rootPackage string, opts *BoundaryOptions) *BoundaryReport {
	if opts == nil {
		opts = &BoundaryOptions{IncludeDynamic: true}
	}

	rootPkg := strings.ToUpper(strings.TrimSpace(rootPackage))

	report := &BoundaryReport{
		RootPackage:     rootPkg,
		Whitelist:       opts.Whitelist,
		CrossedPackages: make(map[string]int),
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	// Find all nodes in root package, in ID order so the walk is the same on
	// every run.
	var rootNodes []string
	for _, n := range g.nodes {
		if strings.EqualFold(n.Package, rootPkg) {
			rootNodes = append(rootNodes, n.ID)
		}
	}
	sort.Strings(rootNodes)

	// Analyze outgoing edges from root package nodes
	violatingObjs := make(map[string]bool)

	for _, nodeID := range rootNodes {
		edges := g.outEdges[nodeID]
		for _, e := range edges {
			// Filter by edge kind if specified
			if len(opts.EdgeKinds) > 0 && !containsKind(opts.EdgeKinds, e.Kind) {
				continue
			}

			fromNode := g.nodes[nodeID]
			toNode := g.nodes[e.To]

			entry := BoundaryEntry{
				From: fromNode,
				Edge: e,
			}

			// Dynamic calls
			if e.Kind == EdgeDynamic {
				entry.Verdict = VerdictDynamic
				entry.To = &Node{ID: e.To, Name: e.To, Type: "DYNAMIC"}
				report.Dynamic++
				if opts.IncludeDynamic {
					report.Entries = append(report.Entries, entry)
				}
				report.TotalDeps++
				continue
			}

			if toNode == nil {
				// Target not in graph — unknown
				entry.Verdict = VerdictUnknown
				entry.To = &Node{ID: e.To, Name: e.To}
				report.Unknown++
				report.Entries = append(report.Entries, entry)
				report.TotalDeps++
				continue
			}

			entry.To = toNode
			entry.TargetPkg = toNode.Package

			verdict := classify(rootPkg, toNode, opts.Whitelist)
			entry.Verdict = verdict

			switch verdict {
			case VerdictStandard:
				report.Standard++
				if opts.IncludeStandard {
					report.Entries = append(report.Entries, entry)
				}
			case VerdictSamePackage:
				report.SamePackage++
				// Always included — shows internal structure
				report.Entries = append(report.Entries, entry)
			case VerdictAllowed:
				report.Allowed++
				report.Entries = append(report.Entries, entry)
			case VerdictViolation:
				report.Violations++
				report.CrossedPackages[toNode.Package]++
				violatingObjs[fromNode.ID] = true
				report.Entries = append(report.Entries, entry)
			default:
				report.Unknown++
				report.Entries = append(report.Entries, entry)
			}

			report.TotalDeps++
		}
	}

	// Collect violating objects
	for id := range violatingObjs {
		report.ViolatingObjects = append(report.ViolatingObjects, id)
	}
	sort.Strings(report.ViolatingObjects)
	sortBoundaryEntries(report.Entries)

	return report
}

// verdictOrder ranks verdicts from the one that needs acting on to the ones
// that are fine: a violation, then a reference that could not be judged (it may
// hide one), then a dynamic call, then the allowed kinds.
var verdictOrder = []BoundaryVerdict{
	VerdictViolation, VerdictUnknown, VerdictDynamic, VerdictAllowed, VerdictSamePackage, VerdictStandard,
}

func verdictRank(v BoundaryVerdict) int {
	for i, o := range verdictOrder {
		if o == v {
			return i
		}
	}
	return len(verdictOrder)
}

// sortBoundaryEntries orders entries worst verdict first (verdictOrder), then by
// the referencing object's ID. Within one object the entries keep the order its
// edges were added in, which for parsed source is the order of the statements.
func sortBoundaryEntries(entries []BoundaryEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if ra, rb := verdictRank(a.Verdict), verdictRank(b.Verdict); ra != rb {
			return ra < rb
		}
		return a.From.ID < b.From.ID
	})
}

// CrossedPackageCount is one line of "Packages crossed".
type CrossedPackageCount struct {
	Package string
	Refs    int
}

// SortedCrossedPackages lists the crossed packages with the most references
// first, and by name where the counts tie. CrossedPackages is a map, and
// printing it as it ranged put the same packages in a different order on every
// run.
func (r *BoundaryReport) SortedCrossedPackages() []CrossedPackageCount {
	out := make([]CrossedPackageCount, 0, len(r.CrossedPackages))
	for pkg, n := range r.CrossedPackages {
		out = append(out, CrossedPackageCount{Package: pkg, Refs: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Refs != out[j].Refs {
			return out[i].Refs > out[j].Refs
		}
		return out[i].Package < out[j].Package
	})
	return out
}

// classify determines the verdict for a dependency target.
func classify(rootPkg string, target *Node, whitelist []string) BoundaryVerdict {
	// Standard SAP object
	if IsStandardObject(target.Name) {
		return VerdictStandard
	}

	targetPkg := strings.ToUpper(strings.TrimSpace(target.Package))

	// No package info
	if targetPkg == "" {
		return VerdictUnknown
	}

	// Same package
	if targetPkg == rootPkg {
		return VerdictSamePackage
	}

	// Check whitelist (supports glob patterns)
	for _, pattern := range whitelist {
		pat := strings.ToUpper(strings.TrimSpace(pattern))
		if pat == targetPkg {
			return VerdictAllowed
		}
		if matched, _ := filepath.Match(pat, targetPkg); matched {
			return VerdictAllowed
		}
	}

	// Custom Z/Y object in different package → violation
	return VerdictViolation
}

// --- Formatting ---

// FormatText returns a human-readable boundary report.
func (r *BoundaryReport) FormatText() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Package Boundary Analysis: %s\n", r.RootPackage))
	sb.WriteString(fmt.Sprintf("Total dependencies: %d\n", r.TotalDeps))
	if len(r.Whitelist) > 0 {
		sb.WriteString(fmt.Sprintf("Whitelist: %s\n", strings.Join(r.Whitelist, ", ")))
	}
	sb.WriteString("\n")

	// Group entries by verdict
	groups := map[BoundaryVerdict][]BoundaryEntry{}
	for _, e := range r.Entries {
		groups[e.Verdict] = append(groups[e.Verdict], e)
	}

	// Same package
	if entries, ok := groups[VerdictSamePackage]; ok {
		sb.WriteString(fmt.Sprintf("--- SAME PACKAGE (%d) ---\n", len(entries)))
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("  %s → %s (%s)\n", e.From.Name, e.To.Name, e.Edge.Kind))
		}
		sb.WriteString("\n")
	}

	// Allowed
	if entries, ok := groups[VerdictAllowed]; ok {
		sb.WriteString(fmt.Sprintf("--- ALLOWED (%d) ---\n", len(entries)))
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("  %s → %s (%s, pkg: %s)\n", e.From.Name, e.To.Name, e.Edge.Kind, e.TargetPkg))
		}
		sb.WriteString("\n")
	}

	// Violations
	if entries, ok := groups[VerdictViolation]; ok {
		sb.WriteString(fmt.Sprintf("--- VIOLATIONS (%d) ---\n", len(entries)))
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("  ✗ %s → %s (%s, pkg: %s) [%s]\n",
				e.From.Name, e.To.Name, e.Edge.Kind, e.TargetPkg, e.Edge.RefDetail))
		}
		sb.WriteString("\n")
	}

	// Dynamic warnings
	if entries, ok := groups[VerdictDynamic]; ok {
		sb.WriteString(fmt.Sprintf("--- DYNAMIC CALLS (%d) ---\n", len(entries)))
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("  ⚠ %s → %s [%s]\n", e.From.Name, e.To.ID, e.Edge.RefDetail))
		}
		sb.WriteString("\n")
	}

	// Summary
	sb.WriteString("--- SUMMARY ---\n")
	sb.WriteString(fmt.Sprintf("  Standard (SAP):    %d\n", r.Standard))
	sb.WriteString(fmt.Sprintf("  Same package:      %d\n", r.SamePackage))
	sb.WriteString(fmt.Sprintf("  Whitelisted:       %d\n", r.Allowed))
	sb.WriteString(fmt.Sprintf("  Violations:        %d\n", r.Violations))
	sb.WriteString(fmt.Sprintf("  Dynamic (warning): %d\n", r.Dynamic))
	if r.Unknown > 0 {
		sb.WriteString(fmt.Sprintf("  Unknown:           %d\n", r.Unknown))
	}

	if len(r.CrossedPackages) > 0 {
		sb.WriteString("\n  Packages crossed:\n")
		for _, c := range r.SortedCrossedPackages() {
			sb.WriteString(fmt.Sprintf("    %s — %d refs\n", c.Package, c.Refs))
		}
	}

	if r.Unknown > 0 {
		// classify sends a reference whose package could not be determined to
		// Unknown and never to Violation, so an unknown can only ever hide a
		// violation — never invent one. A tick beside a count of them is the
		// reassuring direction of wrong, and this verdict is read by people
		// deciding whether to ship.
		sb.WriteString(fmt.Sprintf("\n  ? INCONCLUSIVE — %d of %d references could not be attributed to a package, "+
			"and an unattributed reference is one this cannot judge.\n", r.Unknown, r.TotalDeps))
		for _, e := range r.Entries {
			if e.Verdict == VerdictUnknown {
				// IDs, not the nodes: a *Node formats as a Go struct literal.
				sb.WriteString(fmt.Sprintf("    unattributed: %s → %s (%s)\n", e.From.ID, e.To.ID, e.Edge.Kind))
			}
		}
		if r.Violations > 0 {
			sb.WriteString(fmt.Sprintf("  ✗ %d violation(s) in %d object(s) among the references it could judge\n",
				r.Violations, len(r.ViolatingObjects)))
		}
	} else if r.Violations == 0 && r.Dynamic == 0 {
		sb.WriteString("\n  ✓ CLEAN — no boundary violations\n")
	} else if r.Violations > 0 {
		sb.WriteString(fmt.Sprintf("\n  ✗ %d violation(s) in %d object(s)\n", r.Violations, len(r.ViolatingObjects)))
	}

	return sb.String()
}

// IsClean returns true if there are no violations.
func (r *BoundaryReport) IsClean() bool {
	return r.Violations == 0
}

// ExitCode returns 0 for clean, 1 for violations (for CI/CD gates).
func (r *BoundaryReport) ExitCode() int {
	if r.IsClean() {
		return 0
	}
	return 1
}

func containsKind(kinds []EdgeKind, k EdgeKind) bool {
	for _, kk := range kinds {
		if kk == k {
			return true
		}
	}
	return false
}
