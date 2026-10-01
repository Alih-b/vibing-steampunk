package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// A class name reaches the class include and method helpers either raw
// (/DMO/CL_FLIGHT) or already escaped from a URL (%2FDMO%2FCL_FLIGHT). Each
// must be escaped exactly once: a second escape turns %2F into %252F and the
// request misses the class.
var namespacedClassNameCases = []struct {
	name, in, want string
}{
	{"raw namespaced", "/DMO/CL_FLIGHT", "%2FDMO%2FCL_FLIGHT"},
	{"escaped namespaced", "%2FDMO%2FCL_FLIGHT", "%2FDMO%2FCL_FLIGHT"},
	{"escaped lowercase", "%2fdmo%2fcl_flight", "%2FDMO%2FCL_FLIGHT"},
	{"plain", "ZCL_FOO", "ZCL_FOO"},
}

func TestGetClassIncludeSourceURL_EscapesOnce(t *testing.T) {
	for _, tc := range namespacedClassNameCases {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := GetClassIncludeSourceURL(tc.in, ClassIncludeTestClasses), "/sap/bc/adt/oo/classes/"+tc.want+"/includes/testclasses"; got != want {
				t.Errorf("include: %s, want %s", got, want)
			}
			if got, want := GetClassIncludeSourceURL(tc.in, ClassIncludeMain), "/sap/bc/adt/oo/classes/"+tc.want+"/source/main"; got != want {
				t.Errorf("main: %s, want %s", got, want)
			}
		})
	}
}

// escapedPathServer records the escaped path of every request so a double
// escape (%252F) is visible; the decoded r.URL.Path would hide it.
func escapedPathServer(t *testing.T, body string) (*Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.EscapedPath())
		mu.Unlock()
		w.Header().Set("X-CSRF-Token", "TOKEN")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	cfg := NewConfig(srv.URL, "TESTUSER", "secret")
	return NewClientWithTransport(cfg, NewTransport(cfg)), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

func TestUpdateClassInclude_EscapesNameOnce(t *testing.T) {
	for _, tc := range namespacedClassNameCases {
		t.Run(tc.name, func(t *testing.T) {
			client, paths := escapedPathServer(t, "")
			if err := client.UpdateClassInclude(context.Background(), tc.in, ClassIncludeTestClasses, "* src", "LOCK", ""); err != nil {
				t.Fatalf("UpdateClassInclude: %v", err)
			}
			want := "PUT /sap/bc/adt/oo/classes/" + tc.want + "/includes/testclasses"
			var found bool
			for _, p := range paths() {
				if p == want {
					found = true
				}
			}
			if !found {
				t.Errorf("no %q among requests %v", want, paths())
			}
		})
	}
}

func TestGetClassMethods_EscapesNameOnce(t *testing.T) {
	for _, tc := range namespacedClassNameCases {
		t.Run(tc.name, func(t *testing.T) {
			client, paths := escapedPathServer(t, `<?xml version="1.0" encoding="UTF-8"?><abapsource:objectStructureElement xmlns:abapsource="http://www.sap.com/adt/abapsource" xmlns:adtcore="http://www.sap.com/adt/core"/>`)
			if _, err := client.GetClassMethods(context.Background(), tc.in); err != nil {
				t.Fatalf("GetClassMethods: %v", err)
			}
			want := "GET /sap/bc/adt/oo/classes/" + tc.want + "/objectstructure"
			if got := paths(); len(got) == 0 || got[len(got)-1] != want {
				t.Errorf("requests %v, want last %q", got, want)
			}
		})
	}
}

func TestGetClassIncludeURL_EscapesOnce(t *testing.T) {
	for _, tc := range namespacedClassNameCases {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := GetClassIncludeURL(tc.in, ClassIncludeTestClasses), "/sap/bc/adt/oo/classes/"+tc.want+"/includes/testclasses"; got != want {
				t.Errorf("include: %s, want %s", got, want)
			}
			if got, want := GetClassIncludeURL(tc.in, ClassIncludeMain), "/sap/bc/adt/oo/classes/"+tc.want+"/source/main"; got != want {
				t.Errorf("main: %s, want %s", got, want)
			}
		})
	}
}

func TestGetClassObjectStructure_EscapesNameOnce(t *testing.T) {
	for _, tc := range namespacedClassNameCases {
		t.Run(tc.name, func(t *testing.T) {
			client, paths := escapedPathServer(t, `<?xml version="1.0" encoding="UTF-8"?><abapsource:objectStructureElement xmlns:abapsource="http://www.sap.com/adt/abapsource" xmlns:adtcore="http://www.sap.com/adt/core"/>`)
			if _, err := client.GetClassObjectStructure(context.Background(), tc.in); err != nil {
				t.Fatalf("GetClassObjectStructure: %v", err)
			}
			want := "GET /sap/bc/adt/oo/classes/" + tc.want + "/objectstructure"
			if got := paths(); len(got) == 0 || got[len(got)-1] != want {
				t.Errorf("requests %v, want last %q", got, want)
			}
		})
	}
}

func TestCreateTestInclude_EscapesNameOnce(t *testing.T) {
	for _, tc := range namespacedClassNameCases {
		t.Run(tc.name, func(t *testing.T) {
			client, paths := escapedPathServer(t, "")
			if err := client.CreateTestInclude(context.Background(), tc.in, "LOCK", ""); err != nil {
				t.Fatalf("CreateTestInclude: %v", err)
			}
			want := "POST /sap/bc/adt/oo/classes/" + tc.want + "/includes"
			var found bool
			for _, p := range paths() {
				if p == want {
					found = true
				}
			}
			if !found {
				t.Errorf("no %q among requests %v", want, paths())
			}
		})
	}
}
