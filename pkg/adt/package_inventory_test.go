package adt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// previewXML renders data preview rows for the given columns.
func previewXML(cols []string, rows ...[]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">`)
	for i, c := range cols {
		fmt.Fprintf(&b, `<dataPreview:columns><dataPreview:metadata dataPreview:name="%s"/><dataPreview:dataSet>`, c)
		for _, r := range rows {
			fmt.Fprintf(&b, `<dataPreview:data>%s</dataPreview:data>`, xmlEscapeTest(r[i]))
		}
		b.WriteString(`</dataPreview:dataSet></dataPreview:columns>`)
	}
	b.WriteString(`</dataPreview:tableData>`)
	return b.String()
}

func xmlEscapeTest(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

const repoXML = `<?xml version="1.0" encoding="utf-16"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><REPO>` +
	`<URL>%s</URL><BRANCH_NAME>refs/heads/main</BRANCH_NAME><PACKAGE>%s</PACKAGE><CREATED_BY>TESTUSER</CREATED_BY><OFFLINE>%s</OFFLINE>` +
	`<DOT_ABAPGIT><NAME/></DOT_ABAPGIT></REPO></asx:values></asx:abap>`

const inventoryTreeXML = `<?xml version="1.0" encoding="utf-8"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA><TREE_CONTENT>` +
	`<SEU_ADT_REPOSITORY_OBJ_NODE><OBJECT_TYPE>CLAS/OC</OBJECT_TYPE><OBJECT_NAME>ZCL_DEMO_A</OBJECT_NAME><OBJECT_URI>/sap/bc/adt/oo/classes/zcl_demo_a</OBJECT_URI><DESCRIPTION>Demo A</DESCRIPTION></SEU_ADT_REPOSITORY_OBJ_NODE>` +
	`<SEU_ADT_REPOSITORY_OBJ_NODE><OBJECT_TYPE>DEVC/K</OBJECT_TYPE><OBJECT_NAME>$ZDEMO_SUB</OBJECT_NAME></SEU_ADT_REPOSITORY_OBJ_NODE>` +
	`</TREE_CONTENT></DATA></asx:values></asx:abap>`

// inventorySAP answers the data preview and the package tree, and records
// what it was asked.
type inventorySAP struct {
	mu        sync.Mutex
	queries   []string
	paths     []string
	noAbapGit bool
	// stallOn, when set, is a table whose SELECT is not answered: onStall
	// runs (it ends the caller's context) and the request waits for its
	// context to end.
	stallOn string
	onStall func()
}

func (f *inventorySAP) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	sql := strings.ToLower(strings.Join(strings.Fields(string(body)), " "))
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	if strings.Contains(r.URL.Path, "freestyle") {
		f.queries = append(f.queries, sql)
	}
	f.mu.Unlock()
	if f.stallOn != "" && strings.Contains(sql, "from "+f.stallOn) {
		f.onStall()
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		return
	}
	w.Header().Set("X-CSRF-Token", "t")
	w.Header().Set("Content-Type", "application/xml")
	switch {
	case strings.Contains(r.URL.Path, "nodestructure"):
		_, _ = w.Write([]byte(inventoryTreeXML))
	case strings.Contains(sql, "from tadir"):
		_, _ = w.Write([]byte(previewXML([]string{"OBJECT", "OBJ_NAME", "AUTHOR", "CREATED_ON"},
			[]string{"PROG", "ZDEMO_REPORT", "TESTUSER", "20260105"},
			[]string{"CLAS", "ZCL_DEMO_A", "TESTUSER", "20251218"},
			[]string{"DEVC", "$ZDEMO", "TESTUSER", "00000000"})))
	case strings.Contains(sql, "from tdevc"):
		_, _ = w.Write([]byte(previewXML([]string{"DEVCLASS", "AS4USER", "CREATED_ON"},
			[]string{"$ZDEMO_SUB", "TESTUSER", "20260101"})))
	case strings.Contains(sql, "from dd02l"):
		if f.noAbapGit {
			_, _ = w.Write([]byte(previewXML([]string{"TABNAME"})))
			return
		}
		_, _ = w.Write([]byte(previewXML([]string{"TABNAME"}, []string{"ZABAPGIT"})))
	case strings.Contains(sql, "from zabapgit"):
		_, _ = w.Write([]byte(previewXML([]string{"VALUE", "DATA_STR"},
			[]string{"000000000001", fmt.Sprintf(repoXML, "https://git.example/demo.git", "$ZDEMO", "")},
			[]string{"000000000002", fmt.Sprintf(repoXML, "", "$ZOTHER", "X")})))
	default:
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><root/>`))
	}
}

func newInventoryClient(t *testing.T, f *inventorySAP, opts ...Option) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "u", "p", opts...)
}

func TestPackageInventoryThroughSQL(t *testing.T) {
	f := &inventorySAP{}
	c := newInventoryClient(t, f)
	inv, err := c.PackageInventory(context.Background(), "$zdemo")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Package != "$ZDEMO" || len(inv.Objects) != 3 {
		t.Fatalf("got %+v", inv)
	}
	// sorted by type, then name
	if o := inv.Objects[0]; o.Type != "CLAS" || o.Name != "ZCL_DEMO_A" || o.Author != "TESTUSER" || o.CreatedOn != "2025-12-18" {
		t.Fatalf("first object %+v", o)
	}
	if inv.Objects[1].Type != "DEVC" || inv.Objects[1].CreatedOn != "" {
		t.Fatalf("an initial date must be empty: %+v", inv.Objects[1])
	}
	if len(inv.Subpackages) != 1 || inv.Subpackages[0] != (InventorySubpackage{Name: "$ZDEMO_SUB", Responsible: "TESTUSER", CreatedOn: "2026-01-01"}) {
		t.Fatalf("subpackages %+v", inv.Subpackages)
	}
	if len(inv.AbapGitRepos) != 1 || inv.AbapGitRepos[0].Key != "000000000001" ||
		inv.AbapGitRepos[0].URL != "https://git.example/demo.git" || inv.AbapGitRepos[0].Offline {
		t.Fatalf("abapGit repos %+v", inv.AbapGitRepos)
	}
	if len(inv.Skipped) != 0 {
		t.Fatalf("nothing should be skipped: %v", inv.Skipped)
	}
	for _, q := range f.queries {
		if !strings.HasPrefix(q, "select ") {
			t.Fatalf("only SELECTs may be sent, got %q", q)
		}
	}
	if !strings.Contains(f.queries[0], "devclass = '$zdemo'") {
		t.Fatalf("TADIR query %q", f.queries[0])
	}
}

func TestPackageInventoryWithoutAbapGit(t *testing.T) {
	f := &inventorySAP{noAbapGit: true}
	inv, err := newInventoryClient(t, f).PackageInventory(context.Background(), "$ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if inv.AbapGitRepos == nil || len(inv.AbapGitRepos) != 0 {
		t.Fatalf("checked and none: want an empty list, got %#v", inv.AbapGitRepos)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "zabapgit where") || strings.Contains(q, "from zabapgit") {
			t.Fatalf("ZABAPGIT must not be read when the table does not exist: %q", q)
		}
	}
	if !strings.Contains(strings.Join(inv.Notes, "\n"), "not installed") {
		t.Fatalf("notes %v", inv.Notes)
	}
}

// With --block-free-sql only the ADT package contents are read, and the
// answer names what that leaves out.
func TestPackageInventoryBlockFreeSQL(t *testing.T) {
	f := &inventorySAP{}
	inv, err := newInventoryClient(t, f, WithBlockFreeSQL()).PackageInventory(context.Background(), "$ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.queries) != 0 {
		t.Fatalf("free SQL is blocked, yet sent: %v", f.queries)
	}
	if len(inv.Objects) != 1 || inv.Objects[0].Type != "CLAS" || inv.Objects[0].Name != "ZCL_DEMO_A" {
		t.Fatalf("objects %+v", inv.Objects)
	}
	if len(inv.Subpackages) != 1 || inv.Subpackages[0].Name != "$ZDEMO_SUB" {
		t.Fatalf("subpackages %+v", inv.Subpackages)
	}
	if inv.AbapGitRepos != nil {
		t.Fatalf("abapGit could not be checked; want nil, got %+v", inv.AbapGitRepos)
	}
	skipped := strings.Join(inv.Skipped, "\n")
	for _, want := range []string{"TADIR", "TDEVC", "ZABAPGIT", "block-free-sql"} {
		if !strings.Contains(skipped, want) {
			t.Fatalf("skipped must mention %s: %v", want, inv.Skipped)
		}
	}
}

func TestPackageInventoryRefusesOddNames(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "u", "p")
	for _, bad := range []string{"", "$Z' OR '1'='1", "Z PKG", strings.Repeat("Z", 31)} {
		if _, err := c.PackageInventory(context.Background(), bad); err == nil {
			t.Fatalf("%q: want refused", bad)
		}
	}
}

// objects_truncated means rows were left out: one more than the limit is
// read, so a package holding exactly the limit is complete.
func TestPackageInventoryTruncatedOnlyBeyondTheLimit(t *testing.T) {
	saved := inventoryMaxObjects
	t.Cleanup(func() { inventoryMaxObjects = saved })

	inventoryMaxObjects = 3 // the fixture's TADIR holds exactly 3
	inv, err := newInventoryClient(t, &inventorySAP{}).PackageInventory(context.Background(), "$ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if inv.ObjectsTruncated || len(inv.Objects) != 3 {
		t.Fatalf("exactly the limit: truncated=%v, %d objects", inv.ObjectsTruncated, len(inv.Objects))
	}

	inventoryMaxObjects = 2
	inv, err = newInventoryClient(t, &inventorySAP{}).PackageInventory(context.Background(), "$ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if !inv.ObjectsTruncated || len(inv.Objects) != 2 {
		t.Fatalf("beyond the limit: truncated=%v, %d objects", inv.ObjectsTruncated, len(inv.Objects))
	}
}

// Deleted TADIR entries are not live objects, and the ADT fallback never lists
// them, so the SQL path filters them out too.
func TestPackageInventorySkipsDeletedTADIREntries(t *testing.T) {
	f := &inventorySAP{}
	if _, err := newInventoryClient(t, f).PackageInventory(context.Background(), "$ZDEMO"); err != nil {
		t.Fatal(err)
	}
	for _, q := range f.queries {
		if strings.Contains(q, "from tadir") {
			if !strings.Contains(q, "delflag <> 'x'") {
				t.Fatalf("TADIR query keeps deleted entries: %s", q)
			}
			return
		}
	}
	t.Fatal("no TADIR query sent")
}

func TestPackageInventorySubpackagesTruncatedOnlyBeyondTheLimit(t *testing.T) {
	saved := inventoryMaxSubpackages
	t.Cleanup(func() { inventoryMaxSubpackages = saved })

	inventoryMaxSubpackages = 1 // the fixture's TDEVC holds exactly 1
	inv, err := newInventoryClient(t, &inventorySAP{}).PackageInventory(context.Background(), "$ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if inv.SubpackagesTruncated || len(inv.Subpackages) != 1 {
		t.Fatalf("exactly the limit: truncated=%v, %d subpackages", inv.SubpackagesTruncated, len(inv.Subpackages))
	}

	inventoryMaxSubpackages = 0
	inv, err = newInventoryClient(t, &inventorySAP{}).PackageInventory(context.Background(), "$ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if !inv.SubpackagesTruncated || len(inv.Subpackages) != 0 {
		t.Fatalf("beyond the limit: truncated=%v, %d subpackages", inv.SubpackagesTruncated, len(inv.Subpackages))
	}
}

// ZABAPGIT cannot be filtered by package in SQL. When the read is cut off, a
// repository found is still reported, but "none found" is not a definite
// empty list: the package's repository may be among the rows not read.
func TestPackageInventoryAbapGitScanCutOff(t *testing.T) {
	saved := inventoryMaxRepos
	t.Cleanup(func() { inventoryMaxRepos = saved })
	inventoryMaxRepos = 1 // the fixture holds 2: $ZDEMO's first, then $ZOTHER's

	inv, err := newInventoryClient(t, &inventorySAP{}).PackageInventory(context.Background(), "$ZDEMO")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.AbapGitRepos) != 1 || !strings.Contains(strings.Join(inv.Notes, "\n"), "only the first 1 were scanned") {
		t.Fatalf("found within the cut: repos=%v notes=%v", inv.AbapGitRepos, inv.Notes)
	}

	inv, err = newInventoryClient(t, &inventorySAP{}).PackageInventory(context.Background(), "$ZOTHER")
	if err != nil {
		t.Fatal(err)
	}
	if inv.AbapGitRepos != nil {
		t.Fatalf("not found in a cut-off scan must be unknown (nil), got %v", inv.AbapGitRepos)
	}
}

// A data source that fails is worked around; a context that ends is not a
// failing data source. Whichever SELECT the cancellation or deadline lands
// in, the inventory returns the context's error, not a partial inventory.
func TestPackageInventoryReturnsCancellation(t *testing.T) {
	for _, table := range []string{"tadir", "tdevc", "dd02l", "zabapgit"} {
		t.Run(table+"/canceled", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &inventorySAP{stallOn: table, onStall: cancel}
			inv, err := newInventoryClient(t, f).PackageInventory(ctx, "$ZDEMO")
			if !errors.Is(err, context.Canceled) || inv != nil {
				t.Fatalf("want context.Canceled and no inventory, got %+v, %v", inv, err)
			}
		})
		t.Run(table+"/deadline", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			f := &inventorySAP{stallOn: table, onStall: func() {}}
			inv, err := newInventoryClient(t, f).PackageInventory(ctx, "$ZDEMO")
			if !errors.Is(err, context.DeadlineExceeded) || inv != nil {
				t.Fatalf("want context.DeadlineExceeded and no inventory, got %+v, %v", inv, err)
			}
		})
	}
}
