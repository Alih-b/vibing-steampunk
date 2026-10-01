package saprfc

import (
	"reflect"
	"testing"
)

// Rows as IDOCTYPE_READ_COMPLETE returns them for E1EDKA1: BYTE_FIRST counts
// from the start of EDIDD, whose 63-character header precedes SDATA.
func TestFieldLayoutsAndCut(t *testing.T) {
	layouts := fieldLayouts([]map[string]any{
		{"SEGMENTTYP": "E1EDKA1", "FIELDNAME": "PARTN", "BYTE_FIRST": "000067", "EXTLEN": "000017"},
		{"SEGMENTTYP": "E1EDKA1", "FIELDNAME": "PARVW", "BYTE_FIRST": "000064", "EXTLEN": "000003"},
		{"SEGMENTTYP": "E1EDKA1", "FIELDNAME": "NAME1", "BYTE_FIRST": "000108", "EXTLEN": "000035"},
	}, "")
	got := layouts["E1EDKA1"]
	want := []segmentField{{"PARVW", 0, 3}, {"PARTN", 3, 17}, {"NAME1", 44, 35}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("layout %+v, want %+v", got, want)
	}
	sdata := "AG 11639" // PARTN cut short by the trimmed SDATA, NAME1 absent
	if f := cutFields(sdata, got, false); !reflect.DeepEqual(f, []IDocField{{"PARVW", "AG"}, {"PARTN", "11639"}}) {
		t.Errorf("fields %+v", f)
	}
	if f := cutFields(sdata, got, true); len(f) != 3 || f[2].Value != "" {
		t.Errorf("all fields %+v", f)
	}
	// Offsets count characters: an umlaut before a field must not shift it.
	umlaut := []segmentField{{"A", 0, 5}, {"B", 5, 3}}
	if f := cutFields("Würzb123", umlaut, false); f[0].Value != "Würzb" || f[1].Value != "123" {
		t.Errorf("umlaut cut %+v", f)
	}
}

func TestSegmentLayoutWithoutSegmentType(t *testing.T) {
	l := fieldLayouts([]map[string]any{{"FIELDNAME": "FREIGHT", "BYTE_FIRST": "000064", "EXTLEN": "000020"}}, "ZE1EDL20")
	if len(l["ZE1EDL20"]) != 1 {
		t.Errorf("layout %+v", l)
	}
}

func TestBuildSegments_FilterRawAndLimit(t *testing.T) {
	rows := []map[string]any{
		{"SEGNUM": "000001", "PSGNUM": "000000", "HLEVEL": "01", "SEGNAM": "E1EDK01", "SDATA": "000"},
		{"SEGNUM": "000002", "PSGNUM": "000001", "HLEVEL": "02", "SEGNAM": "E1EDKA1", "SDATA": "AG 11639"},
		{"SEGNUM": "000003", "PSGNUM": "000001", "HLEVEL": "02", "SEGNAM": "ZE1X", "SDATA": "raw data   "},
	}
	layouts := map[string][]segmentField{"E1EDKA1": {{"PARVW", 0, 3}, {"PARTN", 3, 17}}}
	segs, _ := buildSegments(rows, layouts, IDocOptions{MaxSegments: 10})
	if len(segs) != 3 || segs[1].Parent != "1" || segs[1].Level != "2" || len(segs[1].Fields) != 2 {
		t.Fatalf("segments %+v", segs)
	}
	if segs[2].Data != "raw data" || segs[2].Fields != nil {
		t.Errorf("a segment without layout: %+v", segs[2])
	}
	if f, n := buildSegments(rows, layouts, IDocOptions{Segment: "e1edka", MaxSegments: 10}); len(f) != 1 || f[0].Name != "E1EDKA1" || n != 1 {
		t.Errorf("filter %+v (%d matched)", f, n)
	}
	if f, n := buildSegments(rows, layouts, IDocOptions{MaxSegments: 2}); len(f) != 2 || n != 3 {
		t.Errorf("limit %d of %d", len(f), n)
	}
	// Exactly the limit matches: nothing was cut, whatever else the IDoc has.
	if f, n := buildSegments(rows, layouts, IDocOptions{Segment: "E1EDK", MaxSegments: 2}); len(f) != 2 || n != 2 {
		t.Errorf("exact fit %d of %d", len(f), n)
	}
}

func TestStatusRecords(t *testing.T) {
	rows := []map[string]any{
		{"STATUS": "50", "LOGDAT": "20260929", "LOGTIM": "182024", "COUNTR": "1"},
		{"STATUS": "64", "LOGDAT": "20260929", "LOGTIM": "182024", "COUNTR": "2", "STAMID": "B1", "STAMNO": "005", "STATXT": "cut off after seventy charac"},
		{"STATUS": "51", "LOGDAT": "20260929", "LOGTIM": "182025", "STAMID": "VG", "STAMNO": "204", "STAPA1": "0000011639", "STAPA2": ""},
		{"STATUS": "56", "LOGDAT": "20260929", "LOGTIM": "182026", "STATXT": "Partner & not found", "STAPA1": "PI_Q"},
	}
	got := statusRecords(rows, map[string]string{"VG 204": "Customer & vendor & unknown", "B1 005": "the complete text"})
	if got[2].Status != "64" || got[3].Status != "50" {
		t.Errorf("records of one second in counter order: %s, %s", got[2].Status, got[3].Status)
	}
	if got[2].Text != "the complete text" {
		t.Errorf("T100 text over the truncated STATXT: %q", got[2].Text)
	}
	if got[0].Status != "56" || got[0].Text != "Partner PI_Q not found" {
		t.Errorf("newest first, text filled: %+v", got[0])
	}
	if got[1].Text != "Customer 0000011639 vendor  unknown" || got[1].Message != "VG 204" {
		t.Errorf("message text: %+v", got[1])
	}
	if fillStatusText("&1 and &2", "a", "b") != "a and b" {
		t.Error("numbered placeholders")
	}
}

// The template is read once: an & inside an inserted value is not taken for
// another placeholder, and && stays a literal &.
func TestFillStatusText_InsertedAmpersands(t *testing.T) {
	for _, c := range []struct {
		text   string
		params []string
		want   string
	}{
		{"&1 and &2", []string{"A&B", "C"}, "A&B and C"},
		{"& then &", []string{"A&B", "C"}, "A&B then C"},
		{"&2 before &1", []string{"x", "y"}, "y before x"},
		{"R&&D for &1", []string{"z"}, "R&D for z"},
		{"&3 missing", []string{"a"}, "missing"},
	} {
		if got := fillStatusText(c.text, c.params...); got != c.want {
			t.Errorf("fillStatusText(%q, %q) = %q, want %q", c.text, c.params, got, c.want)
		}
	}
}
