package adt

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A typed file ({name}.prog.abap, {name}.clas.abap, ...) is named by its file
// name. The checks here read its content only to refuse a file whose main
// statement names a different object: deploying it under either name would
// put one object's source into another.

// typedStemName is the object a typed file is named for: its file name
// without the type suffix, uppercased, with # for the namespace slash.
// The suffix is matched case-blind by the caller, so it is cut from the
// lowered name (names are uppercased anyway, and lowering can change byte
// lengths before the suffix).
func typedStemName(baseName, suffix string) (string, error) {
	lower := strings.ToLower(baseName)
	stem := lower[:len(lower)-len(suffix)]
	name := nameFromFileStem(stem)
	if len(name) > 40 || !abapObjectName.MatchString(name) {
		return "", fmt.Errorf("cannot tell which object %s is: %q is not an object name (letters, digits and _, at most 40 characters, with a namespace written as #ns#)", baseName, baseName[:len(baseName)-len(suffix)])
	}
	return name, nil
}

// funcFileModuleName is the function module a .func.abap file is named for:
// {group}.fugr.{module}.func.abap or {module}.func.abap.
func funcFileModuleName(baseName string) (string, error) {
	lower := strings.ToLower(baseName)
	stem := lower[:len(lower)-len(".func.abap")]
	if i := strings.Index(stem, ".fugr."); i > 0 {
		stem = stem[i+len(".fugr."):]
	}
	name := nameFromFileStem(stem)
	if len(name) > 30 || !abapObjectName.MatchString(name) {
		return "", fmt.Errorf("cannot tell which function module %s is: %q is not a function module name", baseName, stem)
	}
	return name, nil
}

var (
	progdirBlock = regexp.MustCompile(`(?s)<PROGDIR>.*?</PROGDIR>`)
	progdirSubc  = regexp.MustCompile(`<SUBC>\s*([^<]*?)\s*</SUBC>`)
	progdirName  = regexp.MustCompile(`<NAME>\s*([^<]*?)\s*</NAME>`)
)

// progXMLSubc reads the program type abapGit records for a .prog.abap file in
// the {name}.prog.xml beside it (PROGDIR-SUBC: 1 executable, I include, M
// module pool, S subroutine pool, ...). found is false when there is no such
// file. A .prog.xml that names another program is an error: the pair does not
// belong together, and neither can be trusted to say what the source is.
func progXMLSubc(filePath, name string) (subc string, found bool, err error) {
	dir := filepath.Dir(filePath)
	base := filepath.Base(filePath)
	stem := base[:len(base)-len(".prog.abap")]
	var data []byte
	var xmlName string
	for _, cand := range []string{stem + ".prog.xml", strings.ToLower(stem) + ".prog.xml", stem + ".PROG.XML"} {
		data, err = os.ReadFile(filepath.Join(dir, cand))
		if err == nil {
			xmlName = cand
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", false, fmt.Errorf("reading %s: %w", cand, err)
		}
	}
	if xmlName == "" {
		return "", false, nil
	}
	block := progdirBlock.Find(data)
	if block == nil {
		return "", true, nil
	}
	if m := progdirName.FindSubmatch(block); m != nil {
		if xn := strings.ToUpper(string(m[1])); xn != "" && xn != name {
			return "", true, fmt.Errorf("%s is for program %s, but %s is named for %s; refusing to guess which is right", xmlName, xn, base, name)
		}
	}
	if m := progdirSubc.FindSubmatch(block); m != nil {
		subc = strings.ToUpper(string(m[1]))
	}
	return subc, true, nil
}

// checkProgramStatement checks that a .prog.abap opens with REPORT or PROGRAM
// naming the program its file is named for. hasXML says a .prog.xml was
// found that does not mark the file as an include.
func checkProgramStatement(filePath, name string, hasXML bool) error {
	base := filepath.Base(filePath)
	stmts, err := abapStatements(filePath, 1)
	if err != nil {
		return err
	}
	inclName := strings.ToLower(strings.ReplaceAll(name, "/", "#")) + ".incl.abap"
	includeHint := fmt.Sprintf("if it is an include, keep its .prog.xml with <SUBC>I</SUBC> beside it (as abapGit exports it) or rename it to %s", inclName)
	if hasXML {
		includeHint = fmt.Sprintf("its .prog.xml does not mark it as an include (SUBC I); if it is one, fix the .prog.xml or rename the file to %s", inclName)
	}
	if len(stmts) == 0 {
		return fmt.Errorf("%s holds no ABAP statement", base)
	}
	tokens := strings.Fields(strings.ToUpper(stmts[0]))
	kw := strings.TrimSuffix(tokens[0], ":")
	if kw != "REPORT" && kw != "PROGRAM" {
		return fmt.Errorf("%s does not open with REPORT or PROGRAM %s, so it is not a program; %s", base, name, includeHint)
	}
	if len(tokens) < 2 {
		return nil // REPORT. with no name: nothing contradicts the file name
	}
	if got := strings.TrimPrefix(tokens[1], ":"); got != name {
		return fmt.Errorf("%s is named for program %s but opens with %s %s; it was not deployed under either name. A TOP include opens with its main program's statement: %s; if it really is a program, fix the statement or the file name", base, name, kw, got, includeHint)
	}
	return nil
}

// checkGlobalDeclaration checks the main declaration of a .clas.abap or
// .intf.abap. The main one is the first CLASS <n> DEFINITION PUBLIC (or
// INTERFACE <n> PUBLIC); forward declarations (DEFERRED, LOAD) and local
// declarations before it are passed over. A public declaration of another
// name is refused. With no public declaration at all, a non-forward
// declaration of the file's own name is accepted; anything else is refused.
func checkGlobalDeclaration(filePath, keyword, name string) error {
	base := filepath.Base(filePath)
	stmts, err := abapStatements(filePath, 0)
	if err != nil {
		return err
	}
	ownName := false
	for _, s := range stmts {
		for _, st := range expandChain(s, keyword) {
			tokens := strings.Fields(strings.ToUpper(st))
			if len(tokens) < 2 || tokens[0] != keyword {
				continue
			}
			got, rest := tokens[1], tokens[2:]
			if keyword == "CLASS" {
				if len(rest) == 0 || rest[0] != "DEFINITION" {
					continue // CLASS ... IMPLEMENTATION
				}
				rest = rest[1:]
				if len(rest) >= 2 && rest[0] == "LOCAL" && rest[1] == "FRIENDS" {
					continue // grants friendship; declares nothing
				}
			}
			if containsToken(rest, "DEFERRED") || containsToken(rest, "LOAD") {
				continue
			}
			if len(rest) > 0 && rest[0] == "PUBLIC" {
				if got != name {
					return fmt.Errorf("%s is named for %s %s but declares %s %s PUBLIC; it was not deployed under either name: rename the file or fix the declaration", base, strings.ToLower(keyword), name, keyword, got)
				}
				return nil
			}
			if got == name {
				ownName = true
			}
		}
	}
	if ownName {
		return nil
	}
	want := "CLASS <name> DEFINITION PUBLIC"
	if keyword == "INTERFACE" {
		want = "INTERFACE <name> PUBLIC"
	}
	return fmt.Errorf("%s does not declare %s: expected %s, with <name> the one in the file name", base, name, want)
}

// checkFunctionPoolStatement checks a .fugr.abap. A function group's main
// program usually holds only INCLUDE statements (FUNCTION-POOL is in its TOP
// include), so it may open with anything but a statement for another object.
func checkFunctionPoolStatement(filePath, name string) error {
	base := filepath.Base(filePath)
	stmts, err := abapStatements(filePath, 1)
	if err != nil {
		return err
	}
	if len(stmts) == 0 {
		return fmt.Errorf("%s holds no ABAP statement", base)
	}
	tokens := strings.Fields(strings.ToUpper(stmts[0]))
	kw := strings.TrimSuffix(tokens[0], ":")
	if !statementKeywords[kw] {
		return nil
	}
	if kw != "FUNCTION-POOL" {
		return fmt.Errorf("%s is named for function group %s but opens with %s: it is not that group's source", base, name, strings.Join(tokens, " "))
	}
	if len(tokens) >= 2 && tokens[1] != name {
		return fmt.Errorf("%s is named for function group %s but opens with FUNCTION-POOL %s; it was not deployed under either name: rename the file or fix the statement", base, name, tokens[1])
	}
	return nil
}

// checkFunctionStatement checks that a .func.abap opens with FUNCTION naming
// the module its file is named for.
func checkFunctionStatement(filePath, name string) error {
	base := filepath.Base(filePath)
	stmts, err := abapStatements(filePath, 1)
	if err != nil {
		return err
	}
	if len(stmts) == 0 {
		return fmt.Errorf("%s holds no ABAP statement", base)
	}
	tokens := strings.Fields(strings.ToUpper(stmts[0]))
	if tokens[0] != "FUNCTION" || len(tokens) < 2 {
		return fmt.Errorf("%s does not open with FUNCTION %s", base, strings.ToLower(name))
	}
	if tokens[1] != name {
		return fmt.Errorf("%s is named for function module %s but holds FUNCTION %s; it was not deployed under either name: rename the file or fix the statement", base, name, tokens[1])
	}
	return nil
}

var (
	ddlsDefine = regexp.MustCompile(`(?i)(?:^|[\s;])define\s+(?:(?:root|view|entity|table|function|abstract|custom|hierarchy|transient|external)\s+)+([a-z0-9_/]+)`)
	bdefDefine = regexp.MustCompile(`(?i)(?:^|[\s;])define\s+behavior\s+for\s+([a-z0-9_/]+)`)
	srvdDefine = regexp.MustCompile(`(?i)(?:^|[\s;])define\s+service\s+([a-z0-9_/]+)`)
	// An extension is named apart from what it extends, so its statement
	// says nothing about the file's own name.
	ddlsExtend = regexp.MustCompile(`(?i)(?:^|[\s;])extend\s+view\b`)
	bdefExtend = regexp.MustCompile(`(?i)(?:^|[\s;])(?:extension\b|extend\s+behavior\b)`)
	srvdExtend = regexp.MustCompile(`(?i)(?:^|[\s;])extend\s+service\b`)
	sqlLiteral = regexp.MustCompile(`'[^']*'`)
)

// checkSourceDefinition checks a CDS, behavior or service definition source:
// its define statement must name the object the file is named for. A
// behavior definition is named for its root entity, the first one it
// defines behavior for.
func checkSourceDefinition(filePath string, kind CreatableObjectType, name string) error {
	base := filepath.Base(filePath)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}
	var define, extend *regexp.Regexp
	var want string
	switch kind {
	case ObjectTypeDDLS:
		define, extend, want = ddlsDefine, ddlsExtend, "define view [entity] "+name
	case ObjectTypeBDEF:
		define, extend, want = bdefDefine, bdefExtend, "define behavior for "+name
	case ObjectTypeSRVD:
		define, extend, want = srvdDefine, srvdExtend, "define service "+name
	default:
		return nil
	}
	// The statement may span lines (define root view entity<newline>ZI_X),
	// so the source is read as one text: comments, literals and annotation
	// lines out, the rest joined.
	inBlock := false
	var text strings.Builder
	for _, line := range strings.Split(strings.TrimPrefix(string(data), "\uFEFF"), "\n") {
		line = stripDDLComments(line, &inBlock)
		line = sqlLiteral.ReplaceAllString(line, "''")
		if strings.HasPrefix(strings.TrimSpace(line), "@") {
			continue
		}
		text.WriteString(line)
		text.WriteString("\n")
	}
	src := text.String()
	d := define.FindStringSubmatchIndex(src)
	e := extend.FindStringIndex(src)
	if e != nil && (d == nil || e[0] < d[0]) {
		return nil
	}
	if d == nil {
		return fmt.Errorf("could not find the definition in %s: expected %s", base, want)
	}
	got := strings.ToUpper(src[d[2]:d[3]])
	if ddlKeywords[got] {
		// define root view entity with nothing after it: no name to compare.
		return fmt.Errorf("could not find the name in the definition of %s: expected %s", base, want)
	}
	if got != name {
		return fmt.Errorf("%s is named for %s %s but its definition names %s; it was not deployed under either name: rename the file or fix the definition", base, kind, name, got)
	}
	return nil
}

// ddlKeywords are the words between define and the name in a CDS definition.
var ddlKeywords = map[string]bool{
	"ROOT": true, "VIEW": true, "ENTITY": true, "TABLE": true, "FUNCTION": true, "ABSTRACT": true,
	"CUSTOM": true, "HIERARCHY": true, "TRANSIENT": true, "EXTERNAL": true, "BEHAVIOR": true, "FOR": true, "SERVICE": true,
}

// stripDDLComments removes // and -- line comments and /* */ block comments
// from one line; inBlock carries an open block comment to the next line.
func stripDDLComments(line string, inBlock *bool) string {
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		if *inBlock {
			if strings.HasPrefix(line[i:], "*/") {
				*inBlock = false
				i++
			}
			continue
		}
		if strings.HasPrefix(line[i:], "/*") {
			*inBlock = true
			i++
			continue
		}
		if strings.HasPrefix(line[i:], "//") || strings.HasPrefix(line[i:], "--") {
			break
		}
		b.WriteByte(line[i])
	}
	return b.String()
}

// abapStatements returns the statements of an ABAP source, each without its
// period, with comments removed and its lines joined. max > 0 stops after
// that many. Periods and quotes inside literals ('...', `...`, |...|) do not
// end a statement or start a comment. A UTF-8 byte order mark is dropped.
func abapStatements(filePath string, max int) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	src := strings.TrimPrefix(string(data), "\uFEFF")
	var stmts []string
	var cur strings.Builder
	flush := func() bool {
		if s := strings.TrimSpace(cur.String()); s != "" {
			stmts = append(stmts, s)
		}
		cur.Reset()
		return max > 0 && len(stmts) >= max
	}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "*") {
			continue // a full-line comment
		}
		var quote byte // the literal we are in, if any
		for i := 0; i < len(line); i++ {
			c := line[i]
			if quote != 0 {
				cur.WriteByte(c)
				if c == quote {
					quote = 0
				}
				continue
			}
			switch c {
			case '\'', '`', '|':
				quote = c
				cur.WriteByte(c)
			case '"':
				i = len(line) // an end-of-line comment
			case '.':
				if flush() {
					return stmts, nil
				}
			default:
				cur.WriteByte(c)
			}
		}
		cur.WriteByte(' ')
	}
	flush()
	return stmts, nil
}

// expandChain splits a chained statement (CLASS: a DEFINITION DEFERRED,
// b DEFINITION PUBLIC) into one statement per link, when it is a chain of
// keyword; any other statement comes back as it is.
func expandChain(stmt, keyword string) []string {
	i := strings.Index(stmt, ":")
	if i < 0 || !strings.EqualFold(strings.TrimSpace(stmt[:i]), keyword) {
		return []string{stmt}
	}
	var out []string
	for _, link := range strings.Split(stmt[i+1:], ",") {
		out = append(out, stmt[:i]+" "+strings.TrimSpace(link))
	}
	return out
}

func containsToken(tokens []string, t string) bool {
	for _, x := range tokens {
		if x == t {
			return true
		}
	}
	return false
}
