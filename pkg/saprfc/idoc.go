package saprfc

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/oisee/open-rfc-go/rfc"
)

// An IDoc's segment data sits in EDID4-SDATA, an LRAW field that neither the
// ADT data preview nor RFC_READ_TABLE can select, and IDOC_READ_COMPLETELY is
// not remote-enabled. The EDI document API is: EDI_DOCUMENT_OPEN_FOR_READ,
// EDI_SEGMENTS_GET_ALL and EDI_DOCUMENT_READ_ALL_STATUS, on one connection,
// since the open document lives in the session. The field layout of each
// segment comes from IDOCTYPE_READ_COMPLETE (basic type and extension), and
// from SEGMENT_READ_COMPLETE for a segment the type does not list.

// IDoc is one IDoc as WE02 shows it.
type IDoc struct {
	Number   string            `json:"number"`
	Control  map[string]string `json:"control"`
	Status   []IDocStatus      `json:"status"`
	Segments []IDocSegment     `json:"segments"`
	// SegmentCount is the number of segments in the IDoc; Segments may hold
	// fewer when a filter or the limit applied.
	SegmentCount int      `json:"segment_count"`
	Warnings     []string `json:"warnings,omitempty"`
}

// IDocStatus is one status record, newest first.
type IDocStatus struct {
	Status string `json:"status"`
	// Meaning is what the status code stands for (TEDS2), e.g. "Application
	// document not posted" for 51.
	Meaning string `json:"meaning,omitempty"`
	Date    string `json:"date"`
	Time    string `json:"time"`
	Text    string `json:"text,omitempty"`
	Message string `json:"message,omitempty"` // message class and number, e.g. "E0 066"
	Segment string `json:"segment,omitempty"` // segment number the status refers to
	User    string `json:"user,omitempty"`
	Program string `json:"program,omitempty"`
	counter string // EDIDS-COUNTR, the order within one second
}

// IDocSegment is one data record.
type IDocSegment struct {
	Number string `json:"number"`
	Parent string `json:"parent,omitempty"`
	Level  string `json:"level"`
	Name   string `json:"name"`
	// Fields holds the segment's fields in definition order; empty ones are
	// left out unless all fields were asked for.
	Fields []IDocField `json:"fields,omitempty"`
	// Data is the raw SDATA, given when no field layout was found.
	Data string `json:"data,omitempty"`
}

// IDocField is one field of a segment.
type IDocField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// IDocOptions narrows what ReadIDoc returns.
type IDocOptions struct {
	Segment     string // only segments of this type (prefix match, e.g. "E1EDKA1")
	AllFields   bool   // also fields that are empty
	MaxSegments int    // default 500
	Raw         bool   // SDATA as it is, no field layout
}

// segmentField is a field's place in SDATA.
type segmentField struct {
	name   string
	offset int // in characters, 0-based within SDATA
	length int
}

// edidHeader is the length of EDIDD in front of SDATA (MANDT, DOCNUM, SEGNUM,
// SEGNAM, PSGNUM, HLEVEL); BYTE_FIRST in the segment metadata counts from the
// start of the whole record, 1-based.
const edidHeader = 63

// ReadIDoc reads one IDoc: control record, status records and segments.
func ReadIDoc(ctx context.Context, c *rfc.Client, number string, opts IDocOptions) (*IDoc, error) {
	number = strings.TrimSpace(number)
	if number == "" || strings.Trim(number, "0123456789") != "" {
		return nil, fmt.Errorf("an IDoc number is digits only, got %q", number)
	}
	number = fmt.Sprintf("%016s", strings.TrimLeft(number, "0"))
	if opts.MaxSegments <= 0 {
		opts.MaxSegments = 500
	}

	doc, rows, statusRows, err := readOpenedIDoc(ctx, c, number)
	if err != nil {
		return nil, err
	}
	if statusRows != nil {
		doc.Status = statusRecords(statusRows, messageTexts(ctx, c, statusRows))
		meanings := statusMeanings(ctx, c)
		for i := range doc.Status {
			doc.Status[i].Meaning = meanings[doc.Status[i].Status]
		}
	}

	var layouts map[string][]segmentField
	if !opts.Raw {
		layouts, err = segmentLayouts(ctx, c, doc.Control["IDOCTP"], doc.Control["CIMTYP"])
		if err != nil {
			doc.Warnings = append(doc.Warnings, "segment layout unavailable, data given raw: "+err.Error())
		}
		for _, name := range missingSegmentTypes(rows, layouts) {
			fields, lerr := oneSegmentLayout(ctx, c, name)
			if lerr != nil {
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("no field layout for %s, data given raw: %v", name, lerr))
				continue
			}
			if layouts == nil {
				layouts = map[string][]segmentField{}
			}
			layouts[name] = fields
		}
	}

	var matched int
	doc.Segments, matched = buildSegments(rows, layouts, opts)
	if matched > len(doc.Segments) {
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("showing %d of %d segments; narrow with \"segment\" or raise \"max_segments\"", len(doc.Segments), matched))
	}
	return doc, nil
}

// readOpenedIDoc opens the IDoc, reads its segments and status records, and
// closes it again. The EDI document API keeps the opened document in the
// session, so all four calls run on one pinned connection; Client.Call could
// put each on another. The status rows are nil when they could not be read.
func readOpenedIDoc(ctx context.Context, c *rfc.Client, number string) (*IDoc, []map[string]any, []map[string]any, error) {
	s, err := c.Pin(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	defer s.Close()

	opened, err := s.Call(ctx, "EDI_DOCUMENT_OPEN_FOR_READ", rfc.Params{"DOCUMENT_NUMBER": number})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("IDoc %s: %w", strings.TrimLeft(number, "0"), err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = s.Call(cctx, "EDI_DOCUMENT_CLOSE_READ", rfc.Params{"DOCUMENT_NUMBER": number})
	}()

	doc := &IDoc{Number: strings.TrimLeft(number, "0"), Control: nonEmpty(asRecord(opened.Get("IDOC_CONTROL")))}

	segs, err := s.Call(ctx, "EDI_SEGMENTS_GET_ALL", rfc.Params{"DOCUMENT_NUMBER": number})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("EDI_SEGMENTS_GET_ALL: %w", err)
	}
	rows := segs.Table("IDOC_CONTAINERS")
	doc.SegmentCount = len(rows)

	st, err := s.Call(ctx, "EDI_DOCUMENT_READ_ALL_STATUS", rfc.Params{"DOCUMENT_NUMBER": number})
	if err != nil {
		doc.Warnings = append(doc.Warnings, "status records unavailable: "+err.Error())
		return doc, rows, nil, nil
	}
	statusRows := st.Table("INT_EDIDS")
	if statusRows == nil {
		statusRows = []map[string]any{}
	}
	return doc, rows, statusRows, nil
}

// segmentLayouts reads the field layout of every segment of a basic type and
// its extension.
func segmentLayouts(ctx context.Context, c *rfc.Client, idoctyp, cimtyp string) (map[string][]segmentField, error) {
	if idoctyp == "" {
		return nil, fmt.Errorf("the control record names no basic type")
	}
	p := rfc.Params{"PI_IDOCTYP": idoctyp}
	if cimtyp != "" {
		p["PI_CIMTYP"] = cimtyp
	}
	res, err := c.Call(ctx, "IDOCTYPE_READ_COMPLETE", p)
	if err != nil {
		return nil, fmt.Errorf("IDOCTYPE_READ_COMPLETE %s: %w", idoctyp, err)
	}
	return fieldLayouts(res.Table("PT_FIELDS"), ""), nil
}

// oneSegmentLayout reads the field layout of one segment type.
func oneSegmentLayout(ctx context.Context, c *rfc.Client, segtyp string) ([]segmentField, error) {
	res, err := c.Call(ctx, "SEGMENT_READ_COMPLETE", rfc.Params{"PI_SEGTYP": segtyp})
	if err != nil {
		return nil, err
	}
	return fieldLayouts(res.Table("PT_FIELDS"), segtyp)[segtyp], nil
}

// fieldLayouts groups EDI_IAPI12 rows by segment type. SEGMENT_READ_COMPLETE
// may leave SEGMENTTYP empty; segtyp then names them.
func fieldLayouts(rows []map[string]any, segtyp string) map[string][]segmentField {
	out := map[string][]segmentField{}
	for _, r := range rows {
		name := str(r["SEGMENTTYP"])
		if name == "" {
			name = segtyp
		}
		first, _ := strconv.Atoi(str(r["BYTE_FIRST"]))
		length, _ := strconv.Atoi(str(r["EXTLEN"]))
		if name == "" || first <= edidHeader || length <= 0 {
			continue
		}
		out[name] = append(out[name], segmentField{name: str(r["FIELDNAME"]), offset: first - edidHeader - 1, length: length})
	}
	for name := range out {
		sort.SliceStable(out[name], func(i, j int) bool { return out[name][i].offset < out[name][j].offset })
	}
	return out
}

func missingSegmentTypes(rows []map[string]any, layouts map[string][]segmentField) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		name := str(r["SEGNAM"])
		if name == "" || seen[name] || layouts[name] != nil {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// buildSegments turns EDIDD rows into segments, cutting SDATA into fields
// where a layout is known.
func buildSegments(rows []map[string]any, layouts map[string][]segmentField, opts IDocOptions) ([]IDocSegment, int) {
	filter := strings.ToUpper(strings.TrimSpace(opts.Segment))
	var out []IDocSegment
	matched := 0
	for _, r := range rows {
		name := str(r["SEGNAM"])
		if filter != "" && !strings.HasPrefix(name, filter) {
			continue
		}
		matched++
		if len(out) >= opts.MaxSegments {
			continue
		}
		seg := IDocSegment{
			Number: strings.TrimLeft(str(r["SEGNUM"]), "0"),
			Parent: strings.TrimLeft(str(r["PSGNUM"]), "0"),
			Level:  trimZeros(str(r["HLEVEL"])),
			Name:   name,
		}
		sdata := fmt.Sprint(r["SDATA"])
		if r["SDATA"] == nil {
			sdata = ""
		}
		if layout := layouts[name]; len(layout) > 0 && !opts.Raw {
			seg.Fields = cutFields(sdata, layout, opts.AllFields)
		} else {
			seg.Data = strings.TrimRight(sdata, " ")
		}
		out = append(out, seg)
	}
	return out, matched
}

// cutFields cuts SDATA at the layout's offsets. Offsets count characters, as
// SAP does in a Unicode system, not bytes.
func cutFields(sdata string, layout []segmentField, all bool) []IDocField {
	data := []rune(sdata)
	var out []IDocField
	for _, f := range layout {
		v := ""
		if f.offset < len(data) {
			end := f.offset + f.length
			if end > len(data) {
				end = len(data)
			}
			v = strings.TrimSpace(string(data[f.offset:end]))
		}
		if v == "" && !all {
			continue
		}
		out = append(out, IDocField{Name: f.name, Value: v})
	}
	return out
}

// statusRecords turns EDIDS rows into status records, newest first, with the
// & placeholders of the status text filled in.
func statusRecords(rows []map[string]any, texts map[string]string) []IDocStatus {
	out := make([]IDocStatus, 0, len(rows))
	for _, r := range rows {
		s := IDocStatus{
			Status:  str(r["STATUS"]),
			Date:    str(r["LOGDAT"]),
			Time:    str(r["LOGTIM"]),
			Text:    fillStatusText(str(r["STATXT"]), str(r["STAPA1"]), str(r["STAPA2"]), str(r["STAPA3"]), str(r["STAPA4"])),
			counter: str(r["COUNTR"]),
			Segment: strings.TrimLeft(str(r["SEGNUM"]), "0"),
			User:    str(r["UNAME"]),
			Program: str(r["REPID"]), //nolint:misspell // EDIDS-REPID, the SAP field name
		}
		if id := str(r["STAMID"]); id != "" {
			s.Message = id + " " + str(r["STAMNO"])
			if t := texts[s.Message]; t != "" {
				s.Text = fillStatusText(t, str(r["STAPA1"]), str(r["STAPA2"]), str(r["STAPA3"]), str(r["STAPA4"]))
			}
		}
		out = append(out, s)
	}
	// Records of one second are common; the counter orders them.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Date+out[i].Time, out[j].Date+out[j].Time
		if a != b {
			return a > b
		}
		return fmt.Sprintf("%016s", out[i].counter) > fmt.Sprintf("%016s", out[j].counter)
	})
	return out
}

// fillStatusText replaces the placeholders of a status text: &1..&4 by
// position, a bare & by the next parameter in order, && by a literal &. It
// reads the template once, so an & inside an inserted value stays as it is.
func fillStatusText(text string, params ...string) string {
	var b strings.Builder
	next := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '&' {
			b.WriteByte(text[i])
			continue
		}
		if i+1 < len(text) && text[i+1] == '&' {
			b.WriteByte('&')
			i++
			continue
		}
		if i+1 < len(text) && text[i+1] >= '1' && text[i+1] <= '9' {
			if n := int(text[i+1] - '1'); n < len(params) {
				b.WriteString(params[n])
			}
			i++
			continue
		}
		if next < len(params) {
			b.WriteString(params[next])
			next++
		}
	}
	return strings.TrimSpace(b.String())
}

// asRecord turns a structure from an RFC result into strings.
func asRecord(v any) map[string]string {
	out := map[string]string{}
	if m, ok := v.(map[string]any); ok {
		for k, val := range m {
			out[k] = str(val)
		}
	}
	return out
}

func nonEmpty(m map[string]string) map[string]string {
	for k, v := range m {
		if v == "" {
			delete(m, k)
		}
	}
	return m
}

func trimZeros(s string) string {
	t := strings.TrimLeft(s, "0")
	if t == "" && s != "" {
		return "0"
	}
	return t
}

// messageTexts reads the T100 texts of the status messages. EDIDS-STATXT
// holds 70 characters and is empty for many messages; the message itself
// is complete. English first, German next.
func messageTexts(ctx context.Context, c *rfc.Client, rows []map[string]any) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		id, no := str(r["STAMID"]), str(r["STAMNO"])
		key := id + " " + no
		if id == "" || out[key] != "" {
			continue
		}
		where := fmt.Sprintf("ARBGB = '%s' AND MSGNR = '%s' AND ( SPRSL = 'E' OR SPRSL = 'D' )", strings.ReplaceAll(id, "'", "''"), no)
		texts, err := ReadTable(ctx, c, "T100", where, []string{"SPRSL", "TEXT"}, 2)
		if err != nil {
			continue
		}
		for _, t := range texts {
			if out[key] == "" || strings.TrimSpace(t["SPRSL"]) == "E" {
				out[key] = strings.TrimSpace(t["TEXT"])
			}
		}
	}
	return out
}

// statusMeanings reads the short texts of the IDoc status codes, English
// first and German where English is missing.
func statusMeanings(ctx context.Context, c *rfc.Client) map[string]string {
	out := map[string]string{}
	rows, err := ReadTable(ctx, c, "TEDS2", "LANGUA = 'E' OR LANGUA = 'D'", []string{"STATUS", "LANGUA", "DESCRP"}, 0)
	if err != nil {
		return out
	}
	for _, r := range rows {
		st := strings.TrimSpace(r["STATUS"])
		if out[st] == "" || strings.TrimSpace(r["LANGUA"]) == "E" {
			out[st] = strings.TrimSpace(r["DESCRP"])
		}
	}
	return out
}
