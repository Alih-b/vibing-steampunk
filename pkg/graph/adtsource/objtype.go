package adtsource

import "strings"

// SourceKind names the kind of source-bearing object an ADT type code stands
// for, in the terms adt.Client.GetSource takes, or "" when the code is not one
// of them.
//
// A package listing does not say "CLAS"; it says "CLAS/OC". Comparing that with
// the bare type matched nothing, and a collector that skipped every object
// reported an empty package. Cutting the code at the slash is the wrong fix,
// though, because the part after the slash matters: PROG/I is an include, not a
// program, and is read at a different resource; FUGR/FF is one function module,
// not its group; TABL/DS is a structure and TABL/DT a table. So the codes are
// mapped one by one, and a code not listed here is not guessed at.
//
// Bare codes, as TADIR gives them, map to themselves.
func SourceKind(adtType string) string {
	switch strings.ToUpper(strings.TrimSpace(adtType)) {
	case "CLAS", "CLAS/OC":
		return "CLAS"
	case "INTF", "INTF/OI":
		return "INTF"
	case "PROG", "PROG/P":
		return "PROG"
	case "INCL", "PROG/I":
		return "INCL"
	case "FUGR", "FUGR/F":
		return "FUGR"
	case "FUNC", "FUGR/FF":
		return "FUNC"
	}
	return ""
}

// IsNonSourceType reports whether a package-listing code names an object that
// carries no ABAP source a dependency scan could read: sub-packages and the
// dictionary and administrative objects whose dependencies, if any, are not
// in code. Such entries are excused from a scan.
//
// The list is short on purpose. A code that is neither here nor a SourceKind
// is one the scan does not understand, and the caller reports it as unsearched
// rather than letting it vanish: an object nobody read contributes no edges,
// and no edges is what a clean object looks like.
func IsNonSourceType(adtType string) bool {
	switch strings.ToUpper(strings.TrimSpace(adtType)) {
	case "DEVC/K",
		"TABL/DT", "TABL/DS",
		"DTEL/DE", "DOMA/DD", "TTYP/DA",
		"VIEW/DV", "SHLP/SH", "ENQU/DL",
		"MSAG/N", "TRAN/T",
		// ABAP Push and Messaging Channel definitions: configuration that
		// names a handler class, which is read in its own right. The listing
		// gives these without a subtype.
		"SAPC", "SAMC":
		return true
	}
	return false
}
