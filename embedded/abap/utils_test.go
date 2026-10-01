package embedded

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// extract_param_int converts a run of digits from the request to TYPE i; ten
// or more digits overflow it and dump the WebSocket session. The method must
// refuse long values before the conversion.
func TestExtractParamIntGuardsOverflow(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "src", "zcl_vsp_utils.clas.abap"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ToUpper(strings.Join(methodStatements(abapStatements(string(b)), "EXTRACT_PARAM_INT"), "\n"))
	guard := regexp.MustCompile(`IF STRLEN\( LV_STR \) > 9\nRV_VALUE = -1\nELSE\nRV_VALUE = LV_STR`)
	if !guard.MatchString(body) {
		t.Errorf("extract_param_int converts without a length guard:\n%s", body)
	}
}
