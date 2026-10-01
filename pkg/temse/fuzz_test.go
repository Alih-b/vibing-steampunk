package temse

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// FuzzDecodeList reads arbitrary TemSe list objects in both Unicode code
// pages. A spool that is cut short or not a list must be an error. A list
// that decodes renders, and never ends with an empty page.
func FuzzDecodeList(f *testing.F) {
	raw, err := os.ReadFile("testdata/list_unicode.hex")
	if err != nil {
		f.Fatal(err)
	}
	data, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data, true)
	f.Add(data[:len(data)/2], false)
	f.Add([]byte{0x00, 0x06, 0x00, 0x00, 'P', 0x00}, true)
	f.Add([]byte{0x00, 0x10, 0x00, 0x00, ' ', 0x00, 0xFF, 0xF8, 'a', 0x00, 0xFC, 0xF8, 0x00, 0x25, 'b', 0x00}, true)
	f.Fuzz(func(t *testing.T, data []byte, little bool) {
		charcod := "4102"
		if little {
			charcod = "4103"
		}
		l, err := DecodeList(data, charcod)
		if err != nil {
			return
		}
		if l.Records < len(l.Lines) {
			t.Fatalf("records %d, lines %d", l.Records, len(l.Lines))
		}
		// Every page end opens a page, except a page end that is the last
		// line: the page after it has nothing on it and is not counted.
		ends := 0
		for _, line := range l.Lines {
			if line.Control == string(ctlPageEnd) {
				ends++
			}
		}
		want := ends + 1
		if n := len(l.Lines); n > 0 && l.Lines[n-1].Control == string(ctlPageEnd) {
			want--
		}
		if l.Pages != want {
			t.Fatalf("%d pages for %d page ends (last line %q): an empty page was counted", l.Pages, ends, lastControl(l))
		}
		for i, line := range l.Lines {
			if line.Page < 1 || line.Page > l.Pages {
				t.Fatalf("line %d is on page %d of %d", i, line.Page, l.Pages)
			}
		}
		_ = l.Text()
	})
}

func lastControl(l *List) string {
	if len(l.Lines) == 0 {
		return ""
	}
	return l.Lines[len(l.Lines)-1].Control
}
