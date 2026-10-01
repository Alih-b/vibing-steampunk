package datacluster

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/oisee/vibing-steampunk/pkg/sapcompress"
)

// FuzzParse decodes arbitrary cluster blobs. A cluster is read out of a table
// row (INDX, BALDAT, STXL ...) and may be one fragment short, from a codepage
// this code does not know, or simply not a cluster: that is an error, never a
// panic. The parsed cluster is walked the way the CLI renders it.
func FuzzParse(f *testing.F) {
	names, _ := filepath.Glob("testdata/*.hex")
	for _, name := range names {
		f.Add(loadHex(f, filepath.Base(name)))
	}
	f.Add([]byte{0xFF, 0x06, 0x02, 0x01, 0x01, 0x02, 0x80, 0x00, '4', '1', '0', '3', 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, blob []byte) {
		c, err := Parse(blob)
		if err != nil {
			return
		}
		if c == nil {
			t.Fatal("Parse returned neither a cluster nor an error")
		}
		_ = fmt.Sprintf("%+v", c)
	})
}

// FuzzReadExport reads SE16-style exports. The seed is the fixture laid out
// the way SE16 and SE16N write it.
func FuzzReadExport(f *testing.F) {
	whole := loadHex(f, "indx_plain.hex")
	h := strings.ToUpper(hex.EncodeToString(whole))
	f.Add([]byte("MANDT,RELID,SRTFD,SRTF2,LOEKZ,AEDAT,USERA,CLUSTR,CLUSTD\r\n" +
		"001,ZV,OTHER,0,,20260904,TESTUSER," + fmt.Sprint(len(whole)) + "," + h + "\r\n"))
	f.Add([]byte("RELID;SRTFD;SRTF2;CLUSTR;CLUSTD\nZV;VSPFIX;0;" + fmt.Sprint(len(whole)) + ";" + strings.ToLower(h) + "\n"))
	f.Add([]byte("RELID|SRTF2|CLUSTR|CLUSTD\nZV|1|2|FF00\nZV|0|2|FF06\n"))
	f.Add([]byte("\ufeff\n\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		recs, err := ReadExport(bytes.NewReader(data), "LOEKZ", "AEDAT", "USERA")
		if err != nil {
			return
		}
		for _, r := range recs {
			_, _ = Parse(r.Blob)
		}
	})
}

// TestTableRowCountDoesNotSizeAllocation replays the FuzzParse crasher: a
// nested table whose row count reads 0xADBEEF00 from a 190-byte blob. The
// count went straight into make() and asked for 66 GB, which killed the
// fuzzing process and would take down the server that parsed it.
func TestTableRowCountDoesNotSizeAllocation(t *testing.T) {
	blob := string(rowCountCrasher(t))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := Parse([]byte(blob)); err == nil {
		t.Fatal("a truncated cluster parsed")
	}
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 64<<20 {
		t.Fatalf("allocated %d MiB for a %d-byte blob", got>>20, len(blob))
	}
}

// TestRowLengthOutOfRange: the length after a BC marker is four bytes of
// input. 0xFFFFFFFF used to become -1 on a 32-bit build, slip past need()
// and panic slicing backwards; on 64-bit it was a 4 GiB "truncated" claim.
// Run with GOARCH=386 to see the original panic.
func TestRowLengthOutOfRange(t *testing.T) {
	blob := loadHex(t, "indx_plain.hex")
	at := bytes.IndexByte(blob[HeaderSize:], 0xBC) + HeaderSize
	if at < HeaderSize {
		t.Fatal("fixture has no row marker")
	}
	bad := append([]byte{}, blob...)
	copy(bad[at+1:], []byte{0xFF, 0xFF, 0xFF, 0xFF})
	if _, err := Parse(bad); err == nil {
		t.Fatal("a row of 0xFFFFFFFF bytes parsed")
	}
}

// TestTableCountReadErrorIsNotAnEmptyTable: tableRows used to drop the
// errors from u32(). Once u32 refused lengths past 2 GiB, a block missing its
// row count — BE, line length, then straight to BF — read "BF 03 0E 00" as
// the count, had it refused, carried on with zero rows and took the BF as
// the table's end, so a corrupt cluster parsed cleanly. The blob here is the
// BALDAT fixture, decompressed, with the first empty table's count removed.
func TestTableCountReadErrorIsNotAnEmptyTable(t *testing.T) {
	blob := loadHex(t, "baldat_a4h.hex")
	body, err := sapcompress.Decompress(blob[HeaderSize:])
	if err != nil {
		t.Fatal(err)
	}
	plain := append(append([]byte{}, blob[:HeaderSize]...), body...)
	plain[4] = 1 // body format: uncompressed
	if _, err := Parse(plain); err != nil {
		t.Fatalf("the decompressed fixture itself does not parse: %v", err)
	}
	// BE, line length, count 0, BF: an empty nested table.
	at := -1
	for i := HeaderSize; i+10 <= len(plain); i++ {
		if plain[i] == 0xBE && bytes.Equal(plain[i+5:i+10], []byte{0, 0, 0, 0, 0xBF}) {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatal("fixture has no empty nested table")
	}
	bad := append(append([]byte{}, plain[:at+5]...), plain[at+9:]...)
	if _, err := Parse(bad); err == nil {
		t.Fatal("a nested table without its row count parsed")
	}
}

// TestHeaderLengthsOutOfRange: the object header's row length and the
// descriptor's lengths are read straight from the blob too. 0xFFFFFFFF in
// an elementary object's row length became -1 on a 32-bit build, matched the
// -1 its single field summed to, and the row's run[:leaf.Length] panicked.
// Run with GOARCH=386 to see the original panic.
func TestHeaderLengthsOutOfRange(t *testing.T) {
	head := loadHex(t, "indx_plain.hex")[:HeaderSize]
	obj := make([]byte, 32)
	obj[0], obj[1] = 7, 0x00 // elementary, type code 0
	copy(obj[3:], []byte{0xFF, 0xFF, 0xFF, 0xFF})
	row := []byte{0xBC, 0, 0, 0, 1, 'x', 0xBD, 0x04}
	blob := append(append(append([]byte{}, head...), obj...), row...)
	if _, err := Parse(blob); err == nil {
		t.Fatal("an object of 0xFFFFFFFF bytes parsed")
	}

	// The same length in a structure's descriptor entries.
	obj[0] = 2
	desc := []byte{0xAB, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF, 0xAA, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF, 0xAC, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF}
	blob = append(append(append(append([]byte{}, head...), obj...), desc...), row...)
	if _, err := Parse(blob); err == nil {
		t.Fatal("a descriptor of 0xFFFFFFFF bytes parsed")
	}
}

// rowCountCrasher reads the FuzzParse corpus file whose nested table claims
// 0xADBEEF00 rows; the count is its last four bytes.
func rowCountCrasher(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/fuzz/FuzzParse/table_row_count_66gb")
	if err != nil {
		t.Fatal(err)
	}
	// The corpus file is `go test fuzz v1` followed by one []byte literal.
	lit := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[1])
	lit = strings.TrimSuffix(strings.TrimPrefix(lit, "[]byte("), ")")
	blob, err := strconv.Unquote(lit)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(blob)
}

// TestPaddedRowCountDoesNotScaleAllocation: refusing a count beyond the bytes
// left is not enough on its own. A blob padded to cover its count passed that
// check, and make() then reserved a 24-byte row header per claimed row, 24
// times the input, before the first row failed to parse. Capacity now comes
// from the rows actually read.
func TestPaddedRowCountDoesNotScaleAllocation(t *testing.T) {
	const rows = 4 << 20
	crasher := rowCountCrasher(t)
	at := len(crasher) - 4 // the count is the last four bytes
	blob := append([]byte{}, crasher[:at]...)
	blob = binary.BigEndian.AppendUint32(blob, rows)
	blob = append(blob, make([]byte, rows)...) // 0x00 is no row marker
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := Parse(blob); err == nil {
		t.Fatal("a table of padding parsed")
	}
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > uint64(2*len(blob)) {
		t.Fatalf("allocated %d MiB for a %d MiB blob", got>>20, len(blob)>>20)
	}
}
