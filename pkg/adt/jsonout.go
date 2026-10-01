package adt

import (
	"bytes"
	"encoding/json"
)

// IndentJSON is json.MarshalIndent without HTML escaping.
//
// ABAP Unit titles and returned values are full of <, > and &: "Exception
// Error <COMPUTE_INT_ZERODIVIDE>", "Include: <ZCL_X====CCAU> Line: <21>". The
// default encoder writes each as <, which a reader has to decode and a
// model pays for three times over, and which turns a value handed back by
// execute_abap into something other than what the code returned.
func IndentJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
