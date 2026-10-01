package adt

import (
	"fmt"
	"strings"
)

// --- ABAP Unit report: a stable, lean answer for the test tool ---

// UnitTestCounts sums up a run.
type UnitTestCounts struct {
	// Classes and Methods are what ABAP Unit reported: every test class, and
	// every test method it ran.
	Classes int `json:"classes"`
	Methods int `json:"methods"`
	// Passed and Failed split Methods. A method fails when one of its alerts
	// is a failure (see UnitTestAlertFails); a warning alone does not fail it.
	Passed int `json:"passed"`
	Failed int `json:"failed"`
	// ClassFailures counts test classes with a failure filed on the class
	// itself rather than on a method: CLASS_SETUP, CLASS_TEARDOWN or the class
	// constructor raised, and the methods may never have run.
	ClassFailures int `json:"classFailures"`
	// Warnings counts alerts that are not failures, on classes and methods.
	// The most common one is a class ABAP Unit refused to run because its risk
	// level or duration is above what the run allowed.
	Warnings int `json:"warnings"`
	// NotRun counts test classes that ran no test method and have no failure
	// of their own: ABAP Unit listed them but refused or skipped them, most
	// often because their risk level or duration is above what the run
	// allowed. Such a class is not a pass, so a run with one is not OK.
	NotRun int `json:"notRun"`
}

// UnitTestReport is what the test tool answers.
//
// The full form is the run as RunUnitTests parses it, under the same "classes"
// key and field names it always had, with ok and counts added on top. The
// failures-only form keeps the counts for the whole run but lists only what
// failed, in UnitTestFailureClass, which is the lean shape: names, and per
// alert its kind, severity, title, details and where it was raised.
type UnitTestReport struct {
	// OK is true when at least one test method ran and nothing failed, on a
	// method or on a class. A run in which no test method ran at all is not
	// OK: it is not evidence that anything works.
	OK     bool           `json:"ok"`
	Counts UnitTestCounts `json:"counts"`
	// Note says why OK is false when nothing failed.
	Note string `json:"note,omitempty"`
	// NotRunClasses names the test classes counted in Counts.NotRun.
	NotRunClasses []string `json:"notRunClasses,omitempty"`
	// OnlyFailures marks the failures-only form.
	OnlyFailures bool `json:"onlyFailures,omitempty"`
	// Classes is []UnitTestClass in the full form and []UnitTestFailureClass
	// in the failures-only form.
	Classes any `json:"classes"`
}

// UnitTestFailureClass is a test class in the failures-only form: listed when
// a method in it failed or an alert was filed on the class itself.
type UnitTestFailureClass struct {
	Name       string `json:"name"`
	ParentName string `json:"parentName,omitempty"`
	ParentType string `json:"parentType,omitempty"`
	// Alerts are those filed on the class (CLASS_SETUP / CLASS_TEARDOWN, or a
	// class that was not run), warnings included.
	Alerts      []UnitTestAlertBrief    `json:"alerts,omitempty"`
	TestMethods []UnitTestFailureMethod `json:"testMethods"`
}

// UnitTestFailureMethod is a failed test method in the failures-only form.
type UnitTestFailureMethod struct {
	Name   string               `json:"name"`
	Alerts []UnitTestAlertBrief `json:"alerts"`
}

// UnitTestAlertBrief is an alert without its URIs and stack. At is the first
// stack entry's description ("Include: <ZCL_X========CCAU> Line: <21> (TEST)"),
// which is where the failure was raised and the one part of the stack a reader
// acts on.
type UnitTestAlertBrief struct {
	Kind     string   `json:"kind"`
	Severity string   `json:"severity"`
	Title    string   `json:"title"`
	Details  []string `json:"details,omitempty"`
	At       string   `json:"at,omitempty"`
}

// UnitTestAlertFails reports whether an alert is a failure rather than a
// warning: a failed assertion or an exception, or anything SAP rates critical
// or fatal. "tolerable" and "tolerant" alerts are warnings.
func UnitTestAlertFails(a UnitTestAlert) bool {
	switch strings.ToLower(a.Kind) {
	case "failedassertion", "exception":
		return true
	}
	switch strings.ToLower(a.Severity) {
	case "critical", "fatal":
		return true
	}
	return false
}

// UnitTestMethodFailed reports whether any of a method's alerts is a failure.
func UnitTestMethodFailed(m UnitTestMethod) bool {
	for _, a := range m.Alerts {
		if UnitTestAlertFails(a) {
			return true
		}
	}
	return false
}

// CountUnitTests sums up a run.
func CountUnitTests(result *UnitTestResult) UnitTestCounts {
	var c UnitTestCounts
	if result == nil {
		return c
	}
	for _, class := range result.Classes {
		c.Classes++
		classFailed := false
		for _, a := range class.Alerts {
			if UnitTestAlertFails(a) {
				classFailed = true
			} else {
				c.Warnings++
			}
		}
		if classFailed {
			c.ClassFailures++
		} else if len(class.TestMethods) == 0 {
			c.NotRun++
		}
		for _, m := range class.TestMethods {
			c.Methods++
			if UnitTestMethodFailed(m) {
				c.Failed++
			} else {
				c.Passed++
			}
			for _, a := range m.Alerts {
				if !UnitTestAlertFails(a) {
					c.Warnings++
				}
			}
		}
	}
	return c
}

// UnitTestClassNotRun reports whether a test class ran no test method and has
// no failure of its own, i.e. ABAP Unit listed it but did not run it.
func UnitTestClassNotRun(class UnitTestClass) bool {
	if len(class.TestMethods) > 0 {
		return false
	}
	for _, a := range class.Alerts {
		if UnitTestAlertFails(a) {
			return false
		}
	}
	return true
}

// UnitTestNotRunClasses names the classes UnitTestClassNotRun reports.
func UnitTestNotRunClasses(result *UnitTestResult) []string {
	if result == nil {
		return nil
	}
	var names []string
	for _, class := range result.Classes {
		if UnitTestClassNotRun(class) {
			names = append(names, class.Name)
		}
	}
	return names
}

// NewUnitTestReport builds the test tool's answer. With onlyFailures it lists
// only failed methods and classes with alerts of their own, in the lean shape;
// the counts always cover the whole run.
func NewUnitTestReport(result *UnitTestResult, onlyFailures bool) UnitTestReport {
	if result == nil {
		result = &UnitTestResult{}
	}
	counts := CountUnitTests(result)
	report := UnitTestReport{
		Counts:       counts,
		OnlyFailures: onlyFailures,
	}
	report.NotRunClasses = UnitTestNotRunClasses(result)
	failed := counts.Failed > 0 || counts.ClassFailures > 0
	report.OK = !failed && counts.Methods > 0 && counts.NotRun == 0
	if !failed {
		switch {
		case counts.Classes == 0:
			report.Note = "ABAP Unit reported no test class for this object, so nothing ran"
		case counts.Methods == 0:
			report.Note = "ABAP Unit listed test classes but ran no test method; see the class alerts"
		case counts.NotRun > 0:
			report.Note = fmt.Sprintf("ABAP Unit did not run %d test class(es): %s; see their class alerts (often a risk level or duration above what the run allowed)",
				counts.NotRun, strings.Join(report.NotRunClasses, ", "))
		}
	}

	if !onlyFailures {
		classes := result.Classes
		if classes == nil {
			classes = []UnitTestClass{}
		}
		report.Classes = classes
		return report
	}

	brief := []UnitTestFailureClass{}
	for _, class := range result.Classes {
		fc := UnitTestFailureClass{
			Name:        class.Name,
			ParentName:  class.ParentName,
			ParentType:  class.ParentType,
			Alerts:      briefAlerts(class.Alerts),
			TestMethods: []UnitTestFailureMethod{},
		}
		for _, m := range class.TestMethods {
			if !UnitTestMethodFailed(m) {
				continue
			}
			fc.TestMethods = append(fc.TestMethods, UnitTestFailureMethod{Name: m.Name, Alerts: briefAlerts(m.Alerts)})
		}
		if len(fc.Alerts) == 0 && len(fc.TestMethods) == 0 {
			continue
		}
		brief = append(brief, fc)
	}
	report.Classes = brief
	return report
}

func briefAlerts(alerts []UnitTestAlert) []UnitTestAlertBrief {
	out := make([]UnitTestAlertBrief, 0, len(alerts))
	for _, a := range alerts {
		b := UnitTestAlertBrief{Kind: a.Kind, Severity: a.Severity, Title: a.Title}
		if len(a.Details) > 0 {
			b.Details = a.Details
		}
		if len(a.Stack) > 0 {
			b.At = a.Stack[0].Description
		}
		out = append(out, b)
	}
	return out
}
