package adt

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The data preview (datapreview/freestyle) takes ABAP SQL, not ANSI SQL, and
// wraps it into a statement of its own that ends in INTO TABLE @DATA(...).
// What callers write first is often the ANSI spelling, and each of those
// fails with a message about the wrapper rather than about the query:
//
//   - t.field instead of t~field: the period ends the ABAP statement, so the
//     rest is a second statement ("Only one SELECT statement is allowed", or
//     "A Boolean expression was expected in "T"").
//   - ORDER BY x DESC instead of DESCENDING ("DESC" is not allowed here).
//   - a closing period or semicolon.
//
// normalizeOpenSQL rewrites exactly these, outside literals, and says what it
// changed. Anything else is left to SAP, whose message then comes back with a
// hint (see explainQueryError).

// maskLiterals returns s with the content of every '...' literal replaced by
// 'x', so that patterns can be matched on the statement alone and the
// positions still line up with s.
func maskLiterals(s string) string {
	b := []byte(s)
	in := false
	for i := range b {
		if b[i] == '\'' {
			in = !in
			continue
		}
		if in {
			b[i] = 'x'
		}
	}
	return string(b)
}

var (
	reSourceName  = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([A-Z0-9_/]+)(?:\s+AS\s+([A-Z0-9_]+))?`)
	reDotted      = regexp.MustCompile(`([A-Za-z0-9_/]+)\.([A-Za-z_][A-Za-z0-9_]*)`)
	reOrderBy     = regexp.MustCompile(`(?i)\bORDER\s+BY\b`)
	reDirection   = regexp.MustCompile(`(?i)\b(DESC|ASC)\b`)
	reTrailingEnd = regexp.MustCompile(`[\s.;]+$`)
)

// sqlSources names the tables and aliases a statement reads from, upper case.
func sqlSources(masked string) (tables []string, names map[string]bool) {
	names = map[string]bool{}
	for _, m := range reSourceName.FindAllStringSubmatch(masked, -1) {
		t := strings.ToUpper(m[1])
		tables = append(tables, t)
		names[t] = true
		if m[2] != "" && !isSQLKeyword(m[2]) {
			names[strings.ToUpper(m[2])] = true
		}
	}
	return tables, names
}

func isSQLKeyword(w string) bool {
	switch strings.ToUpper(w) {
	case "WHERE", "INNER", "LEFT", "RIGHT", "OUTER", "JOIN", "ON", "GROUP", "ORDER", "HAVING", "UNION", "CROSS":
		return true
	}
	return false
}

type sqlEdit struct {
	from, to int
	text     string
}

// normalizeOpenSQL rewrites the ANSI spellings the data preview rejects into
// ABAP SQL and returns the notes on what it changed.
func normalizeOpenSQL(q string) (string, []string) {
	masked := maskLiterals(q)
	var edits []sqlEdit
	var notes []string

	_, names := sqlSources(masked)
	dotted := map[string]bool{}
	for _, m := range reDotted.FindAllStringSubmatchIndex(masked, -1) {
		left := strings.ToUpper(masked[m[2]:m[3]])
		if !names[left] {
			continue
		}
		// A preceding character that belongs to a name means the match
		// started inside a longer token; leave that alone.
		if m[0] > 0 && strings.ContainsAny(masked[m[0]-1:m[0]], "~.") {
			continue
		}
		dot := m[3]
		edits = append(edits, sqlEdit{dot, dot + 1, "~"})
		dotted[q[m[2]:m[3]]] = true
	}
	if len(dotted) > 0 {
		ns := make([]string, 0, len(dotted))
		for n := range dotted {
			ns = append(ns, n)
		}
		sort.Strings(ns)
		notes = append(notes, fmt.Sprintf("ABAP SQL qualifies columns with ~, not a period: rewrote the qualifiers %s", strings.Join(ns, ", ")))
	}

	if loc := reOrderBy.FindStringIndex(masked); loc != nil {
		changed := false
		for _, m := range reDirection.FindAllStringIndex(masked[loc[1]:], -1) {
			from, to := loc[1]+m[0], loc[1]+m[1]
			// A direction follows a sort expression. Right after BY or a
			// comma the word is the expression itself -- a column or alias
			// that happens to be named DESC.
			if prev := strings.TrimRight(masked[loc[1]:from], " \t\r\n"); prev == "" || strings.HasSuffix(prev, ",") {
				continue
			}
			word := strings.ToUpper(masked[from:to])
			edits = append(edits, sqlEdit{from, to, map[string]string{"DESC": "DESCENDING", "ASC": "ASCENDING"}[word]})
			changed = true
		}
		if changed {
			notes = append(notes, "ABAP SQL spells the sort order DESCENDING/ASCENDING: rewrote DESC/ASC")
		}
	}

	if m := reTrailingEnd.FindStringIndex(masked); m != nil && strings.ContainsAny(masked[m[0]:], ".;") {
		edits = append(edits, sqlEdit{m[0], len(q), ""})
		notes = append(notes, "dropped the closing period/semicolon: the data preview adds its own INTO and period")
	}

	if len(edits) == 0 {
		return q, nil
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].from > edits[j].from })
	out := q
	for _, e := range edits {
		out = out[:e.from] + e.text + out[e.to:]
	}
	return out, notes
}

// QueryError is a query the data preview refused, with SAP's message on its
// own and what vsp could tell about it.
type QueryError struct {
	Message string   // SAP's message, without the XML around it
	Hints   []string // what to change, as far as vsp can tell
	Notes   []string // what vsp had already rewritten before sending
	Err     error
}

func (e *QueryError) Error() string {
	var b strings.Builder
	b.WriteString("SAP refused the query: ")
	b.WriteString(e.Message)
	for _, n := range e.Notes {
		b.WriteString("\nvsp rewrote: " + n)
	}
	for _, h := range e.Hints {
		b.WriteString("\nhint: " + h)
	}
	return b.String()
}

func (e *QueryError) Unwrap() error { return e.Err }

var (
	reExcMessage     = regexp.MustCompile(`<message[^>]*>([^<]*)</message>`)
	reUnknownColumn  = regexp.MustCompile(`Unknown column name "([^"]+)"`)
	reCannotFind     = regexp.MustCompile(`Cannot find \\?'([^'\\]+)\\?'`)
	reClientField    = regexp.MustCompile(`client field "([^"]+)" cannot be specified`)
	reLongField      = regexp.MustCompile(`(?i)(?:LRAW|LCHR) field ([A-Z0-9_/]+)\.?\s*$`)
	reInvalidLiteral = regexp.MustCompile(`is not a valid value for (\S+)`)
)

// sapQueryMessage pulls SAP's message out of the exception XML.
func sapQueryMessage(err error) (string, bool) {
	var api *APIError
	if !errors.As(err, &api) || api.StatusCode != 400 {
		return "", false
	}
	m := reExcMessage.FindStringSubmatch(api.Message)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(unescapeXML(m[1])), true
}

func unescapeXML(s string) string {
	return strings.NewReplacer("&quot;", `"`, "&apos;", "'", "&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(s)
}

// explainQueryError turns a refused query into a QueryError with hints. The
// lookups it makes (columns of the tables read, the DD02L row of a name the
// preview cannot find) are what a caller would do next by hand, one guess at
// a time.
func (c *Client) explainQueryError(ctx context.Context, sent string, notes []string, err error) error {
	msg, ok := sapQueryMessage(err)
	if !ok {
		return err
	}
	qe := &QueryError{Message: msg, Notes: notes, Err: err}
	tables, _ := sqlSources(maskLiterals(sent))

	switch {
	case reUnknownColumn.MatchString(msg):
		col := strings.ToUpper(reUnknownColumn.FindStringSubmatch(msg)[1])
		if i := strings.LastIndexAny(col, "~."); i >= 0 {
			col = col[i+1:]
		}
		for _, t := range uniqueStrings(tables, 3) {
			cols, cerr := c.tableColumns(ctx, t)
			if cerr != nil || len(cols) == 0 {
				continue
			}
			qe.Hints = append(qe.Hints, columnHint(t, col, cols))
		}
	case reCannotFind.MatchString(msg):
		name := strings.ToUpper(reCannotFind.FindStringSubmatch(msg)[1])
		qe.Hints = append(qe.Hints, c.missingSourceHint(ctx, name))
	case reClientField.MatchString(msg):
		qe.Hints = append(qe.Hints, "the data preview reads the logon client only; drop the condition on "+reClientField.FindStringSubmatch(msg)[1])
	case reLongField.MatchString(msg):
		qe.Hints = append(qe.Hints, "LRAW/LCHR fields such as "+reLongField.FindStringSubmatch(msg)[1]+" cannot be selected through the data preview; leave the field out (IDoc segment data: EDID4-SDATA)")
	case reInvalidLiteral.MatchString(msg):
		qe.Hints = append(qe.Hints, "the literal does not fit the column type "+reInvalidLiteral.FindStringSubmatch(msg)[1]+"; check its length (e.g. KUNNR is 10 characters with leading zeros)")
	case strings.Contains(msg, "longer than 255"):
		qe.Hints = append(qe.Hints, "a literal or name reached the data preview's 255-character source line; break long IN lists into shorter ones")
	}
	return qe
}

func uniqueStrings(in []string, max int) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] && len(out) < max {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// tableColumns reads a table's or view's column names from the data preview's
// metadata, which comes with every answer, even an empty one.
func (c *Client) tableColumns(ctx context.Context, table string) ([]string, error) {
	res, err := c.GetTableContents(ctx, table, 1, "")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, col := range res.Columns {
		out = append(out, col.Name)
	}
	return out, nil
}

// columnHint names the columns a table does have, the likely ones first. A
// table that has the column gets no list: in a join the refused name is then
// qualified with the wrong table or alias, not misspelled.
func columnHint(table, col string, cols []string) string {
	for _, c := range cols {
		if c == col {
			return fmt.Sprintf("%s does have column %s; check the table or alias it is qualified with", table, col)
		}
	}
	var close []string
	for _, c := range cols {
		if strings.Contains(c, col) || strings.Contains(col, c) && len(c) > 2 || editDistance(c, col) <= 2 {
			close = append(close, c)
		}
	}
	list := cols
	more := ""
	if len(list) > 80 {
		list, more = list[:80], fmt.Sprintf(" (+%d more)", len(cols)-80)
	}
	h := fmt.Sprintf("%s has no column %s", table, col)
	if len(close) > 0 {
		h += "; closest: " + strings.Join(close, ", ")
	}
	return h + "; its columns: " + strings.Join(list, ", ") + more
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			// Two-argument min: the integration tests declare their own.
			cur[j] = min(min(prev[j]+1, cur[j-1]+1), prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// missingSourceHint says what a name the data preview cannot find is: a
// structure, an append, a view of a kind it does not read, or nothing at all.
// The hint claims a name is missing only when both dictionary reads worked: a
// timeout or a missing authorization says nothing about the dictionary.
func (c *Client) missingSourceHint(ctx context.Context, name string) string {
	lit := strings.ReplaceAll(name, "'", "''")
	res, err := c.runQueryRaw(ctx, "SELECT tabname, tabclass FROM dd02l WHERE tabname = '"+lit+"' AND as4local = 'A'", 1)
	if err != nil {
		return fmt.Sprintf("whether %s exists could not be checked (reading DD02L failed: %v)", name, err)
	}
	if res != nil && len(res.Rows) > 0 {
		class := strings.TrimSpace(fmt.Sprint(res.Rows[0]["TABCLASS"]))
		switch class {
		case "INTTAB":
			return name + " is a structure: it has no rows to select"
		case "APPEND":
			return name + " is an append structure: select the table it extends"
		case "VIEW":
			return name + " is a view the data preview cannot read (maintenance or help view?); select its base tables"
		}
		return fmt.Sprintf("%s exists in DD02L with class %s, which the data preview does not read", name, class)
	}
	prefix := name
	if len(prefix) > 6 {
		prefix = prefix[:6]
	}
	like := strings.ReplaceAll(prefix, "'", "''")
	res, err = c.runQueryRaw(ctx, "SELECT tabname FROM dd02l WHERE tabname LIKE '"+like+"%' AND as4local = 'A' AND ( tabclass = 'TRANSP' OR tabclass = 'CLUSTER' OR tabclass = 'POOL' OR tabclass = 'VIEW' )", 15)
	if err != nil {
		return "no table or view " + name + " in the dictionary (looking for similar names failed: " + err.Error() + ")"
	}
	if res != nil && len(res.Rows) > 0 {
		var ns []string
		for _, r := range res.Rows {
			ns = append(ns, strings.TrimSpace(fmt.Sprint(r["TABNAME"])))
		}
		return "no table or view " + name + " in the dictionary; similar names: " + strings.Join(ns, ", ")
	}
	return "no table or view " + name + " in the dictionary (a CDS entity is addressed by its entity name)"
}
