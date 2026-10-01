package sapcompress

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"math/rand/v2"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// FuzzDecompress feeds arbitrary bytes to the LZH and LZC decoders. These
// bytes come straight out of a cluster table row, so a truncated or corrupt
// fragment must be an error. A stream that decodes holds exactly the length
// its header promised.
func FuzzDecompress(f *testing.F) {
	names, _ := filepath.Glob("testdata/*.hex")
	for _, name := range names {
		if strings.HasPrefix(filepath.Base(name), "big.") {
			continue // 55 KB; the fuzzer mutates small inputs far faster
		}
		f.Add(load(f, filepath.Base(name)))
	}
	f.Add([]byte{})
	f.Add([]byte{0x05, 0, 0, 0, 0x12, 0x1f, 0x9d, 0x02, 0x00})
	f.Add([]byte{0x05, 0, 0, 0, 0x10, 0x1f, 0x9d, 0x90, 0x41, 0x42})
	f.Fuzz(func(t *testing.T, data []byte) {
		h, herr := ParseHeader(data)
		out, err := Decompress(data)
		if err != nil {
			return
		}
		if herr != nil {
			t.Fatalf("Decompress succeeded on a header ParseHeader refused: %v", herr)
		}
		if len(out) != h.Length {
			t.Fatalf("header promised %d bytes, got %d", h.Length, len(out))
		}
	})
}

// TestHeaderLengthDoesNotReserveMemory: the length in the header is input.
// A nine-byte stream claiming 4 GiB used to reserve 4 GiB before reading a
// bit of it, and on 32-bit builds the conversion went negative and makeslice
// panicked. Both corpus files in testdata/fuzz/FuzzDecompress replay that.
func TestHeaderLengthDoesNotReserveMemory(t *testing.T) {
	for _, in := range [][]byte{
		{0xff, 0xff, 0xff, 0xff, 0x12, 0x1f, 0x9d, 0x02, 0x00},
		{0xff, 0xff, 0xff, 0xff, 0x10, 0x1f, 0x9d, 0x90, 0x41, 0x42},
	} {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if _, err := Decompress(in); err == nil {
			t.Fatalf("% x decoded to 4 GiB", in)
		}
		runtime.ReadMemStats(&after)
		if got := after.TotalAlloc - before.TotalAlloc; got > 64<<20 {
			t.Fatalf("% x: allocated %d MiB for a %d-byte input", in, got>>20, len(in))
		}
	}
}

// TestDecompressPastPreallocCap: maxPrealloc bounds only what the header may
// reserve up front; a stream that really holds more must still come back
// whole. Nothing here writes LZH, so the stream is built from the parts the
// decoder takes apart: compress/flate's raw DEFLATE, shifted left by the
// two-bit noise count (0, so no noise bits follow), behind a SAP header.
func TestDecompressPastPreallocCap(t *testing.T) {
	const size = 5 << 20
	want := make([]byte, size)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range want {
		want[i] = "ABAP cluster INDX BALDAT 0123456789\n"[rng.IntN(36)]
	}
	var d bytes.Buffer
	w, err := flate.NewWriter(&d, flate.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(want); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	raw := d.Bytes()
	body := make([]byte, len(raw)+1)
	for i, b := range raw {
		body[i] |= b << 2
		body[i+1] = b >> 6
	}
	stream := binary.LittleEndian.AppendUint32(nil, size)
	stream = append(stream, byte(LZH)|0x10, 0x1f, 0x9d, 0x00)
	stream = append(stream, body...)

	got, err := Decompress(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) <= maxPrealloc || !bytes.Equal(got, want) {
		t.Fatalf("got %d bytes, want the %d written (cap %d)", len(got), size, maxPrealloc)
	}
}
