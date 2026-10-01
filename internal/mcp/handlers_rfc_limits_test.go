package mcp

import "testing"

// Each op takes its name from its own parameter only: a job_name given to
// "run" is not the report to run.
func TestRFCNameKey_IsOpSpecific(t *testing.T) {
	for op, want := range map[string]string{
		"run": "report", "job": "job_name", "read_table": "table", "search": "pattern",
		"call": "function", "describe": "function", "info": "", "ping": "",
	} {
		if got := rfcNameKey(op); got != want {
			t.Errorf("rfcNameKey(%q) = %q, want %q", op, got, want)
		}
	}
}

func TestTruncateAtLine(t *testing.T) {
	s := "line one\nline two\nline three\n"
	if got, cut := truncateAtLine(s, 0); got != s || cut {
		t.Errorf("no limit: %q %v", got, cut)
	}
	if got, cut := truncateAtLine(s, len(s)); got != s || cut {
		t.Errorf("exact fit: %q %v", got, cut)
	}
	if got, cut := truncateAtLine(s, 15); got != "line one\n" || !cut {
		t.Errorf("cut: %q %v", got, cut)
	}
}
