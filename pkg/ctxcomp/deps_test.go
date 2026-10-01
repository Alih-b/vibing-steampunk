package ctxcomp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func embeddedPath(name string) string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "embedded", "abap", name)
}

func readEmbedded(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(embeddedPath(name))
	if err != nil {
		t.Fatalf("read embedded %s: %v", name, err)
	}
	return string(data)
}

func TestExtractDependencies_APCHandler(t *testing.T) {
	src := readEmbedded(t, "zcl_vsp_apc_handler.clas.abap")
	deps := ExtractDependencies(src)

	found := make(map[string]DependencyKind)
	for _, d := range deps {
		found[d.Name] = d.Kind
	}

	// Should find these dependencies
	expect := map[string]DependencyKind{
		"CL_APC_WSP_EXT_STATEFUL_BASE": KindClass,
		"IF_APC_WSP_EXTENSION":         KindInterface,
		"IF_APC_WSP_SERVER_CONTEXT":    KindInterface,
		"IF_APC_WSP_MESSAGE_MANAGER":   KindInterface,
		"ZIF_VSP_SERVICE":              KindInterface,
		"ZCL_VSP_UTILS":                KindClass,
		"ZCL_VSP_RFC_SERVICE":          KindClass,
		"ZCL_VSP_DEBUG_SERVICE":        KindClass,
		"ZCL_VSP_AMDP_SERVICE":         KindClass,
		"ZCL_VSP_REPORT_SERVICE":       KindClass,
	}
	// The git and transport services are created by name at runtime (they
	// are optional), so the source names neither as a type.
	for _, name := range []string{"ZCL_VSP_GIT_SERVICE", "ZCL_VSP_TRANSPORT_SERVICE"} {
		if _, ok := found[name]; ok {
			t.Errorf("%s is a static dependency of the APC handler; it must stay optional", name)
		}
	}

	for name, kind := range expect {
		gotKind, ok := found[name]
		if !ok {
			t.Errorf("expected dependency %s not found", name)
			continue
		}
		if gotKind != kind {
			t.Errorf("dependency %s: got kind %s, want %s", name, gotKind, kind)
		}
	}
}

func TestExtractDependencies_DebugService(t *testing.T) {
	src := readEmbedded(t, "zcl_vsp_debug_service.clas.abap")
	deps := ExtractDependencies(src)

	found := make(map[string]bool)
	for _, d := range deps {
		found[d.Name] = true
	}

	// Should find the interface it implements
	if !found["ZIF_VSP_SERVICE"] {
		t.Error("expected ZIF_VSP_SERVICE dependency")
	}

	// Should find TPDAPI references
	if !found["IF_TPDAPI_SERVICE"] {
		t.Error("expected IF_TPDAPI_SERVICE dependency")
	}
	if !found["IF_TPDAPI_SESSION"] {
		t.Error("expected IF_TPDAPI_SESSION dependency")
	}
	if !found["IF_TPDAPI_BP"] {
		t.Error("expected IF_TPDAPI_BP dependency")
	}
}

func TestExtractDependencies_Interface(t *testing.T) {
	src := readEmbedded(t, "zif_vsp_service.intf.abap")
	deps := ExtractDependencies(src)

	// An interface with no external refs should have minimal deps
	for _, d := range deps {
		if d.Name == "ZIF_VSP_SERVICE" {
			t.Error("interface should not reference itself")
		}
	}
}

func TestExtractDependencies_InlinePatterns(t *testing.T) {
	tests := []struct {
		name   string
		source string
		expect []Dependency
	}{
		{
			name:   "TYPE REF TO",
			source: "DATA lo_obj TYPE REF TO zcl_my_class.",
			expect: []Dependency{{Name: "ZCL_MY_CLASS", Kind: KindClass}},
		},
		{
			name:   "NEW constructor",
			source: "DATA(lo_obj) = NEW zcl_factory( ).",
			expect: []Dependency{{Name: "ZCL_FACTORY", Kind: KindClass}},
		},
		{
			name:   "Static call",
			source: "zcl_helper=>do_something( ).",
			expect: []Dependency{{Name: "ZCL_HELPER", Kind: KindClass}},
		},
		{
			name:   "Interface method",
			source: "lo_obj->zif_payment~authorize( ).",
			expect: []Dependency{{Name: "ZIF_PAYMENT", Kind: KindInterface}},
		},
		{
			name:   "INHERITING FROM",
			source: "CLASS zcl_child DEFINITION INHERITING FROM zcl_parent.",
			expect: []Dependency{{Name: "ZCL_PARENT", Kind: KindClass}},
		},
		{
			name:   "INTERFACES",
			source: "INTERFACES zif_serializable.",
			expect: []Dependency{{Name: "ZIF_SERIALIZABLE", Kind: KindInterface}},
		},
		{
			name:   "CALL FUNCTION",
			source: "CALL FUNCTION 'BAPI_USER_GET_DETAIL'.",
			expect: []Dependency{{Name: "BAPI_USER_GET_DETAIL", Kind: KindFunction}},
		},
		{
			name:   "CAST",
			source: "DATA(lo_intf) = CAST zif_processor( lo_obj ).",
			expect: []Dependency{{Name: "ZIF_PROCESSOR", Kind: KindInterface}},
		},
		{
			name:   "namespace",
			source: "DATA lo_obj TYPE REF TO /dmf/cl_something.",
			expect: []Dependency{{Name: "/DMF/CL_SOMETHING", Kind: KindClass}},
		},
		{
			name:   "skip builtin",
			source: "DATA lv_str TYPE string.\nDATA lv_bool TYPE abap_bool.",
			expect: []Dependency{},
		},
		{
			name:   "skip CL_ABAP standard",
			source: "DATA lo_obj TYPE REF TO cl_abap_typedescr.",
			expect: []Dependency{},
		},
		{
			name:   "skip comment lines",
			source: "* DATA lo_obj TYPE REF TO zcl_hidden.\n\" DATA lo_obj TYPE REF TO zcl_also_hidden.",
			expect: []Dependency{},
		},
		{
			name:   "multiple deps on one line",
			source: "lo_result = zcl_converter=>convert( CAST zif_input( lo_data ) ).",
			expect: []Dependency{
				{Name: "ZCL_CONVERTER", Kind: KindClass},
				{Name: "ZIF_INPUT", Kind: KindInterface},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := ExtractDependencies(tt.source)

			if len(deps) != len(tt.expect) {
				names := make([]string, len(deps))
				for i, d := range deps {
					names[i] = d.Name
				}
				t.Fatalf("got %d deps %v, want %d", len(deps), names, len(tt.expect))
			}

			found := make(map[string]DependencyKind)
			for _, d := range deps {
				found[d.Name] = d.Kind
			}

			for _, exp := range tt.expect {
				kind, ok := found[exp.Name]
				if !ok {
					t.Errorf("expected dep %s not found", exp.Name)
				} else if kind != exp.Kind {
					t.Errorf("dep %s: got kind %s, want %s", exp.Name, kind, exp.Kind)
				}
			}
		})
	}
}

func TestExtractDependencies_Deduplication(t *testing.T) {
	src := `DATA lo1 TYPE REF TO zcl_foo.
DATA lo2 TYPE REF TO zcl_foo.
zcl_foo=>bar( ).
DATA(lo3) = NEW zcl_foo( ).`

	deps := ExtractDependencies(src)
	count := 0
	for _, d := range deps {
		if d.Name == "ZCL_FOO" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected ZCL_FOO once, got %d times", count)
	}
}

func TestInferKind(t *testing.T) {
	tests := []struct {
		name string
		want DependencyKind
	}{
		{"zif_payment", KindInterface},
		{"yif_custom", KindInterface},
		{"if_apc_wsp_ext", KindInterface},
		{"zcl_order", KindClass},
		{"cl_gui_alv", KindClass},
		{"/dmf/if_processor", KindInterface},
		{"/dmf/cl_handler", KindClass},
	}

	for _, tt := range tests {
		got := inferKind(tt.name)
		if got != tt.want {
			t.Errorf("inferKind(%q) = %s, want %s", tt.name, got, tt.want)
		}
	}
}

// The context appended to a read is built from this reader, and it could see
// NEW, CAST, TYPE REF TO and the functional static call while missing the
// classic instantiation. A class written before 7.40 therefore got a thinner
// dependency context than the same class written after it — silently, and on
// exactly the older code where a reader most needs the help.
func TestCreateObjectNamesItsClass(t *testing.T) {
	const src = `CLASS zcl_probe IMPLEMENTATION.
  METHOD run.
    CREATE OBJECT lo_worker TYPE zcl_demo_worker.
    CREATE OBJECT lo_other TYPE ZCL_DEMO_OTHER EXPORTING iv_x = 1.
  ENDMETHOD.
ENDCLASS.`
	found := map[string]bool{}
	for _, d := range ExtractDependencies(src) {
		found[d.Name] = true
	}
	for _, want := range []string{"ZCL_DEMO_WORKER", "ZCL_DEMO_OTHER"} {
		if !found[want] {
			t.Errorf("CREATE OBJECT ... TYPE %s produced no dependency; found %v", want, keysOf(found))
		}
	}
	// The receiving variable is not a dependency. Naming it would put an object
	// that does not exist into the context, which is worse than a thin one.
	for _, invented := range []string{"LO_WORKER", "LO_OTHER"} {
		if found[invented] {
			t.Errorf("the receiving variable %s was reported as a dependency", invented)
		}
	}
}

// The bare form names no class on the line — its type comes from the variable's
// own declaration, which TYPE REF TO already covers. Inventing one would be the
// worse failure.
func TestBareCreateObjectInventsNothing(t *testing.T) {
	const src = `CLASS zcl_probe IMPLEMENTATION.
  METHOD run.
    DATA lo_worker TYPE REF TO zcl_demo_worker.
    CREATE OBJECT lo_worker.
  ENDMETHOD.
ENDCLASS.`
	found := map[string]bool{}
	for _, d := range ExtractDependencies(src) {
		found[d.Name] = true
	}
	if !found["ZCL_DEMO_WORKER"] {
		t.Errorf("the declaration's own type was lost; found %v", keysOf(found))
	}
	if found["LO_WORKER"] {
		t.Error("the variable was reported as a dependency")
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// CREATE OBJECT lo_x TYPE (lv_class) takes its class from a variable at
// runtime. The name in the parentheses is that variable, not a class, and
// reporting it as a dependency would put an object that does not exist into the
// context a reader is given — the invention class, which is worse than the gap
// this pattern was added to close. It is common: a single standard package has
// dozens of these lines.
func TestDynamicCreateObjectInventsNoClass(t *testing.T) {
	const src = `CLASS zcl_probe IMPLEMENTATION.
  METHOD run.
    CREATE OBJECT lo_expression TYPE (lv_class).
    CREATE OBJECT mo_maintain TYPE (iv_class).
    CREATE OBJECT lo_real TYPE zcl_demo_real.
  ENDMETHOD.
ENDCLASS.`
	found := map[string]bool{}
	for _, d := range ExtractDependencies(src) {
		found[d.Name] = true
	}
	for _, invented := range []string{"LV_CLASS", "IV_CLASS"} {
		if found[invented] {
			t.Errorf("%s is a variable holding a class name at runtime; it was reported as a class", invented)
		}
	}
	// And the static line beside them must still be found, so the guard is not
	// a blanket refusal.
	if !found["ZCL_DEMO_REAL"] {
		t.Errorf("the static CREATE OBJECT was lost along with the dynamic ones; found %v", keysOf(found))
	}
}
