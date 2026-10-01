package mcp

import "testing"

func TestMessageTexts(t *testing.T) {
	got, err := messageTexts(map[string]any{"2": "second", "001": "first"})
	if err != nil || len(got) != 2 || got[0].Number != "001" || got[1].Number != "002" {
		t.Fatalf("object form: %+v %v", got, err)
	}
	got, err = messageTexts([]any{map[string]any{"number": "10", "text": "ten"}})
	if err != nil || got[0].Number != "010" {
		t.Fatalf("list form: %+v %v", got, err)
	}
	for name, bad := range map[string]any{
		"four digits": map[string]any{"1000": "x"},
		"not digits":  map[string]any{"A1": "x"},
		"too long":    map[string]any{"001": "0123456789012345678901234567890123456789012345678901234567890123456789ABCD"},
		"wrong shape": "text",
	} {
		if _, err := messageTexts(bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
