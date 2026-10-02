package adt

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ABAPFileInfo contains parsed information about an ABAP source file.
type ABAPFileInfo struct {
	FilePath          string
	ObjectType        CreatableObjectType
	ObjectName        string
	ParentName        string           // For function modules: the function group name
	Description       string           // Parsed from comments if available
	ClassIncludeType  ClassIncludeType // For class includes (testclasses, definitions, etc.)
	HasDefinition     bool             // For classes
	HasImplementation bool
	HasTestClasses    bool
}

// extractFunctionGroupFromFilename extracts the function group name from abapGit-style filenames.
// Pattern: {fugr_name}.fugr.{func_name}.func.abap → FUGR_NAME
// Example: zvsp_report.fugr.z_vsp_run_report.func.abap → ZVSP_REPORT
// Example: #aif#util.fugr.#aif#func.func.abap → /AIF/UTIL (namespaced)
func extractFunctionGroupFromFilename(filePath string) string {
	baseName := filepath.Base(filePath)
	// Pattern: {fugr}.fugr.{func}.func.abap
	if strings.HasSuffix(strings.ToLower(baseName), ".func.abap") {
		// Find .fugr. in the filename
		lowerName := strings.ToLower(baseName)
		fugrIdx := strings.Index(lowerName, ".fugr.")
		if fugrIdx > 0 {
			name := strings.ToUpper(baseName[:fugrIdx])
			// Convert # back to / for namespaced objects (abapGit convention)
			name = strings.ReplaceAll(name, "#", "/")
			return name
		}
	}
	return ""
}

// extractClassNameFromFilename extracts the parent class name from abapGit-style filenames.
// Examples:
//   - zcl_foo.clas.testclasses.abap → ZCL_FOO
//   - zcl_foo.clas.locals_def.abap → ZCL_FOO
//   - zcl_foo.clas.locals_imp.abap → ZCL_FOO
//   - zcl_foo.clas.macros.abap → ZCL_FOO
//   - zcl_foo.clas.abap → ZCL_FOO
//   - #dmo#cl_flight.clas.abap → /DMO/CL_FLIGHT (namespaced)
func extractClassNameFromFilename(filePath string) string {
	baseName := filepath.Base(filePath)

	// Remove known suffixes in order of specificity
	suffixes := []string{
		".clas.testclasses.abap",
		".clas.locals_def.abap",
		".clas.locals_imp.abap",
		".clas.macros.abap",
		".clas.abap",
	}

	for _, suffix := range suffixes {
		if strings.HasSuffix(strings.ToLower(baseName), suffix) {
			name := baseName[:len(baseName)-len(suffix)]
			name = strings.ToUpper(name)
			// Convert # back to / for namespaced objects (abapGit convention)
			name = strings.ReplaceAll(name, "#", "/")
			return name
		}
	}

	return ""
}

// ParseABAPFile analyzes an ABAP source file and extracts metadata.
//
// Suffixes are matched without regard to case: ZCL_FOO.CLAS.ABAP is read the
// same as zcl_foo.clas.abap. Only the suffix is case-blind; object names are
// uppercased as ABAP has them.
//
// A file named with its type ({name}.prog.abap, {name}.clas.abap, ...) is
// that type, and its object is the one in its file name, with abapGit's #
// standing for the namespace slash. The content is never the source of the
// name: an abapGit TOP include zrep_top.prog.abap opens with PROGRAM zrep,
// and taking the name from there deployed the include over its main program.
// The content is checked instead, and a file whose main statement names
// another object is refused.
//
// A {name}.prog.abap whose sibling {name}.prog.xml says SUBC I (abapGit's mark
// for an include) is the include {name}. Without that mark a .prog.abap must
// open with REPORT or PROGRAM naming {name}.
//
// A plain {name}.abap file is typed from its first statement, and only when
// the statement names the object the file is named after.
func ParseABAPFile(filePath string) (*ABAPFileInfo, error) {
	info := &ABAPFileInfo{FilePath: filePath}
	baseName := filepath.Base(filePath)
	lower := strings.ToLower(baseName)
	hasSuffix := func(suffix string) bool { return strings.HasSuffix(lower, suffix) }
	// typed names the object after the file, without its suffix.
	typed := func(kind CreatableObjectType, suffix string) error {
		name, err := typedStemName(baseName, suffix)
		if err != nil {
			return err
		}
		info.ObjectType = kind
		info.ObjectName = name
		return nil
	}

	var err error
	switch {
	// Class includes (must be before .clas.abap). Their content is local
	// classes and test classes, named anything; the file names the class.
	case hasSuffix(".clas.testclasses.abap"):
		err = typed(ObjectTypeClass, ".clas.testclasses.abap")
		info.ClassIncludeType = ClassIncludeTestClasses
	case hasSuffix(".clas.locals_def.abap"):
		err = typed(ObjectTypeClass, ".clas.locals_def.abap")
		info.ClassIncludeType = ClassIncludeDefinitions
	case hasSuffix(".clas.locals_imp.abap"):
		err = typed(ObjectTypeClass, ".clas.locals_imp.abap")
		info.ClassIncludeType = ClassIncludeImplementations
	case hasSuffix(".clas.macros.abap"):
		err = typed(ObjectTypeClass, ".clas.macros.abap")
		info.ClassIncludeType = ClassIncludeMacros
	// Main class
	case hasSuffix(".clas.abap"):
		info.ClassIncludeType = ClassIncludeMain
		if err = typed(ObjectTypeClass, ".clas.abap"); err == nil {
			err = checkGlobalDeclaration(filePath, "CLASS", info.ObjectName)
		}
	case hasSuffix(".prog.abap"):
		if err = typed(ObjectTypeProgram, ".prog.abap"); err != nil {
			break
		}
		var subc string
		var found bool
		subc, found, err = progXMLSubc(filePath, info.ObjectName)
		if err != nil {
			break
		}
		if found && subc == "I" {
			// abapGit keeps an include as a .prog.abap and marks it in the
			// .prog.xml beside it. What it opens with does not matter: a TOP
			// include opens with its main program's PROGRAM statement.
			info.ObjectType = ObjectTypeInclude
			break
		}
		err = checkProgramStatement(filePath, info.ObjectName, found)
	case hasSuffix(".incl.abap"):
		// An include has no statement of its own to check; the file names it.
		err = typed(ObjectTypeInclude, ".incl.abap")
	case hasSuffix(".intf.abap"):
		if err = typed(ObjectTypeInterface, ".intf.abap"); err == nil {
			err = checkGlobalDeclaration(filePath, "INTERFACE", info.ObjectName)
		}
	case hasSuffix(".fugr.abap"):
		if err = typed(ObjectTypeFunctionGroup, ".fugr.abap"); err == nil {
			err = checkFunctionPoolStatement(filePath, info.ObjectName)
		}
	case hasSuffix(".func.abap"):
		info.ObjectType = ObjectTypeFunctionMod
		info.ParentName = extractFunctionGroupFromFilename(filePath)
		if info.ObjectName, err = funcFileModuleName(baseName); err == nil {
			err = checkFunctionStatement(filePath, info.ObjectName)
		}
	// RAP object types (ABAPGit-compatible extensions)
	case hasSuffix(".ddls.asddls"):
		if err = typed(ObjectTypeDDLS, ".ddls.asddls"); err == nil {
			err = checkSourceDefinition(filePath, ObjectTypeDDLS, info.ObjectName)
		}
	case hasSuffix(".bdef.asbdef"):
		if err = typed(ObjectTypeBDEF, ".bdef.asbdef"); err == nil {
			err = checkSourceDefinition(filePath, ObjectTypeBDEF, info.ObjectName)
		}
	case hasSuffix(".srvd.srvdsrv"):
		if err = typed(ObjectTypeSRVD, ".srvd.srvdsrv"); err == nil {
			err = checkSourceDefinition(filePath, ObjectTypeSRVD, info.ObjectName)
		}
	default:
		if group, member, ok := fugrMember(baseName); ok {
			// abapGit's own name for a function module:
			// {group}.fugr.{module}.abap. The group's other includes share
			// the pattern, so the content decides, and must agree.
			kind, name, err := detectTypeFromContent(filePath)
			if err != nil {
				return nil, err
			}
			if kind != ObjectTypeFunctionMod {
				return nil, fmt.Errorf("%s is part of function group %s but does not open with a FUNCTION statement: a function group's own includes cannot be deployed one file at a time (a function module file starts with FUNCTION)", baseName, group)
			}
			if name != member {
				return nil, fmt.Errorf("%s is named for function module %s but holds FUNCTION %s; rename the file or fix the statement", baseName, member, name)
			}
			info.ObjectType = ObjectTypeFunctionMod
			info.ObjectName = name
			info.ParentName = group
			break
		}
		if !hasSuffix(".abap") {
			return nil, fmt.Errorf("unsupported file extension: %s (expected .clas.abap, .clas.testclasses.abap, .clas.locals_def.abap, .clas.locals_imp.abap, .prog.abap, .incl.abap, .intf.abap, .fugr.abap, {group}.fugr.{module}.abap, .func.abap, .ddls.asddls, .bdef.asbdef, or .srvd.srvdsrv)", filepath.Ext(baseName))
		}
		// Generic .abap: typed from the first statement. This used to call
		// back into ParseABAPFile on the same path, which landed here again,
		// and the recursion killed the process (issue #237).
		stemName, err := genericStemName(baseName)
		if err != nil {
			return nil, err
		}
		kind, name, err := detectTypeFromContent(filePath)
		if err != nil {
			return nil, err
		}
		if kind == "" {
			// No REPORT, CLASS, INTERFACE or FUNCTION statement opens it, so
			// it can only be an include. Exports before issue #235 wrote
			// includes as {name}.abap, and those still read back as one.
			info.ObjectType = ObjectTypeInclude
			info.ObjectName = stemName
			break
		}
		if name != stemName {
			// A TOP include opens with its main program's PROGRAM, REPORT or
			// FUNCTION-POOL statement. Taking that for the object would
			// deploy the include over the program or group it belongs to.
			return nil, fmt.Errorf("%s opens with a statement for %s %s, not %s: it may be an include of %s; rename it to %s.incl.abap if it is the include, or to %s if it really is %s",
				baseName, kind, name, stemName, name, strings.ToLower(strings.ReplaceAll(stemName, "/", "#")), typedFileName(kind, name), name)
		}
		info.ObjectType = kind
		info.ObjectName = name
	}
	if err != nil {
		return nil, err
	}

	// The name is settled. What the content still gives is a description and,
	// for a class, which sections it has.
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNum := 0
	inComment := false

	for scanner.Scan() && lineNum < 200 { // Scan first 200 lines
		line := scanner.Text()
		lineNum++

		if info.ObjectType == ObjectTypeClass {
			if strings.Contains(strings.ToUpper(line), "DEFINITION") {
				info.HasDefinition = true
			}
			if strings.Contains(strings.ToUpper(line), "IMPLEMENTATION") {
				info.HasImplementation = true
			}
			if strings.Contains(strings.ToUpper(line), "FOR TESTING") {
				info.HasTestClasses = true
			}
		}

		// Parse description from header comments
		trimmed := strings.TrimSpace(line)
		if info.Description == "" {
			if strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "\"") {
				comment := strings.TrimPrefix(trimmed, "*")
				comment = strings.TrimPrefix(comment, "\"")
				comment = strings.TrimSpace(strings.TrimLeft(comment, "&"))
				comment = strings.TrimSpace(comment)

				// Skip common patterns, and the header template's own
				// "Report ZDEMO" line, which named one program "& Report ZDEMO".
				if comment != "" && !headerTitleLine.MatchString(comment) &&
					!strings.HasPrefix(comment, "-") &&
					!strings.HasPrefix(comment, "=") &&
					!strings.HasPrefix(comment, "*") &&
					!strings.Contains(strings.ToLower(comment), "author") &&
					!strings.Contains(strings.ToLower(comment), "date") &&
					len(comment) > 10 && len(comment) < 60 {
					info.Description = comment
					inComment = true
				}
			} else if inComment {
				inComment = false
			}
		}

		// Early exit if we have all required info
		if info.ObjectType != ObjectTypeClass && info.Description != "" {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	// Provide default description if none found
	if info.Description == "" {
		info.Description = fmt.Sprintf("Generated from %s", filepath.Base(filePath))
	}

	return info, nil
}

// nameFromFileStem turns a file stem into an object name: uppercased, with
// abapGit's # standing for the namespace slash.
func nameFromFileStem(stem string) string {
	return strings.ToUpper(strings.ReplaceAll(stem, "#", "/"))
}

// fugrMember splits abapGit's {group}.fugr.{member}.abap into the group and
// the member, both as object names. Any other name, including the group's own
// {group}.fugr.abap in whatever case, gives ok=false.
func fugrMember(baseName string) (group, member string, ok bool) {
	lower := strings.ToLower(baseName)
	const marker, suffix = ".fugr.", ".abap"
	idx := strings.Index(lower, marker)
	if idx <= 0 || !strings.HasSuffix(lower, suffix) {
		return "", "", false
	}
	start, end := idx+len(marker), len(lower)-len(suffix)
	// {group}.fugr.abap has its marker and suffix overlapping: start > end.
	if start >= end {
		return "", "", false
	}
	// Cut lower, not baseName: lowercasing can change byte lengths (Ⱥ→ⱥ), so
	// positions found in lower do not hold in baseName. Names are upper-cased
	// anyway.
	m := lower[start:end]
	if strings.Contains(m, ".") {
		return "", "", false
	}
	return nameFromFileStem(lower[:idx]), nameFromFileStem(m), true
}

// abapObjectName is a repository object name, optionally with a /NAMESPACE/.
var abapObjectName = regexp.MustCompile(`^(/[A-Z0-9_]+/)?[A-Z0-9_]+$`)

// genericStemName is the object name a plain {name}.abap file is named
// for. A stem that is not an object name (zfoo.bar.abap, say) is refused
// rather than guessed at.
func genericStemName(baseName string) (string, error) {
	stem := baseName[:len(baseName)-len(".abap")]
	name := nameFromFileStem(stem)
	if len(name) > 40 || !abapObjectName.MatchString(name) {
		return "", fmt.Errorf("cannot tell what %s is: %q is not an object name; name the file with its type (.prog.abap, .incl.abap, .clas.abap, .intf.abap, .fugr.abap, {group}.fugr.{module}.abap)", baseName, stem)
	}
	return name, nil
}

// typedFileName is the file name that states the type outright.
func typedFileName(kind CreatableObjectType, name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "/", "#")) + exportExtension(kind)
}

// statementKeywords are the statements that open an object of their own.
var statementKeywords = map[string]bool{
	"REPORT": true, "PROGRAM": true, "FUNCTION-POOL": true, "FUNCTION": true,
	"CLASS": true, "INTERFACE": true,
}

// detectTypeFromContent reads the first ABAP statement of a file and says
// what object it opens, and that object's name. It returns "" with no error
// when the statement opens none (an include), and an error when the file
// holds no statement or opens with something it cannot place.
//
// It only reads; it never parses the file as a whole. That is the caller's
// job, done once the type is known.
func detectTypeFromContent(filePath string) (CreatableObjectType, string, error) {
	stmt, err := firstABAPStatement(filePath)
	if err != nil {
		return "", "", err
	}
	base := filepath.Base(filePath)
	tokens := strings.Fields(strings.ToUpper(stmt))
	if len(tokens) == 0 {
		return "", "", fmt.Errorf("could not detect the object type of %s: it holds no ABAP statement", base)
	}
	keyword := strings.TrimSuffix(tokens[0], ":")
	if !statementKeywords[keyword] {
		return "", "", nil
	}
	if keyword != tokens[0] || (len(tokens) > 1 && strings.HasPrefix(tokens[1], ":")) {
		return "", "", fmt.Errorf("could not detect the object type of %s: it opens with a chained %s: statement, which declares local objects, not the one the file is named for; name the file with its type (.incl.abap for an include)", base, keyword)
	}
	if len(tokens) < 2 {
		return "", "", fmt.Errorf("could not detect the object type of %s: its %s statement names nothing", base, keyword)
	}
	name := tokens[1]
	switch keyword {
	case "REPORT", "PROGRAM":
		return ObjectTypeProgram, name, nil
	case "FUNCTION-POOL":
		return ObjectTypeFunctionGroup, name, nil
	case "FUNCTION":
		return ObjectTypeFunctionMod, name, nil
	}

	// A global class is declared CLASS <name> DEFINITION PUBLIC, and a global
	// interface INTERFACE <name> PUBLIC, with PUBLIC right there: anything
	// else (CREATE PUBLIC, FINAL ... PUBLIC, DEFERRED, LOAD) is a local
	// declaration or a forward one, which is what a program include opens
	// with. Deploying it as a global object would be wrong either way.
	global := false
	switch keyword {
	case "CLASS":
		global = len(tokens) >= 4 && tokens[2] == "DEFINITION" && tokens[3] == "PUBLIC"
	case "INTERFACE":
		global = len(tokens) >= 3 && tokens[2] == "PUBLIC"
	}
	for _, t := range tokens[2:] {
		if t == "DEFERRED" || t == "LOAD" {
			global = false
		}
	}
	if !global {
		suffix := map[string]string{"CLASS": "clas", "INTERFACE": "intf"}[keyword]
		want := "CLASS " + name + " DEFINITION PUBLIC"
		if keyword == "INTERFACE" {
			want = "INTERFACE " + name + " PUBLIC"
		}
		return "", "", fmt.Errorf("could not detect the object type of %s: it opens with %s, not %s, so it reads as a local or forward declaration from an include; rename it to .%s.abap if it is the global object, or .incl.abap if it is an include", base, strings.Join(tokens, " "), want, suffix)
	}
	if keyword == "CLASS" {
		return ObjectTypeClass, name, nil
	}
	return ObjectTypeInterface, name, nil
}

// firstABAPStatement returns the text of the first statement in the file, up
// to its period, with comments removed and lines joined. It reads at most the
// first 200 lines. A UTF-8 byte order mark, which Windows editors write, is
// not part of the statement.
func firstABAPStatement(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var stmt strings.Builder
	for lineNum := 0; lineNum < 200 && scanner.Scan(); lineNum++ {
		line := scanner.Text()
		if lineNum == 0 {
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		if strings.HasPrefix(line, "*") {
			continue // a full-line comment
		}
		if i := strings.Index(line, "\""); i >= 0 {
			line = line[:i] // an end-of-line comment
		}
		if i := strings.Index(line, "."); i >= 0 {
			stmt.WriteString(line[:i])
			return strings.TrimSpace(stmt.String()), nil
		}
		stmt.WriteString(line)
		stmt.WriteString(" ")
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading file: %w", err)
	}
	return strings.TrimSpace(stmt.String()), nil
}

// headerTitleLine is the line SE38's header template puts first: the
// object kind and its name, which is not a description.
var headerTitleLine = regexp.MustCompile(`(?i)^(report|include|program|class|interface|function\s+module|function\s+group)\s+\S+\s*$`)
