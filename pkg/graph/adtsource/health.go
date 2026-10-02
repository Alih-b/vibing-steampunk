package adtsource

import (
	"time"

	"github.com/oisee/vibing-steampunk/pkg/adt"
)

// The verdicts below are the parts of a health signal the CLI and the MCP
// server compute identically. Each returns a status word and the details map
// the signal carries; the callers wrap them in their own signal types, which
// differ in what else they carry.

// UnitTestCounts counts the test classes, test methods and alerts in a run.
// An alert raised on a class counts as well as one raised on a method.
func UnitTestCounts(result *adt.UnitTestResult) (classes, methods, alerts int) {
	if result == nil {
		return 0, 0, 0
	}
	classes = len(result.Classes)
	for _, c := range result.Classes {
		methods += len(c.TestMethods)
		alerts += len(c.Alerts)
		for _, m := range c.TestMethods {
			alerts += len(m.Alerts)
		}
	}
	return classes, methods, alerts
}

// UnitTestVerdict is the signal for one test run: FAIL on any alert, NONE when
// there were no test classes, PASS otherwise.
func UnitTestVerdict(result *adt.UnitTestResult) (status string, details map[string]any) {
	classes, methods, alerts := UnitTestCounts(result)
	status = "PASS"
	if classes == 0 {
		status = "NONE"
	}
	if alerts > 0 {
		status = "FAIL"
	}
	return status, map[string]any{"classes": classes, "methods": methods, "alerts": alerts}
}

// ATCCounts counts the findings in a worklist by priority: 1 is an error, 2 a
// warning, anything else information.
func ATCCounts(result *adt.ATCWorklist) (total, errors, warnings, infos int) {
	if result == nil {
		return 0, 0, 0, 0
	}
	for _, obj := range result.Objects {
		total += len(obj.Findings)
		for _, f := range obj.Findings {
			switch f.Priority {
			case 1:
				errors++
			case 2:
				warnings++
			default:
				infos++
			}
		}
	}
	return total, errors, warnings, infos
}

// ATCVerdict is the signal for one ATC run: FINDINGS when there are any,
// CLEAN otherwise.
func ATCVerdict(result *adt.ATCWorklist) (status string, details map[string]any) {
	total, errors, warnings, infos := ATCCounts(result)
	status = "CLEAN"
	if total > 0 {
		status = "FINDINGS"
	}
	return status, map[string]any{"findings": total, "errors": errors, "warnings": warnings, "infos": infos}
}

// StalenessVerdict grades the age of the newest change: STALE after a year,
// AGING after ninety days, ACTIVE otherwise. checked is how many objects the
// date was taken over.
func StalenessVerdict(newest time.Time, checked int) (status string, details map[string]any) {
	ageDays := int(time.Since(newest).Hours() / 24)
	status = "ACTIVE"
	switch {
	case ageDays > 365:
		status = "STALE"
	case ageDays > 90:
		status = "AGING"
	}
	return status, map[string]any{"last_changed": newest.Format(time.RFC3339), "age_days": ageDays, "checked": checked}
}

// RevisionsVerdict grades an object's revision list by its newest entry:
// UNKNOWN when there is none, ERROR when its date cannot be read.
func RevisionsVerdict(revs []adt.Revision) (status string, details map[string]any) {
	if len(revs) == 0 {
		return "UNKNOWN", nil
	}
	tm, err := time.Parse(time.RFC3339, revs[0].Date)
	if err != nil {
		return "ERROR", map[string]any{"message": err.Error()}
	}
	return StalenessVerdict(tm, 1)
}
