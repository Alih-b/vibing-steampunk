package adt

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The shape an S/4HANA 7.58 system answers <group>/enhancements/options with,
// names replaced.
const testEnhancementOptionsXML = `<?xml version="1.0" encoding="utf-8"?><enho:enhancementOption xmlns:enho="http://www.sap.com/adt/enhancementOptions/enho">` +
	`<enho:option enhocore:full_name="\PR:SAPLZDEMO\DA:ZDEMO_S\SE:END\EI" enho:fullDescription="Struct. ZDEMO_S, End" enho:mode="static" xmlns:enhocore="http://www.sap.com/abapsource/enhancementscore">` +
	`<atom:link href="/sap/bc/adt/functions/groups/zdemo/includes/lzdemotop/source/main#start=18,0" rel="http://www.sap.com/adt/relations/source" type="text/plain" xmlns:atom="http://www.w3.org/2005/Atom"/></enho:option>` +
	`<enho:option enhocore:full_name="\FU:Z_DEMO\SE:BEGIN\EI" enho:fullDescription="Function Module Z_DEMO, Start" enho:mode="any" xmlns:enhocore="http://www.sap.com/abapsource/enhancementscore">` +
	`<atom:link href="/sap/bc/adt/functions/groups/zdemo/fmodules/z_demo/source/main#start=1,25" rel="http://www.sap.com/adt/relations/source" type="text/plain" xmlns:atom="http://www.w3.org/2005/Atom"/></enho:option>` +
	`</enho:enhancementOption>`

func TestParseEnhancementOptions(t *testing.T) {
	opts, err := parseEnhancementOptions([]byte(testEnhancementOptionsXML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(opts) != 2 {
		t.Fatalf("options = %+v", opts)
	}
	if opts[1].FullName != `\FU:Z_DEMO\SE:BEGIN\EI` || opts[1].Mode != "any" || opts[1].Description != "Function Module Z_DEMO, Start" ||
		!strings.Contains(opts[1].Source, "fmodules/z_demo/source/main") {
		t.Errorf("option = %+v", opts[1])
	}
	if opts[0].Mode != "static" {
		t.Errorf("mode = %q, want static", opts[0].Mode)
	}
}

func TestEnhancedObjectFor(t *testing.T) {
	for in, want := range map[string]enhancedObject{
		"/sap/bc/adt/functions/groups/zdemo":        {URI: "/sap/bc/adt/functions/groups/zdemo", Type: "FUGR/F", Name: "ZDEMO", Program: "SAPLZDEMO"},
		"/sap/bc/adt/functions/groups/%2fns%2fdemo": {URI: "/sap/bc/adt/functions/groups/%2fns%2fdemo", Type: "FUGR/F", Name: "/NS/DEMO", Program: "/NS/SAPLDEMO"},
		"/sap/bc/adt/programs/programs/zreport/":    {URI: "/sap/bc/adt/programs/programs/zreport", Type: "PROG/P", Name: "ZREPORT", Program: "ZREPORT"},
		"/sap/bc/adt/oo/classes/zcl_demo":           {URI: "/sap/bc/adt/oo/classes/zcl_demo", Type: "CLAS/OC", Name: "ZCL_DEMO", Program: "ZCL_DEMO======================CP"},
	} {
		got, err := enhancedObjectFor(in)
		if err != nil || got != want {
			t.Errorf("%s: %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "/sap/bc/adt/programs/includes/lzdemotop", "/sap/bc/adt/functions/groups/zdemo/fmodules/z_demo",
		// a collection, and subresources of an object, are not the object
		"/sap/bc/adt/programs/programs/", "/sap/bc/adt/functions/groups/",
		"/sap/bc/adt/programs/programs/zreport/source/main", "/sap/bc/adt/oo/classes/zcl_demo/includes/testclasses"} {
		if _, err := enhancedObjectFor(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// The body carries what the server insists on, escaped, and the plug-in's mode
// follows the option: static stays static, everything else is dynamic.
func TestSourceCodePluginBody(t *testing.T) {
	target, _ := enhancedObjectFor("/sap/bc/adt/functions/groups/zdemo")
	body := sourceCodePluginBody(SourceCodePluginOptions{
		Name: "ZENH_DEMO", Description: `Demo "A" & <B>`, Package: "ZPKG", Option: `\FU:Z_DEMO\SE:BEGIN\EI`, Mode: "any",
	}, target, "DE")

	var doc struct {
		Name    string `xml:"name,attr"`
		Type    string `xml:"type,attr"`
		Desc    string `xml:"description,attr"`
		Package struct {
			Name string `xml:"name,attr"`
		} `xml:"packageRef"`
		Common struct {
			ToolType string `xml:"toolType,attr"`
		} `xml:"contentCommon"`
		Specific struct {
			Hook struct {
				Object struct{ URI, Type, Name string } `xml:"enhancedObject"`
				Impl   struct {
					ID       string `xml:"id,attr"`
					Program  string `xml:"programname,attr"`
					Mode     string `xml:"enhmode,attr"`
					FullName string `xml:"full_name,attr"`
				} `xml:"hookImplementation"`
			} `xml:"hookTechnology"`
		} `xml:"contentSpecific"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("body is not XML: %v\n%s", err, body)
	}
	if doc.Name != "ZENH_DEMO" || doc.Type != "ENHO/XHH" || doc.Desc != `Demo "A" & <B>` || doc.Package.Name != "ZPKG" || doc.Common.ToolType != "HOOK_IMPL" {
		t.Errorf("header = %+v", doc)
	}
	impl := doc.Specific.Hook.Impl
	if impl.ID != "1" || impl.Program != "SAPLZDEMO" || impl.Mode != "D" || impl.FullName != `\FU:Z_DEMO\SE:BEGIN\EI` {
		t.Errorf("hook = %+v", impl)
	}
	if !strings.Contains(body, `adtcore:uri="/sap/bc/adt/functions/groups/zdemo"`) || !strings.Contains(body, `adtcore:type="FUGR/F"`) {
		t.Errorf("enhanced object missing: %s", body)
	}

	static := sourceCodePluginBody(SourceCodePluginOptions{Name: "Z", Description: "d", Package: "P", Option: "o", Mode: "static"}, target, "")
	if !strings.Contains(static, `enho:enhmode="S"`) {
		t.Errorf("a static option must make a static plug-in: %s", static)
	}
}

func TestCreateSourceCodePlugin_PostsToTheCollection(t *testing.T) {
	var mu sync.Mutex
	var posted struct{ path, query, contentType, body string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-csrf-token", "TOKEN")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sap/bc/adt/enhancements/enhoxhh":
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			posted.path, posted.query, posted.contentType, posted.body = r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), string(b)
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusOK) // package lookup, CSRF probe
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "TESTUSER", "pw", WithLanguage("DE"))

	u, err := c.CreateSourceCodePlugin(context.Background(), SourceCodePluginOptions{
		Name: "zenh_demo", Description: "Demo", Package: "$tmp",
		ObjectURL: "/sap/bc/adt/functions/groups/zdemo", Option: `\FU:Z_DEMO\SE:BEGIN\EI`,
	})
	if err != nil {
		t.Fatalf("CreateSourceCodePlugin: %v", err)
	}
	if u != "/sap/bc/adt/enhancements/enhoxhh/zenh_demo" {
		t.Errorf("url = %s", u)
	}
	mu.Lock()
	defer mu.Unlock()
	if posted.contentType != "application/vnd.sap.adt.enh.enhoxhh.v3+xml" {
		t.Errorf("content type = %q", posted.contentType)
	}
	if !strings.Contains(posted.body, `adtcore:name="ZENH_DEMO"`) || !strings.Contains(posted.body, `adtcore:name="$TMP"`) {
		t.Errorf("body = %s", posted.body)
	}
	if strings.Contains(posted.query, "corrNr") {
		t.Errorf("a local object needs no corrNr, got %q", posted.query)
	}
}

func TestCreateSourceCodePlugin_RefusesIncompleteInput(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "TESTUSER", "pw")
	for name, opts := range map[string]SourceCodePluginOptions{
		"no option":      {Name: "Z", Description: "d", Package: "$TMP", ObjectURL: "/sap/bc/adt/functions/groups/zdemo"},
		"no description": {Name: "Z", Package: "$TMP", ObjectURL: "/sap/bc/adt/functions/groups/zdemo", Option: "o"},
		"an include":     {Name: "Z", Description: "d", Package: "$TMP", ObjectURL: "/sap/bc/adt/programs/includes/zinc", Option: "o"},
	} {
		if _, err := c.CreateSourceCodePlugin(context.Background(), opts); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
