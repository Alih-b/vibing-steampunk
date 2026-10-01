package abaplint

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// abapSeeds rebuilds the ABAP sources behind testdata/oracle_fixtures.json by
// placing every token back at its row and column. The oracle stores tokens,
// not files, but laid out again they are the real programs the lexer is
// checked against, which makes them the best starting corpus there is.
func abapSeeds(f *testing.F) []string {
	f.Helper()
	raw, err := os.ReadFile("testdata/oracle_fixtures.json")
	if err != nil {
		f.Fatal(err)
	}
	var files []oracleFile
	if err := json.Unmarshal(raw, &files); err != nil {
		f.Fatal(err)
	}
	seeds := make([]string, 0, 12+len(files))
	seeds = append(seeds,
		"",
		"DATA lv_x TYPE i.",
		"DATA: lv_x TYPE i, lv_y TYPE string.",
		"WRITE: / 'a', |b{ c }d|, `e`.",
		"* comment\n\" another\nlv_x = 1. \"trailing",
		"METHOD m BY DATABASE PROCEDURE FOR HDB LANGUAGE SQLSCRIPT.\nselect 1 from dummy; ENDMETHOD.",
		"lo->m( )->n( ). zcl=>s( ). a-b+c @d ##PRAGMA.",
		"'unterminated", "|unterminated{ ", "`", ":", ":::,,..",
	)
	for _, file := range files {
		var b strings.Builder
		row, col := 1, 1
		for _, tok := range file.Tokens {
			for row < tok.Row {
				b.WriteByte('\n')
				row++
				col = 1
			}
			for col < tok.Col {
				b.WriteByte(' ')
				col++
			}
			b.WriteString(tok.Str)
			col += len(tok.Str)
		}
		seeds = append(seeds, b.String())
	}
	return seeds
}

// FuzzLexer: malformed ABAP must never panic the lexer, and what it returns
// must be tokens: non-empty, already trimmed, the same on a second run.
func FuzzLexer(f *testing.F) {
	for _, s := range abapSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		tokens := (&Lexer{}).Run(src)
		total := 0
		for i, tok := range tokens {
			if tok.Str == "" || strings.TrimSpace(tok.Str) != tok.Str {
				t.Fatalf("token %d is %q: empty or untrimmed", i, tok.Str)
			}
			total += len(tok.Str)
		}
		if total > len(src) {
			t.Fatalf("tokens hold %d bytes, more than the %d of the source", total, len(src))
		}
		again := (&Lexer{}).Run(src)
		if len(again) != len(tokens) {
			t.Fatalf("second run gave %d tokens, first %d", len(again), len(tokens))
		}
		for i := range tokens {
			if again[i] != tokens[i] {
				t.Fatalf("second run differs at token %d: %+v vs %+v", i, again[i], tokens[i])
			}
		}
	})
}

type tokenKey struct {
	row, col int
	str      string
}

// FuzzStatementParser: splitting and classifying statements must not panic,
// and must not lose or invent tokens. Every token the lexer produced ends up
// in a statement (as a token or a pragma) except the chaining colons the
// parser consumes; nothing appears that the lexer did not produce. Chaining
// repeats the prefix, so this is a set comparison, not a count.
func FuzzStatementParser(f *testing.F) {
	for _, s := range abapSeeds(f) {
		f.Add(s)
	}
	matcher := NewStatementMatcher()
	f.Fuzz(func(t *testing.T, src string) {
		tokens := (&Lexer{}).Run(src)
		stmts := (&StatementParser{}).Parse(tokens)
		matcher.ClassifyStatements(stmts)

		in := map[tokenKey]bool{}
		for _, tok := range tokens {
			if tok.Type != TokenComment && tok.Str == ":" {
				continue
			}
			in[tokenKey{tok.Row, tok.Col, tok.Str}] = true
		}
		trailing := trailingChainPrefix(tokens)
		out := map[tokenKey]bool{}
		for i, s := range stmts {
			if s.Type == "" {
				t.Fatalf("statement %d has no type", i)
			}
			_ = s.ConcatTokens()
			_ = s.FirstTokenStr()
			for _, tok := range append(append([]Token{}, s.Tokens...), s.Pragmas...) {
				k := tokenKey{tok.Row, tok.Col, tok.Str}
				if !in[k] {
					t.Fatalf("statement %d holds %+v, which the lexer did not produce", i, tok)
				}
				out[k] = true
			}
		}
		for k := range in {
			if !out[k] && !trailing[k] {
				t.Fatalf("token %+v was dropped", k)
			}
		}
	})
}

// trailingChainPrefix returns the one set of tokens the parser may drop, the
// way abaplint's own StatementParser does: a chain prefix with nothing after
// it at the end of the source ("DATA:" as the last statement) is never
// emitted. Those are the tokens after the last "." up to the first colon
// after it, and only when nothing but colons follows that colon.
func trailingChainPrefix(tokens []Token) map[tokenKey]bool {
	var code []Token
	for _, tok := range tokens {
		if tok.Type != TokenComment {
			code = append(code, tok)
		}
	}
	start := 0
	for i, tok := range code {
		if tok.Str == "." {
			start = i + 1
		}
	}
	colon := -1
	for i := start; i < len(code); i++ {
		if code[i].Str == ":" {
			colon = i
			break
		}
	}
	if colon < 0 {
		return nil
	}
	for _, tok := range code[colon:] {
		if tok.Str != ":" {
			return nil
		}
	}
	prefix := map[tokenKey]bool{}
	for _, tok := range code[start:colon] {
		prefix[tokenKey{tok.Row, tok.Col, tok.Str}] = true
	}
	return prefix
}

// FuzzLinter runs every rule over arbitrary source: a lint pass over a file
// the user is editing must not take the server down.
func FuzzLinter(f *testing.F) {
	for _, s := range abapSeeds(f) {
		f.Add(s)
	}
	linter := NewLinter()
	f.Fuzz(func(t *testing.T, src string) {
		for _, is := range linter.Run("fuzz.prog.abap", src) {
			if is.Key == "" {
				t.Fatalf("issue without a rule key: %+v", is)
			}
		}
	})
}
