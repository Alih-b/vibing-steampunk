package graph

import (
	"strings"

	"github.com/oisee/vibing-steampunk/pkg/abaplint"
)

// ExtractDepsFromSource extracts dependency edges from ABAP source code
// using the native Go lexer+parser. This works completely offline — no SAP needed.
//
// The parser recognizes statement types and extracts referenced object names:
//   - CALL FUNCTION 'FM_NAME'       → CALLS edge to FUGR
//   - SUBMIT PROG_NAME              → CALLS edge to PROG
//   - PERFORM sub IN PROGRAM prog   → CALLS edge to PROG
//   - CREATE OBJECT TYPE zcl        → REFERENCES edge to CLAS
//   - DATA x TYPE REF TO zcl        → REFERENCES edge to CLAS
//   - INCLUDE zincl                  → CONTAINS_INCLUDE edge
//   - SELECT ... FROM ztable        → REFERENCES edge to TABL
//   - lo->method( ) / zcl=>method() → CALLS edge (from Call statements)
func ExtractDepsFromSource(source string, sourceNodeID string) []*Edge {
	// Lex + parse
	lexer := &abaplint.Lexer{}
	tokens := lexer.Run(source)

	parser := &abaplint.StatementParser{}
	stmts := parser.Parse(tokens)

	matcher := abaplint.NewStatementMatcher()
	matcher.ClassifyStatements(stmts)

	var edges []*Edge

	for _, stmt := range stmts {
		if stmt.Type == "Comment" || stmt.Type == "Empty" || stmt.Type == "Unknown" {
			continue
		}

		extracted := extractFromStatement(stmt, sourceNodeID)
		// Stamped in one place rather than in fifteen extractors: every edge
		// from source knows where in the source it came from.
		if len(stmt.Tokens) > 0 {
			for _, e := range extracted {
				if e.Line == 0 {
					e.Line = stmt.Tokens[0].Row
				}
			}
		}
		edges = append(edges, extracted...)
	}

	return edges
}

// extractFromStatement extracts dependency edges from a single classified statement.
func extractFromStatement(stmt abaplint.Statement, sourceNodeID string) []*Edge {
	toks := stmt.Tokens
	if len(toks) < 2 {
		return nil
	}

	switch stmt.Type {
	case "CallFunction":
		return extractCallFunction(toks, sourceNodeID)
	case "Submit":
		return extractSubmit(toks, sourceNodeID)
	case "Perform":
		return extractPerform(toks, sourceNodeID)
	case "CreateObject":
		return extractCreateObject(toks, sourceNodeID)
	case "Include":
		return extractInclude(toks, sourceNodeID)
	case "Select":
		return extractSelect(toks, sourceNodeID)
	case "Data":
		return extractTypeRef(toks, sourceNodeID)
	case "Type":
		return extractTypeRef(toks, sourceNodeID)
	case "Constant":
		return extractTypeRef(toks, sourceNodeID)
	case "InterfaceDef":
		return extractInterfaceDef(toks, sourceNodeID)
	case "ClassDefinition":
		return extractClassDef(toks, sourceNodeID)
	case "Call":
		return extractMethodCall(toks, sourceNodeID)
	case "MethodDef":
		// A signature names types as well as exceptions: IMPORTING io TYPE REF
		// TO zcl_x is a dependency of the class whether or not any line uses it.
		return append(extractTypeRef(toks, sourceNodeID),
			extractExceptionList(toks, sourceNodeID, "RAISING", "")...)
	case "Move":
		// x = zcl_y=>method( ). Modern ABAP is written this way, and this
		// parser saw none of it: only a bare CALL was classified as a Call, so
		// a functional-style static call produced no edge at all. Measured
		// against the other parser in this repo on one real class, that was
		// three of nine dependencies missing — and the two parsers are reached
		// by two capabilities that are supposed to agree.
		// NEW zcl_x( ) instantiates, which is a dependency of the same weight
		// as calling it. Both shapes appear on the right of an assignment and
		// neither was seen.
		return append(extractStaticSelectors(toks, sourceNodeID),
			extractNewOperator(toks, sourceNodeID)...)
	case "Catch":
		// The exception classes a block handles are dependencies of it.
		return extractExceptionList(toks, sourceNodeID, "CATCH", "INTO")

	case "Raise":
		return extractRaise(toks, sourceNodeID)
	case "CallTransaction":
		return extractCallTransaction(toks, sourceNodeID)
	case "LeaveToTransaction":
		return extractLeaveToTransaction(toks, sourceNodeID)
	case "CallTransformation":
		return extractCallTransformation(toks, sourceNodeID)
	}
	return nil
}

// extractStaticSelectors finds every class or interface named on the left of
// the static component selector.
//
// The rule is syntactic and exact: `=>` selects a static component, and what
// stands to its left is a class or an interface name — never a variable, which
// takes `->`. So any token immediately before `=>` is an object this code
// depends on, wherever in the statement it appears, including the middle of a
// chained expression: zcl_a=>get( )->use( ) names zcl_a and nothing else here.
func extractStaticSelectors(toks []abaplint.Token, from string) []*Edge {
	var edges []*Edge
	seen := map[string]bool{}
	for i := 1; i < len(toks); i++ {
		if toks[i].Str != "=>" {
			continue
		}
		name := strings.ToUpper(strings.TrimSpace(toks[i-1].Str))
		if name == "" || !isIdentifier(name) || seen[name] {
			continue
		}
		seen[name] = true
		edges = append(edges, &Edge{
			From:      from,
			To:        NodeID(guessTypeFromName(name), name),
			Kind:      EdgeCalls,
			Source:    SourceParser,
			RefDetail: "STATIC:" + name,
		})
	}
	return edges
}

// extractNewOperator reads the class from NEW zcl_x( ). The token after NEW is
// the class unless it is an opening parenthesis, which is NEW #( ) — the type
// inferred from context, and inferring it here would be guessing.
func extractNewOperator(toks []abaplint.Token, from string) []*Edge {
	var edges []*Edge
	seen := map[string]bool{}
	for i := 0; i+1 < len(toks); i++ {
		if !strings.EqualFold(toks[i].Str, "NEW") {
			continue
		}
		name := strings.ToUpper(strings.TrimSpace(toks[i+1].Str))
		if name == "" || name == "#" || !isIdentifier(name) || seen[name] {
			continue
		}
		seen[name] = true
		edges = append(edges, &Edge{
			From:      from,
			To:        NodeID(guessTypeFromName(name), name),
			Kind:      EdgeCalls,
			Source:    SourceParser,
			RefDetail: "NEW:" + name,
		})
	}
	return edges
}

// extractExceptionList reads the class names between a keyword and a stopper —
// CATCH zcx_a zcx_b INTO lx, or RAISING zcx_a zcx_b to the end of a signature.
func extractExceptionList(toks []abaplint.Token, from, keyword, stop string) []*Edge {
	var edges []*Edge
	seen := map[string]bool{}
	collecting := false
	for _, t := range toks {
		up := strings.ToUpper(strings.TrimSpace(t.Str))
		switch {
		case up == keyword:
			collecting = true
			continue
		case !collecting:
			continue
		case stop != "" && up == stop, up == ".":
			collecting = false
			continue
		}
		if !isIdentifier(up) || seen[up] {
			continue
		}
		seen[up] = true
		edges = append(edges, &Edge{
			From:      from,
			To:        NodeID(guessTypeFromName(up), up),
			Kind:      EdgeReferences,
			Source:    SourceParser,
			RefDetail: keyword + ":" + up,
		})
	}
	return edges
}

// CALL FUNCTION 'FM_NAME' ...
func extractCallFunction(toks []abaplint.Token, from string) []*Edge {
	for i, t := range toks {
		if strings.EqualFold(t.Str, "FUNCTION") && i+1 < len(toks) {
			// Only a quoted literal names a function module here. A bare token
			// is a variable holding the name at runtime, and taking it at face
			// value invented a dependency on a module called after the variable
			// — CALL FUNCTION lv_fm_name became a static edge to FUGR
			// LV_FM_NAME, an object that does not exist. The dynamic extractor
			// already tests for the quote; this side did not, so one statement
			// produced both a real dynamic edge and an imaginary static one.
			raw := toks[i+1].Str
			if raw == "" || (raw[0] != '\'' && raw[0] != '`') {
				return nil
			}
			name := unquote(raw)
			if name != "" {
				return []*Edge{{
					From:      from,
					To:        NodeID("FUGR", fmToFugrName(name)),
					Kind:      EdgeCalls,
					Source:    SourceParser,
					RefDetail: "FM:" + name,
				}}
			}
		}
	}
	return nil
}

// SUBMIT prog_name ...
func extractSubmit(toks []abaplint.Token, from string) []*Edge {
	if len(toks) >= 2 {
		name := toks[1].Str
		if isIdentifier(name) && isCustomName(name) {
			return []*Edge{{
				From:      from,
				To:        NodeID("PROG", name),
				Kind:      EdgeCalls,
				Source:    SourceParser,
				RefDetail: "SUBMIT:" + name,
			}}
		}
	}
	return nil
}

// PERFORM sub IN PROGRAM prog
func extractPerform(toks []abaplint.Token, from string) []*Edge {
	for i, t := range toks {
		if strings.EqualFold(t.Str, "PROGRAM") && i+1 < len(toks) {
			name := toks[i+1].Str
			if isIdentifier(name) && isCustomName(name) {
				return []*Edge{{
					From:      from,
					To:        NodeID("PROG", name),
					Kind:      EdgeCalls,
					Source:    SourceParser,
					RefDetail: "PERFORM_IN:" + name,
				}}
			}
		}
	}
	return nil
}

// CREATE OBJECT lo TYPE zcl_foo
func extractCreateObject(toks []abaplint.Token, from string) []*Edge {
	for i, t := range toks {
		if strings.EqualFold(t.Str, "TYPE") && i+1 < len(toks) {
			name := toks[i+1].Str
			if isIdentifier(name) && isCustomName(name) {
				return []*Edge{{
					From:      from,
					To:        NodeID("CLAS", name),
					Kind:      EdgeReferences,
					Source:    SourceParser,
					RefDetail: "CREATE_OBJECT:" + name,
				}}
			}
		}
	}
	return nil
}

// INCLUDE zinclude_name
func extractInclude(toks []abaplint.Token, from string) []*Edge {
	if len(toks) >= 2 {
		name := toks[1].Str
		if isIdentifier(name) {
			return []*Edge{{
				From:      from,
				To:        NodeID("PROG", name),
				Kind:      EdgeContainsInclude,
				Source:    SourceParser,
				RefDetail: "INCLUDE:" + name,
			}}
		}
	}
	return nil
}

// SELECT ... FROM ztable ...
func extractSelect(toks []abaplint.Token, from string) []*Edge {
	for i, t := range toks {
		if strings.EqualFold(t.Str, "FROM") && i+1 < len(toks) {
			name := toks[i+1].Str
			if isIdentifier(name) && isCustomName(name) {
				return []*Edge{{
					From:      from,
					To:        NodeID("TABL", name),
					Kind:      EdgeReferences,
					Source:    SourceParser,
					RefDetail: "SELECT_FROM:" + name,
				}}
			}
		}
	}
	return nil
}

// DATA x TYPE REF TO zcl_foo  /  DATA x TYPE zcl_foo-component
func extractTypeRef(toks []abaplint.Token, from string) []*Edge {
	var edges []*Edge
	for i, t := range toks {
		if strings.EqualFold(t.Str, "TYPE") {
			// TYPE REF TO name
			if i+3 < len(toks) &&
				strings.EqualFold(toks[i+1].Str, "REF") &&
				strings.EqualFold(toks[i+2].Str, "TO") {
				name := toks[i+3].Str
				if isIdentifier(name) && isCustomName(name) {
					edges = append(edges, &Edge{
						From:      from,
						To:        NodeID("CLAS", name), // could be INTF too
						Kind:      EdgeReferences,
						Source:    SourceParser,
						RefDetail: "TYPE_REF_TO:" + name,
					})
				}
			} else if i+1 < len(toks) {
				// TYPE name or TYPE TABLE OF name
				idx := i + 1
				if idx < len(toks) && strings.EqualFold(toks[idx].Str, "TABLE") {
					idx++ // skip TABLE
					if idx < len(toks) && strings.EqualFold(toks[idx].Str, "OF") {
						idx++ // skip OF
					}
				}
				if idx < len(toks) {
					name := strings.Split(toks[idx].Str, "-")[0] // strip component
					if isIdentifier(name) && isCustomName(name) {
						edges = append(edges, &Edge{
							From:      from,
							To:        NodeID("TYPE", name),
							Kind:      EdgeReferences,
							Source:    SourceParser,
							RefDetail: "TYPE:" + name,
						})
					}
				}
			}
		}
	}
	return edges
}

// INTERFACES zif_foo
func extractInterfaceDef(toks []abaplint.Token, from string) []*Edge {
	if len(toks) >= 2 {
		name := toks[1].Str
		if isIdentifier(name) && isCustomName(name) {
			return []*Edge{{
				From:      from,
				To:        NodeID("INTF", name),
				Kind:      EdgeReferences,
				Source:    SourceParser,
				RefDetail: "IMPLEMENTS:" + name,
			}}
		}
	}
	return nil
}

// CLASS zcl_child DEFINITION ... INHERITING FROM zcl_parent
func extractClassDef(toks []abaplint.Token, from string) []*Edge {
	for i, t := range toks {
		if strings.EqualFold(t.Str, "INHERITING") && i+2 < len(toks) &&
			strings.EqualFold(toks[i+1].Str, "FROM") {
			name := toks[i+2].Str
			if isIdentifier(name) && isCustomName(name) {
				return []*Edge{{
					From:      from,
					To:        NodeID("CLAS", name),
					Kind:      EdgeReferences,
					Source:    SourceParser,
					RefDetail: "INHERITS:" + name,
				}}
			}
		}
	}
	return nil
}

// Method calls: lo->method( ), zcl_foo=>method( )
func extractMethodCall(toks []abaplint.Token, from string) []*Edge {
	var edges []*Edge
	for i, t := range toks {
		// Static call: ZCL_FOO=>METHOD
		if isStaticArrow(t) && i > 0 {
			name := toks[i-1].Str
			if isIdentifier(name) && isCustomName(name) {
				method := ""
				if i+1 < len(toks) {
					method = toks[i+1].Str
				}
				edges = append(edges, &Edge{
					From:      from,
					To:        NodeID("CLAS", name),
					Kind:      EdgeCalls,
					Source:    SourceParser,
					RefDetail: "STATIC_CALL:" + name + "=>" + method,
				})
			}
		}
	}
	return edges
}

// RAISE EXCEPTION TYPE zcx_error
func extractRaise(toks []abaplint.Token, from string) []*Edge {
	for i, t := range toks {
		if strings.EqualFold(t.Str, "TYPE") && i+1 < len(toks) {
			name := toks[i+1].Str
			if isIdentifier(name) && isCustomName(name) {
				return []*Edge{{
					From:      from,
					To:        NodeID("CLAS", name),
					Kind:      EdgeReferences,
					Source:    SourceParser,
					RefDetail: "RAISES:" + name,
				}}
			}
		}
	}
	return nil
}

// CALL TRANSACTION 'VA01' ...
func extractCallTransaction(toks []abaplint.Token, from string) []*Edge {
	for i, t := range toks {
		if strings.EqualFold(t.Str, "TRANSACTION") && i+1 < len(toks) {
			name := unquote(toks[i+1].Str)
			if name != "" {
				return []*Edge{{
					From:      from,
					To:        NodeID(NodeTRAN, name),
					Kind:      EdgeCalls,
					Source:    SourceParser,
					RefDetail: "CALL_TRANSACTION:" + name,
				}}
			}
		}
	}
	return nil
}

// LEAVE TO TRANSACTION 'SM30' ...
func extractLeaveToTransaction(toks []abaplint.Token, from string) []*Edge {
	for i, t := range toks {
		if strings.EqualFold(t.Str, "TRANSACTION") && i+1 < len(toks) {
			name := unquote(toks[i+1].Str)
			if name != "" {
				return []*Edge{{
					From:      from,
					To:        NodeID(NodeTRAN, name),
					Kind:      EdgeCalls,
					Source:    SourceParser,
					RefDetail: "LEAVE_TO_TRANSACTION:" + name,
				}}
			}
		}
	}
	return nil
}

// CALL TRANSFORMATION id SOURCE ... RESULT ...
func extractCallTransformation(toks []abaplint.Token, from string) []*Edge {
	for i, t := range toks {
		if strings.EqualFold(t.Str, "TRANSFORMATION") && i+1 < len(toks) {
			name := toks[i+1].Str
			// Can be a literal ('ID') or identifier
			cleaned := unquote(name)
			if cleaned != "" {
				name = cleaned
			}
			if isIdentifier(name) && !strings.EqualFold(name, "SOURCE") && !strings.EqualFold(name, "RESULT") {
				return []*Edge{{
					From:      from,
					To:        NodeID(NodeXSLT, name),
					Kind:      EdgeCalls,
					Source:    SourceParser,
					RefDetail: "CALL_TRANSFORMATION:" + name,
				}}
			}
		}
	}
	return nil
}

// --- Dynamic call detection ---
// Dynamic calls are invisible to CROSS/WBCROSSGT tables because the target
// is only known at runtime. The parser can detect the PATTERN and flag it.

// EdgeDynamic is a special edge kind for dynamic calls that cannot be
// statically resolved. These should be highlighted in boundary reports
// as potential hidden cross-package dependencies.
const EdgeDynamic EdgeKind = "DYNAMIC_CALL"

// extractDynamicCalls scans all tokens for dynamic call patterns.
// These are calls where the target is a variable, not a literal:
//   - CALL FUNCTION lv_variable       (vs CALL FUNCTION 'LITERAL')
//   - SUBMIT (lv_variable)            (vs SUBMIT ZPROG)
//   - PERFORM (lv_form) IN PROGRAM (lv_prog)
//   - CREATE OBJECT lo TYPE (lv_class)
//   - CALL METHOD (lv_variable)=>method
func ExtractDynamicCalls(source string, sourceNodeID string) []*Edge {
	lexer := &abaplint.Lexer{}
	tokens := lexer.Run(source)

	parser := &abaplint.StatementParser{}
	stmts := parser.Parse(tokens)

	matcher := abaplint.NewStatementMatcher()
	matcher.ClassifyStatements(stmts)

	var edges []*Edge

	for _, stmt := range stmts {
		toks := stmt.Tokens
		if len(toks) < 2 {
			continue
		}

		switch stmt.Type {
		case "CallFunction":
			// CALL FUNCTION <not-a-string-literal> → dynamic
			for i, t := range toks {
				if strings.EqualFold(t.Str, "FUNCTION") && i+1 < len(toks) {
					next := toks[i+1]
					if next.Str != "" && next.Str[0] != '\'' {
						edges = append(edges, &Edge{
							From:      sourceNodeID,
							To:        "DYNAMIC:" + next.Str,
							Kind:      EdgeDynamic,
							Source:    SourceParser,
							RefDetail: "DYNAMIC_FM:" + next.Str,
						})
					}
				}
			}
		case "Call":
			// CALL METHOD (var)=>meth, or lo->(var): the name is decided at
			// runtime. Classified but never extracted, so a dynamic method call
			// appeared in no answer at all — neither as a dependency nor as a
			// warning that one exists.
			for i := 0; i+2 < len(toks); i++ {
				if toks[i].Str != "(" || toks[i+2].Str != ")" {
					continue
				}
				prev := ""
				if i > 0 {
					prev = strings.ToUpper(toks[i-1].Str)
				}
				// Only where a name belongs. Any other parenthesis in a CALL is
				// an argument list, and reporting those would bury the real ones.
				if prev != "METHOD" && prev != "->" && prev != "=>" {
					continue
				}
				varName := toks[i+1].Str
				if varName == "" || !isIdentifier(varName) {
					continue
				}
				edges = append(edges, &Edge{
					From:      sourceNodeID,
					To:        "DYNAMIC:" + varName,
					Kind:      EdgeDynamic,
					Source:    SourceParser,
					RefDetail: "DYNAMIC_METHOD:" + varName,
				})
			}
		case "Submit":
			// SUBMIT (variable) → dynamic
			if len(toks) >= 2 && toks[1].Str == "(" {
				varName := ""
				if len(toks) >= 3 {
					varName = toks[2].Str
				}
				edges = append(edges, &Edge{
					From:      sourceNodeID,
					To:        "DYNAMIC:" + varName,
					Kind:      EdgeDynamic,
					Source:    SourceParser,
					RefDetail: "DYNAMIC_SUBMIT:" + varName,
				})
			}
		case "Perform":
			// PERFORM sub IN PROGRAM (variable) → dynamic
			for i, t := range toks {
				if strings.EqualFold(t.Str, "PROGRAM") && i+1 < len(toks) {
					if toks[i+1].Str == "(" {
						varName := ""
						if i+2 < len(toks) {
							varName = toks[i+2].Str
						}
						edges = append(edges, &Edge{
							From:      sourceNodeID,
							To:        "DYNAMIC:" + varName,
							Kind:      EdgeDynamic,
							Source:    SourceParser,
							RefDetail: "DYNAMIC_PERFORM:" + varName,
						})
					}
				}
			}
		case "CreateObject":
			// CREATE OBJECT lo TYPE (variable)
			for i, t := range toks {
				if strings.EqualFold(t.Str, "TYPE") && i+1 < len(toks) {
					if toks[i+1].Str == "(" {
						varName := ""
						if i+2 < len(toks) {
							varName = toks[i+2].Str
						}
						edges = append(edges, &Edge{
							From:      sourceNodeID,
							To:        "DYNAMIC:" + varName,
							Kind:      EdgeDynamic,
							Source:    SourceParser,
							RefDetail: "DYNAMIC_CREATE:" + varName,
						})
					}
				}
			}
		case "CallTransaction":
			// CALL TRANSACTION lv_variable (not a literal)
			for i, t := range toks {
				if strings.EqualFold(t.Str, "TRANSACTION") && i+1 < len(toks) {
					next := toks[i+1]
					if next.Str != "" && next.Str[0] != '\'' {
						edges = append(edges, &Edge{
							From:      sourceNodeID,
							To:        "DYNAMIC:" + next.Str,
							Kind:      EdgeDynamic,
							Source:    SourceParser,
							RefDetail: "DYNAMIC_TRANSACTION:" + next.Str,
						})
					}
				}
			}
		case "CallTransformation":
			// CALL TRANSFORMATION (lv_variable) or CALL TRANSFORMATION lv_var
			for i, t := range toks {
				if strings.EqualFold(t.Str, "TRANSFORMATION") && i+1 < len(toks) {
					next := toks[i+1]
					if next.Str == "(" {
						varName := ""
						if i+2 < len(toks) {
							varName = toks[i+2].Str
						}
						edges = append(edges, &Edge{
							From:      sourceNodeID,
							To:        "DYNAMIC:" + varName,
							Kind:      EdgeDynamic,
							Source:    SourceParser,
							RefDetail: "DYNAMIC_TRANSFORMATION:" + varName,
						})
					}
				}
			}
		}
	}

	return edges
}

// --- helpers ---

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '/') {
			return false
		}
	}
	return true
}

func isCustomName(name string) bool {
	upper := strings.ToUpper(name)
	return len(upper) > 0 && (upper[0] == 'Z' || upper[0] == 'Y')
}

func isStaticArrow(t abaplint.Token) bool {
	return t.Str == "=>" || strings.Contains(strings.ToLower(t.Type.String()), "static")
}

// fmToFugrName extracts the function group name from a function module name.
// Convention: Z_FUGR_FM_NAME → hard to reverse reliably, so we keep FM name.
func fmToFugrName(fmName string) string {
	return fmName // Keep as-is; TADIR resolution will fix it
}

// AddSourceDeps parses source, adds every static and dynamic dependency it
// finds as an edge from the node `from`, and adds each edge's target as a node
// with the type and name its ID carries and no package. The node `from` itself
// is the caller's to add, with whatever it knows about it.
//
// Eleven scans across cmd/vsp and internal/mcp did this by hand, and they did
// not agree on a target ID with no "type:" part: half skipped it, half would
// have panicked on it. The extractors never produce one — every target is a
// NodeID or "DYNAMIC:<name>" — so skipping it is what every copy actually did.
func (g *Graph) AddSourceDeps(from, source string) {
	edges := ExtractDepsFromSource(source, from)
	for _, e := range append(edges, ExtractDynamicCalls(source, from)...) {
		g.AddEdge(e)
		if typ, name, ok := strings.Cut(e.To, ":"); ok {
			g.AddNode(&Node{ID: e.To, Name: name, Type: typ})
		}
	}
}
