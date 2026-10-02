package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// --- SQL Trace (ST05) Operations ---

// The ST05 model, rewritten against what the server sends.
//
// Everything here was previously modelled from a guess and never corrected,
// because the request carried a concrete Accept that this resource answers with
// 406. Both calls failed on every invocation they had ever made, so no response
// was ever parsed and no parser was ever contradicted. Fixing the Accept made
// the requests succeed and the parsers wrong in the same minute.
//
// What the resource actually returns is broader than "is SQL trace on": a row
// per application server instance, each carrying eight independent trace types.
// SQL is one of them.

// SQLTraceState is the trace state of one application server instance.
type SQLTraceState struct {
	Instance   string `json:"instance"`
	Host       string `json:"host,omitempty"`
	IsLocal    bool   `json:"isLocal"`
	IsSelected bool   `json:"isSelected"`
	// ModifiedBy and ModifiedAt are who last changed this instance's trace
	// settings, which is the only "when" the resource offers — there is no
	// start time for a running trace.
	ModifiedBy string `json:"modifiedBy,omitempty"`
	ModifiedAt string `json:"modifiedAt,omitempty"`

	// Types is the eight switches, by their own names. A map rather than eight
	// fields because callers ask "is anything on" far more often than they ask
	// about one, and because the set has grown before.
	Types map[string]bool `json:"types"`

	// Filter narrows what is recorded. Empty strings mean unfiltered, which is
	// the ordinary state and is why they are omitted.
	TraceUser       string `json:"traceUser,omitempty"`
	TransactionCode string `json:"transactionCode,omitempty"`
	Program         string `json:"program,omitempty"`
	RFCFunction     string `json:"rfcFunction,omitempty"`
	URL             string `json:"url,omitempty"`
	WorkProcessID   string `json:"workProcessId,omitempty"`

	StackTrace     bool `json:"stackTrace"`
	AuthErrorsOnly bool `json:"authErrorsOnly"`
}

// Active reports whether any trace type is switched on for this instance.
func (s *SQLTraceState) Active() bool {
	for _, on := range s.Types {
		if on {
			return true
		}
	}
	return false
}

// SQLTraceOn reports whether the SQL trace specifically is on.
func (s *SQLTraceState) SQLTraceOn() bool { return s.Types["sql"] }

// SQLTraceDirectory is what the trace directory resource returns.
//
// On 7.58 it returns no entries at all: one URI, pointing at the STMC web
// application where the traces are read. Modelling it as a list of files and
// returning an empty one would say "this system has no traces", which is a
// different statement and not one this resource makes.
type SQLTraceDirectory struct {
	// Entries is what the resource lists, where it lists anything.
	Entries []SQLTraceEntry `json:"entries"`
	// AnalysisURL is where the release directs a reader instead. When it is set
	// and Entries is empty, the absence of entries is a property of the
	// resource and not of the system.
	AnalysisURL string `json:"analysisUrl,omitempty"`
}

// SQLTraceEntry represents a trace file in the directory.
type SQLTraceEntry struct {
	ID          string `json:"id"`
	User        string `json:"user"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime,omitempty"`
	TraceType   string `json:"traceType"`
	RecordCount int    `json:"recordCount"`
	Size        int64  `json:"size,omitempty"`
	URI         string `json:"uri"`
}

// GetSQLTraceState returns the trace state of every application server
// instance this system reports.
func (c *Client) GetSQLTraceState(ctx context.Context) ([]SQLTraceState, error) {
	// Measured on 7.58: this resource answers */* and refuses everything else
	// with 406 — application/xml, text/plain, and both spellings of the vendor
	// type it might plausibly want. The concrete type here made GetSQLTraceState
	// fail on every call it had ever made. Third place this week; the debugger
	// and the RFC tunnel were the first two.
	resp, err := c.transport.Request(ctx, "/sap/bc/adt/st05/trace/state", &RequestOptions{
		Method: http.MethodGet,
		Accept: "*/*",
	})
	if err != nil {
		return nil, fmt.Errorf("getting SQL trace state: %w", err)
	}

	return parseSQLTraceState(resp.Body)
}

// ListSQLTraces retrieves a list of SQL trace files.
func (c *Client) ListSQLTraces(ctx context.Context, user string, maxResults int) (*SQLTraceDirectory, error) {
	params := url.Values{}
	if user != "" {
		params.Set("user", user)
	}
	if maxResults > 0 {
		params.Set("$top", fmt.Sprintf("%d", maxResults))
	}

	endpoint := "/sap/bc/adt/st05/trace/directory"
	if len(params) > 0 {
		endpoint = endpoint + "?" + params.Encode()
	}

	// Same measurement, same answer: the trace directory refuses
	// application/atom+xml with a 406 and answers */*. Both ST05 resources were
	// unreachable, and only one of them was being probed.
	resp, err := c.transport.Request(ctx, endpoint, &RequestOptions{
		Method: http.MethodGet,
		Accept: "*/*",
	})
	if err != nil {
		return nil, fmt.Errorf("listing SQL traces: %w", err)
	}

	return parseSQLTraceDirectory(resp.Body)
}

// parseSQLTraceState reads the instance table the resource actually sends.
func parseSQLTraceState(data []byte) ([]SQLTraceState, error) {
	type flags struct {
		SQL  string `xml:"sqlOn"`
		Buf  string `xml:"bufOn"`
		Enq  string `xml:"enqOn"`
		RFC  string `xml:"rfcOn"`
		HTTP string `xml:"httpOn"`
		APC  string `xml:"apcOn"`
		AMC  string `xml:"amcOn"`
		Auth string `xml:"authOn"`
	}
	type props struct {
		AuthErrorsOnly string `xml:"authErrorsOnly"`
		StackTraceOn   string `xml:"stackTraceOn"`
	}
	type filter struct {
		TraceUser       string `xml:"traceUser"`
		TransactionCode string `xml:"transactionCode"`
		Program         string `xml:"program"`
		RFCFunction     string `xml:"rfcFunction"`
		URL             string `xml:"url"`
		WpID            string `xml:"wpId"`
	}
	type instance struct {
		Instance   string `xml:"instance"`
		Host       string `xml:"host"`
		IsLocal    string `xml:"isLocal"`
		IsSelected string `xml:"isSelected"`
		ModUser    string `xml:"modificationUser"`
		ModAt      string `xml:"modificationDateTime"`
		Types      flags  `xml:"traceTypes"`
		Props      props  `xml:"traceProperties"`
		Filter     filter `xml:"traceFilter"`
	}
	var table struct {
		XMLName   xml.Name   `xml:"traceStateInstanceTable"`
		Instances []instance `xml:"traceStateInstance"`
	}
	if err := xml.Unmarshal(data, &table); err != nil {
		return nil, fmt.Errorf("parsing SQL trace state: %w", err)
	}

	out := make([]SQLTraceState, 0, len(table.Instances))
	for _, in := range table.Instances {
		out = append(out, SQLTraceState{
			Instance:   strings.TrimSpace(in.Instance),
			Host:       strings.TrimSpace(in.Host),
			IsLocal:    xmlBool(in.IsLocal),
			IsSelected: xmlBool(in.IsSelected),
			ModifiedBy: strings.TrimSpace(in.ModUser),
			ModifiedAt: strings.TrimSpace(in.ModAt),
			Types: map[string]bool{
				"sql": xmlBool(in.Types.SQL), "buffer": xmlBool(in.Types.Buf),
				"enqueue": xmlBool(in.Types.Enq), "rfc": xmlBool(in.Types.RFC),
				"http": xmlBool(in.Types.HTTP), "apc": xmlBool(in.Types.APC),
				"amc": xmlBool(in.Types.AMC), "authorization": xmlBool(in.Types.Auth),
			},
			TraceUser:       strings.TrimSpace(in.Filter.TraceUser),
			TransactionCode: strings.TrimSpace(in.Filter.TransactionCode),
			Program:         strings.TrimSpace(in.Filter.Program),
			RFCFunction:     strings.TrimSpace(in.Filter.RFCFunction),
			URL:             strings.TrimSpace(in.Filter.URL),
			WorkProcessID:   strings.TrimSpace(in.Filter.WpID),
			StackTrace:      xmlBool(in.Props.StackTraceOn),
			AuthErrorsOnly:  xmlBool(in.Props.AuthErrorsOnly),
		})
	}
	return out, nil
}

// xmlBool reads the several ways SAP writes a flag.
func xmlBool(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "TRUE", "X", "1":
		return true
	}
	return false
}

// sqlTraceEntryXML is one entry of the Atom-feed shape.
type sqlTraceEntryXML struct {
	ID      string `xml:"id"`
	Title   string `xml:"title"`
	Updated string `xml:"updated"`
	Link    struct {
		Href string `xml:"href,attr"`
	} `xml:"link"`
	Author struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Content struct {
		Trace struct {
			TraceType   string `xml:"traceType,attr"`
			StartTime   string `xml:"startTime,attr"`
			EndTime     string `xml:"endTime,attr"`
			RecordCount string `xml:"recordCount,attr"`
			Size        string `xml:"size,attr"`
		} `xml:"trace"`
	} `xml:"content"`
}

// parseSQLTraceDirectory reads the directory document.
//
// Two shapes, and which one arrives is a property of the release. Older systems
// answer with an Atom feed of trace files. 7.58 answers with a single URI
// pointing at the STMC web application, and lists nothing at all — the traces
// are there, they are simply not offered through this resource.
//
// The distinction is the whole reason this returns a struct and not a slice. An
// empty slice from here would read as "this system has no traces", which is a
// claim about the system. What is true is a claim about the resource, and the
// caller is handed the URL so the answer is still useful.
func parseSQLTraceDirectory(data []byte) (*SQLTraceDirectory, error) {
	var feed struct {
		XMLName xml.Name           `xml:"feed"`
		Entries []sqlTraceEntryXML `xml:"entry"`
	}
	if err := xml.Unmarshal(data, &feed); err == nil {
		out := &SQLTraceDirectory{Entries: make([]SQLTraceEntry, 0, len(feed.Entries))}
		for _, entry := range feed.Entries {
			var recordCount int
			var size int64
			_, _ = fmt.Sscanf(entry.Content.Trace.RecordCount, "%d", &recordCount)
			_, _ = fmt.Sscanf(entry.Content.Trace.Size, "%d", &size)
			out.Entries = append(out.Entries, SQLTraceEntry{
				ID:          entry.ID,
				User:        entry.Author.Name,
				StartTime:   entry.Content.Trace.StartTime,
				EndTime:     entry.Content.Trace.EndTime,
				TraceType:   entry.Content.Trace.TraceType,
				RecordCount: recordCount,
				Size:        size,
				URI:         entry.Link.Href,
			})
		}
		return out, nil
	}

	var dir struct {
		XMLName xml.Name `xml:"traceDirectory"`
		URI     string   `xml:"uri"`
	}
	if err := xml.Unmarshal(data, &dir); err != nil {
		return nil, fmt.Errorf("parsing SQL trace directory: %w", err)
	}
	return &SQLTraceDirectory{
		Entries:     []SQLTraceEntry{},
		AnalysisURL: strings.TrimSpace(dir.URI),
	}, nil
}
