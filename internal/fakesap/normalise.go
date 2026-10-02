package fakesap

import (
	"regexp"
	"sort"
	"strings"
)

var (
	progressRun = regexp.MustCompile(`\r[^\r\n]*`)
	ageDays     = regexp.MustCompile(`("age_days":\s*)\d+|(age_days=)\d+|\(\d+ days?\)|\d+ days ago|(Age \(days\)\s*\|\s*)\d+`)
	digits      = regexp.MustCompile(`\d+`)
	refsLine    = regexp.MustCompile(`^\s+\S+ — \d+ refs$`)
	serverURL   = regexp.MustCompile(`http://127\.0\.0\.1:\d+`)
	arrayOpen   = regexp.MustCompile(`^(\s*)"(entries|unsearched)": \[$`)
	escapedList = regexp.MustCompile(`(not a complete answer:)((?:\\n  [^\\"]*)+)`)
)

// Normalise removes from an output what changes between runs and is not part
// of the answer:
//
//   - the progress counter concurrent reads write to stderr, in any order;
//   - the age of a fixed date, which grows by one every day;
//   - the fake's port;
//   - the order of boundary crossings. graph.AnalyzeCrossings walks a map, so
//     its entries come out in a different order on every run — a property of
//     the code under test that predates these goldens, and one that would make
//     them flap. Crossing lines (they carry "→"), Markdown crossing rows and
//     JSON "entries" arrays are sorted in place, and so are the "Packages
//     crossed" lines of a boundary report, which come from a map too;
//   - the order of what could not be searched. The MCP side lists failed
//     lookups in the order the graph's map handed them out, so the JSON
//     "unsearched" arrays and the item lines of an UnsearchedNote — as lines
//     or inside an escaped JSON string — are sorted too.
//
// Nothing else is reordered.
func Normalise(s string) string {
	s = progressRun.ReplaceAllString(s, "")
	s = ageDays.ReplaceAllStringFunc(s, func(m string) string {
		return digits.ReplaceAllString(m, "N")
	})
	s = serverURL.ReplaceAllString(s, "http://fake")
	s = escapedList.ReplaceAllStringFunc(s, func(m string) string {
		sub := escapedList.FindStringSubmatch(m)
		items := strings.Split(strings.TrimPrefix(sub[2], `\n  `), `\n  `)
		sort.Strings(items)
		return sub[1] + `\n  ` + strings.Join(items, `\n  `)
	})
	lines := strings.Split(s, "\n")
	lines = sortJSONEntries(lines)
	lines = sortNoteItems(lines)
	lines = sortRuns(lines, func(l string) bool { return strings.Contains(l, "→") })
	lines = sortRuns(lines, func(l string) bool { return strings.HasPrefix(l, "| $") })
	lines = sortRuns(lines, refsLine.MatchString)
	return strings.Join(lines, "\n")
}

// sortRuns sorts every maximal run of consecutive lines that match.
func sortRuns(lines []string, match func(string) bool) []string {
	for i := 0; i < len(lines); {
		if !match(lines[i]) {
			i++
			continue
		}
		j := i
		for j < len(lines) && match(lines[j]) {
			j++
		}
		sort.Strings(lines[i:j])
		i = j
	}
	return lines
}

// sortJSONEntries sorts the elements of every indented JSON "entries" array,
// element by element, keeping the commas where JSON wants them.
func sortJSONEntries(lines []string) []string {
	for i := 0; i < len(lines); i++ {
		m := arrayOpen.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		elemIndent := m[1] + "  "
		closeLine := m[1] + "]"
		var blocks [][]string
		j := i + 1
		for j < len(lines) && strings.TrimRight(lines[j], ",") != closeLine {
			start := j
			for j < len(lines) && strings.TrimRight(lines[j], ",") != elemIndent+"}" {
				j++
			}
			j++
			blocks = append(blocks, append([]string(nil), lines[start:min(j, len(lines))]...))
		}
		if j >= len(lines) {
			return lines
		}
		for _, b := range blocks {
			b[len(b)-1] = strings.TrimRight(b[len(b)-1], ",")
		}
		sort.Slice(blocks, func(a, b int) bool {
			return strings.Join(blocks[a], "\n") < strings.Join(blocks[b], "\n")
		})
		k := i + 1
		for bi, b := range blocks {
			if bi < len(blocks)-1 {
				b[len(b)-1] += ","
			}
			k += copy(lines[k:], b)
		}
		i = j
	}
	return lines
}

// sortNoteItems sorts the indented item lines that follow an UnsearchedNote
// header in plain text.
func sortNoteItems(lines []string) []string {
	for i := 0; i < len(lines); i++ {
		if !strings.HasSuffix(strings.TrimSpace(lines[i]), "not a complete answer:") {
			continue
		}
		indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " "))] + "  "
		j := i + 1
		for j < len(lines) && strings.HasPrefix(lines[j], indent) && !strings.HasPrefix(strings.TrimPrefix(lines[j], indent), "… and") {
			j++
		}
		sort.Strings(lines[i+1 : j])
		i = j - 1
	}
	return lines
}
