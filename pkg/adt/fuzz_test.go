package adt

import (
	"bytes"
	"encoding/xml"
	"reflect"
	"strings"
	"testing"
)

// The decoders below take whatever body SAP sends back. A gateway HTML error
// page, a truncated exception document or a body from another release must
// come back as an error or an empty result, never a panic: a panic in the
// middle of an edit can leave the object's ENQUEUE lock held.

// htmlErrorPage is what an ICM or a reverse proxy answers instead of XML.
const htmlErrorPage = `<!DOCTYPE html><html><head><title>500 Internal Server Error</title></head>` +
	`<body><h1>Internal Server Error</h1><p>ICMENOSESSION</p></body></html>`

const lockOK = `<?xml version="1.0" encoding="UTF-8"?>
<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>
<LOCK_HANDLE>HANDLE-1</LOCK_HANDLE><CORRNR>TR-EXAMPLE</CORRNR><CORRUSER>TESTUSER</CORRUSER>
<CORRTEXT>demo &amp; test</CORRTEXT><IS_LOCAL>X</IS_LOCAL><IS_LINK_UP></IS_LINK_UP>
<MODIFICATION_SUPPORT>Modification</MODIFICATION_SUPPORT>
</DATA></asx:values></asx:abap>`

const lockConflict = `<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">` +
	`<namespace id="com.sap.adt"/><type id="ExceptionResourceNoAccess"/>` +
	`<message lang="EN">User TESTUSER is currently editing ZCL_DEMO_LOCKED</message></exc:exception>`

// exceptionSeeds are ADT exception documents and the things that arrive in
// their place.
var exceptionSeeds = []string{
	lockConflict,
	`<exc:exception><type id="ExceptionResourceNotFound"/></exc:exception>`,
	`<exc:exception><type id="ExceptionAuthorization"/><message>not authorised</message></exc:exception>`,
	`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">` +
		`<message lang="EN">System expected the element '{http://www.sap.com/cts/adt/tm}root'</message></exc:exception>`,
	`<exc:exception><type id="ExceptionResourceAlreadyExists"/><message lang="EN">Resource exists</message>`,
	`<exc:exception><message>Unknown column name "FOO" &quot;x&quot;</message></exc:exception>`,
	htmlErrorPage,
	"",
	"<",
}

// FuzzADTException feeds an error body to everything that reads one: the lock
// parser (which routes exception documents to lockExceptionError), the
// exception reader itself, and the APIError helpers the transport and the
// query explainer run on every 4xx.
func FuzzADTException(f *testing.F) {
	for _, s := range exceptionSeeds {
		f.Add([]byte(s))
	}
	f.Add([]byte(lockOK))
	f.Fuzz(func(t *testing.T, body []byte) {
		if err := lockExceptionError(body); err == nil {
			t.Fatal("lockExceptionError returned nil")
		}
		if res, err := parseLockResult(body); err == nil && res == nil {
			t.Fatal("parseLockResult returned neither a result nor an error")
		} else if err == nil && bytes.Contains(body, []byte("exc:exception")) {
			t.Fatal("an exception document parsed as a lock result")
		}
		for _, status := range []int{400, 403, 404, 500} {
			api := &APIError{StatusCode: status, Message: string(body), Path: "/sap/bc/adt/x"}
			_ = api.Error()
			_ = api.IsNotFound()
			_ = api.IsSessionExpired()
			_ = IsResourceAlreadyExists(api)
			if _, ok := sapQueryMessage(api); ok && status != 400 {
				t.Fatalf("status %d read as a query message", status)
			}
		}
	})
}

// FuzzLockResult checks the lock parser round-trips: a result written back as
// ABAP serialization XML parses to the same result.
func FuzzLockResult(f *testing.F) {
	f.Add([]byte(lockOK))
	f.Add([]byte(lockConflict))
	f.Add([]byte(htmlErrorPage))
	f.Add([]byte(`<asx:abap><asx:values><DATA><LOCK_HANDLE></LOCK_HANDLE></DATA></asx:values></asx:abap>`))
	f.Fuzz(func(t *testing.T, body []byte) {
		res, err := parseLockResult(body)
		if err != nil {
			return
		}
		again, err := parseLockResult(encodeLockResult(res))
		if err != nil {
			t.Fatalf("re-encoded lock result does not parse: %v", err)
		}
		if *again != *res {
			t.Fatalf("lock result changed on a round trip:\n%+v\n%+v", res, again)
		}
	})
}

func encodeLockResult(r *LockResult) []byte {
	flag := func(b bool) string {
		if b {
			return "X"
		}
		return ""
	}
	var b bytes.Buffer
	b.WriteString(`<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>`)
	for _, f := range [][2]string{
		{"LOCK_HANDLE", r.LockHandle}, {"CORRNR", r.CorrNr}, {"CORRUSER", r.CorrUser},
		{"CORRTEXT", r.CorrText}, {"IS_LOCAL", flag(r.IsLocal)}, {"IS_LINK_UP", flag(r.IsLinkUp)},
		{"MODIFICATION_SUPPORT", r.ModificationSupport},
	} {
		b.WriteString("<" + f[0] + ">")
		_ = xml.EscapeText(&b, []byte(f[1]))
		b.WriteString("</" + f[0] + ">")
	}
	b.WriteString(`</DATA></asx:values></asx:abap>`)
	return b.Bytes()
}

// FuzzActivationResult: the activation parser never fails (an unreadable body
// becomes an error message), and its verdict agrees with what it returns —
// an error-class message or an inactive object is never a success.
func FuzzActivationResult(f *testing.F) {
	for _, s := range []string{
		activationFailedRoot, activationFailedWrapped, activationWarning,
		activationInactiveRoot, activationMultiTxt, activationNotExecuted, activationExecutedWithWarning,
		htmlErrorPage, lockConflict, "",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		res, err := parseActivationResult(body)
		if err != nil {
			t.Fatalf("parseActivationResult returned an error: %v", err)
		}
		if res == nil || res.Messages == nil || res.Inactive == nil {
			t.Fatalf("incomplete result: %+v", res)
		}
		if len(res.Inactive) > 0 && res.Success {
			t.Fatal("inactive objects reported as a success")
		}
		for _, m := range res.Messages {
			if strings.ContainsAny(m.Type, activationErrorTypes) && res.Success {
				t.Fatalf("error message %+v reported as a success", m)
			}
		}
	})
}

// FuzzSearchResults round-trips the repository search answer.
func FuzzSearchResults(f *testing.F) {
	f.Add([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:objectReference adtcore:uri="/sap/bc/adt/programs/programs/ztest_program" adtcore:type="PROG/P" adtcore:name="ZTEST_PROGRAM" adtcore:packageName="ZTEST" adtcore:description="Test Program"/>
  <adtcore:objectReference adtcore:uri="/sap/bc/adt/oo/classes/zcl_test_class" adtcore:type="CLAS/OC" adtcore:name="ZCL_TEST_CLASS" adtcore:packageName="ZTEST" adtcore:description="Test &amp; Class"/>
</adtcore:objectReferences>`))
	f.Add([]byte(`<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core"/>`))
	f.Add([]byte(htmlErrorPage))
	f.Add([]byte(lockConflict))
	f.Fuzz(func(t *testing.T, body []byte) {
		results, err := ParseSearchResults(body)
		if err != nil {
			return
		}
		out, err := xml.Marshal(SearchResults{Results: results})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		again, err := ParseSearchResults(out)
		if err != nil {
			t.Fatalf("re-encoded search results do not parse: %v\n%s", err, out)
		}
		if len(again) == 0 && len(results) == 0 {
			return
		}
		if !reflect.DeepEqual(again, results) {
			t.Fatalf("search results changed on a round trip:\n%+v\n%+v", results, again)
		}
	})
}

// FuzzReleaseReports: the release answer is either read or refused with an
// error; read, it yields one status line per report plus one per message.
func FuzzReleaseReports(f *testing.F) {
	f.Add([]byte(`<?xml version="1.0" encoding="utf-8"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm" xmlns:chkrun="http://www.sap.com/adt/checkrun">
  <tm:releasereports>
    <chkrun:checkReport chkrun:reporter="CTS" chkrun:status="released">
      <chkrun:checkMessageList>
        <chkrun:checkMessage chkrun:type="I" chkrun:shortText="Transport released successfully"/>
        <chkrun:checkMessage chkrun:type="W" chkrun:shortText="Some objects were ignored"/>
      </chkrun:checkMessageList>
    </chkrun:checkReport>
    <chkrun:checkReport chkrun:reporter="SYNTAX" chkrun:status="passed"/>
  </tm:releasereports>
</tm:root>`))
	f.Add([]byte(`<?xml version="1.0" encoding="utf-8"?><tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"></tm:root>`))
	f.Add([]byte(htmlErrorPage))
	f.Add([]byte(lockConflict))
	f.Add([]byte("  \n"))
	f.Fuzz(func(t *testing.T, body []byte) {
		reports, err := parseReleaseReports(body)
		lines, err2 := parseReleaseResult(body)
		if (err == nil) != (err2 == nil) {
			t.Fatalf("parseReleaseReports err=%v, parseReleaseResult err=%v", err, err2)
		}
		if err != nil {
			return
		}
		want := len(reports)
		for _, r := range reports {
			want += len(r.Messages)
		}
		if len(lines) != want {
			t.Fatalf("%d lines for %d reports and their messages", len(lines), want)
		}
	})
}

// FuzzDumpDetail: a short dump's formatted text is free-form and cut by
// column positions, the kind of parsing that indexes past the end of a short
// line. It runs on every dump a caller opens.
func FuzzDumpDetail(f *testing.F) {
	for _, s := range []string{
		formattedDumpDetailSample, formattedDumpNoPositionSample, formattedDumpShortHeaderSample,
		formattedDumpSample, "this is not a dump", "", "|", "|Short Text|\n|x\\\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, formatted string) {
		if d := parseDumpDetail(formatted); d == nil {
			t.Fatal("parseDumpDetail returned nil")
		}
	})
}
