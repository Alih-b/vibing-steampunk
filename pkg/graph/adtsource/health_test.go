package adtsource

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

func TestUnitTestVerdict(t *testing.T) {
	failing := &adt.UnitTestResult{Classes: []adt.UnitTestClass{{
		Alerts: []adt.UnitTestAlert{{}},
		TestMethods: []adt.UnitTestMethod{
			{},
			{Alerts: []adt.UnitTestAlert{{}}},
		},
	}}}
	for _, c := range []struct {
		in     *adt.UnitTestResult
		status string
		alerts int
	}{
		{nil, "NONE", 0},
		{&adt.UnitTestResult{}, "NONE", 0},
		{&adt.UnitTestResult{Classes: []adt.UnitTestClass{{TestMethods: []adt.UnitTestMethod{{}}}}}, "PASS", 0},
		{failing, "FAIL", 2},
	} {
		status, details := UnitTestVerdict(c.in)
		if status != c.status || details["alerts"] != c.alerts {
			t.Errorf("UnitTestVerdict = %s %v, want %s with %d alerts", status, details, c.status, c.alerts)
		}
	}
}

func TestATCVerdict(t *testing.T) {
	wl := &adt.ATCWorklist{Objects: []adt.ATCObject{{Findings: []adt.ATCFinding{{Priority: 1}, {Priority: 2}, {Priority: 3}, {Priority: 4}}}}}
	status, details := ATCVerdict(wl)
	if status != "FINDINGS" || details["findings"] != 4 || details["errors"] != 1 || details["warnings"] != 1 || details["infos"] != 2 {
		t.Errorf("ATCVerdict = %s %v", status, details)
	}
	if status, _ := ATCVerdict(nil); status != "CLEAN" {
		t.Errorf("ATCVerdict(nil) = %s", status)
	}
}

func TestStalenessVerdicts(t *testing.T) {
	day := 24 * time.Hour
	for _, c := range []struct {
		age  time.Duration
		want string
	}{{10 * day, "ACTIVE"}, {100 * day, "AGING"}, {400 * day, "STALE"}} {
		if got, _ := StalenessVerdict(time.Now().Add(-c.age), 1); got != c.want {
			t.Errorf("age %v: %s, want %s", c.age, got, c.want)
		}
	}
	if got, d := RevisionsVerdict(nil); got != "UNKNOWN" || d != nil {
		t.Errorf("no revisions = %s %v", got, d)
	}
	if got, _ := RevisionsVerdict([]adt.Revision{{Date: "yesterday"}}); got != "ERROR" {
		t.Errorf("unreadable date = %s", got)
	}
	if got, d := RevisionsVerdict([]adt.Revision{{Date: "2019-01-01T00:00:00Z"}}); got != "STALE" || d["checked"] != 1 {
		t.Errorf("old revision = %s %v", got, d)
	}
}

type tvarvcSQL struct {
	rows map[string][]string
	fail map[string]bool
}

func (f tvarvcSQL) RunQuery(_ context.Context, sql string, _ int) (*adt.TableContentsResult, error) {
	for table := range f.fail {
		if strings.Contains(sql, "FROM "+table+" ") {
			return nil, errors.New("refused")
		}
	}
	res := &adt.TableContentsResult{}
	for table, incs := range f.rows {
		if strings.Contains(sql, "FROM "+table+" ") {
			for _, inc := range incs {
				res.Rows = append(res.Rows, map[string]interface{}{"INCLUDE": inc})
			}
		}
	}
	return res, nil
}

func TestTVARVCReadersAsksBothTablesAndNamesEachObjectOnce(t *testing.T) {
	q := tvarvcSQL{rows: map[string][]string{
		"WBCROSSGT": {"ZCL_DEMO_A====================CM001", "ZCL_DEMO_A====================CM002"},
		"CROSS":     {"ZDEMO_REPORT", "", "ZCL_DEMO_A====================CM003"},
	}}
	readers, wbErr, crossErr := TVARVCReaders(context.Background(), q)
	if wbErr != nil || crossErr != nil {
		t.Fatal(wbErr, crossErr)
	}
	if len(readers) != 2 || readers[0] != (ObjectRef{"CLAS", "ZCL_DEMO_A"}) || readers[1] != (ObjectRef{"PROG", "ZDEMO_REPORT"}) {
		t.Errorf("readers = %+v", readers)
	}

	q.fail = map[string]bool{"WBCROSSGT": true}
	readers, wbErr, crossErr = TVARVCReaders(context.Background(), q)
	if wbErr == nil || !strings.HasPrefix(wbErr.Error(), "WBCROSSGT: ") || crossErr != nil || len(readers) != 2 {
		t.Errorf("one table down: %v %v %+v", wbErr, crossErr, readers)
	}
}

func TestD010INCRows(t *testing.T) {
	got := D010INCRows([]adt.LoadRow{{Master: "M", Include: "I", ObsoleteInVersion: 1}})
	if len(got) != 1 || got[0].Master != "M" || got[0].Include != "I" || got[0].ObsoleteInVersion != 1 {
		t.Errorf("D010INCRows = %+v", got)
	}
	if got := D010INCRows(nil); got == nil || len(got) != 0 {
		t.Errorf("D010INCRows(nil) = %#v, want empty and non-nil", got)
	}
}
