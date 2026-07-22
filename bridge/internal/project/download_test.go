package project

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/stm"
)

// fakeReader replays a fixed sequence of chunks.
type fakeReader struct {
	chunks []stm.FileChunk
	err    error
}

func (r *fakeReader) ReadFile(_ context.Context, _, chunkIndex, _ int) (stm.FileChunk, error) {
	if r.err != nil {
		return stm.FileChunk{}, r.err
	}
	if chunkIndex >= len(r.chunks) {
		return stm.FileChunk{}, io.ErrUnexpectedEOF
	}
	return r.chunks[chunkIndex], nil
}

func TestDownload_ConcatenatesChunksInOrder(t *testing.T) {
	r := &fakeReader{chunks: []stm.FileChunk{
		{Cur: 0, Total: 3, Bin: []byte("AAA")},
		{Cur: 1, Total: 3, Bin: []byte("BBB")},
		{Cur: 2, Total: 3, Bin: []byte("CCC")},
	}}
	got, err := Download(context.Background(), r)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != "AAABBBCCC" {
		t.Errorf("got %q", got)
	}
}

func TestDownload_RejectsOrderMismatch(t *testing.T) {
	r := &fakeReader{chunks: []stm.FileChunk{
		{Cur: 0, Total: 2, Bin: []byte("AAA")},
		{Cur: 5, Total: 2, Bin: []byte("BBB")}, // cur != index
	}}
	if _, err := Download(context.Background(), r); err == nil {
		t.Error("expected an order-mismatch error")
	}
}

func TestDownload_RejectsImplausibleTotal(t *testing.T) {
	r := &fakeReader{chunks: []stm.FileChunk{{Cur: 0, Total: 0, Bin: []byte("x")}}}
	if _, err := Download(context.Background(), r); err == nil {
		t.Error("expected a total-out-of-range error")
	}
}

func TestDownload_RejectsChangingTotal(t *testing.T) {
	r := &fakeReader{chunks: []stm.FileChunk{
		{Cur: 0, Total: 3, Bin: []byte("AAA")},
		{Cur: 1, Total: 2, Bin: []byte("BBB")},
	}}
	if _, err := Download(context.Background(), r); err == nil {
		t.Error("expected a changing-total error")
	}
}

func TestDownload_RejectsEmptyChunk(t *testing.T) {
	r := &fakeReader{chunks: []stm.FileChunk{{Cur: 0, Total: 1}}}
	if _, err := Download(context.Background(), r); err == nil {
		t.Error("expected an empty-chunk error")
	}
}

// makeZIP builds a ZIP with Go's archive/zip Writer, which — like the STM —
// streams entries with data descriptors (flag bit 3) and zeroed local-header
// sizes. That makes it a faithful stand-in for the STM's archive shape.
func makeZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtract_DataDescriptorZIP(t *testing.T) {
	zipData := makeZIP(t, map[string]string{
		"project.ppfx": "<PROJECT/>",
		"project.tpfx": "<TOOLS/>",
		"project.cpfx": "<IGNORED/>", // present but not extracted
	})
	// Sanity: confirm the archive really uses data descriptors (flag bit 3).
	zr, _ := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if zr.File[0].Flags&0x8 == 0 {
		t.Fatal("test ZIP does not use data descriptors; fixture no longer models the STM")
	}

	f, err := Extract(zipData)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if string(f.PPFX) != "<PROJECT/>" {
		t.Errorf("ppfx = %q", f.PPFX)
	}
	if string(f.TPFX) != "<TOOLS/>" {
		t.Errorf("tpfx = %q", f.TPFX)
	}
}

func TestExtract_MissingPPFXFails(t *testing.T) {
	zipData := makeZIP(t, map[string]string{"project.tpfx": "<TOOLS/>"})
	if _, err := Extract(zipData); err == nil {
		t.Error("expected an error when project.ppfx is absent")
	}
}

func TestExtract_OversizeEntryFails(t *testing.T) {
	zipData := makeZIP(t, map[string]string{
		"project.ppfx": strings.Repeat("x", MaxEntryBytes+1),
	})
	if _, err := Extract(zipData); err == nil {
		t.Error("expected an oversize-entry error")
	}
}

func TestExtract_GarbageIsNotAZIP(t *testing.T) {
	if _, err := Extract([]byte("not a zip at all")); err == nil {
		t.Error("expected an error opening non-ZIP data")
	}
}

func TestExtract_TooManyEntriesFails(t *testing.T) {
	files := make(map[string]string, MaxZIPEntries+1)
	files["project.ppfx"] = "<PROJECT/>"
	for i := 0; i < MaxZIPEntries; i++ {
		files[fmt.Sprintf("ignored-%03d", i)] = ""
	}
	if _, err := Extract(makeZIP(t, files)); err == nil {
		t.Error("expected an entry-count error")
	}
}
