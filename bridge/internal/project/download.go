// Package project downloads and unpacks the PHC installation project from the
// STM. The STM serves it as a base64-chunked ZIP whose entries use general-
// purpose flag bit 3 (data descriptors) with zero sizes in the local headers
// and raw DEFLATE — the format that defeated the Swift hand-rolled parser until
// it switched to a central-directory reader. Go's archive/zip reads the central
// directory, so it handles this natively (verified against real hardware).
package project

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/stm"
)

// Conservative limits applied before allocation (plan §10).
const (
	MaxChunks     = 4096
	MaxZIPBytes   = 16 << 20 // accumulated ZIP
	MaxEntryBytes = 8 << 20  // any single extracted file
	MaxZIPEntries = 128
)

// ChunkReader is the subset of *stm.Client the download loop needs.
type ChunkReader interface {
	ReadFile(ctx context.Context, fileIndex, chunkIndex, mode int) (stm.FileChunk, error)
}

// Files holds the raw project member files extracted from the STM ZIP.
type Files struct {
	PPFX []byte // project.ppfx — required (hardware config)
	TPFX []byte // project.tpfx — optional (automation tools)
}

// Download pulls the project ZIP via the STM's chunked readFile(0, i, 1) loop,
// validating chunk ordering and size bounds as it goes.
func Download(ctx context.Context, r ChunkReader) ([]byte, error) {
	var buf bytes.Buffer
	expectedTotal := -1
	for i := 0; ; i++ {
		if i >= MaxChunks {
			return nil, fmt.Errorf("project: exceeded %d chunks without finishing", MaxChunks)
		}
		chunk, err := r.ReadFile(ctx, 0, i, 1)
		if err != nil {
			return nil, fmt.Errorf("project: readFile chunk %d: %w", i, err)
		}
		if chunk.Total < 1 || chunk.Total > MaxChunks {
			return nil, fmt.Errorf("project: implausible total %d at chunk %d", chunk.Total, i)
		}
		if expectedTotal < 0 {
			expectedTotal = chunk.Total
		} else if chunk.Total != expectedTotal {
			return nil, fmt.Errorf("project: chunk total changed from %d to %d at chunk %d",
				expectedTotal, chunk.Total, i)
		}
		if chunk.Cur != i {
			return nil, fmt.Errorf("project: chunk order mismatch: got cur=%d, want %d", chunk.Cur, i)
		}
		if chunk.Cur < 0 || chunk.Cur >= chunk.Total {
			return nil, fmt.Errorf("project: chunk %d is outside total %d", chunk.Cur, chunk.Total)
		}
		if len(chunk.Bin) == 0 {
			return nil, fmt.Errorf("project: chunk %d has an empty payload", i)
		}
		if buf.Len()+len(chunk.Bin) > MaxZIPBytes {
			return nil, fmt.Errorf("project: accumulated ZIP exceeds %d bytes", MaxZIPBytes)
		}
		buf.Write(chunk.Bin)
		if chunk.Cur >= chunk.Total-1 {
			break
		}
	}
	if buf.Len() == 0 {
		return nil, fmt.Errorf("project: empty ZIP download")
	}
	return buf.Bytes(), nil
}

// Extract reads project.ppfx (required) and project.tpfx (optional) from the ZIP
// bytes. Entry names are matched exactly and read into memory — never used as
// filesystem paths — so path traversal is not a concern here.
func Extract(zipData []byte) (Files, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return Files{}, fmt.Errorf("project: opening ZIP: %w", err)
	}
	if len(zr.File) > MaxZIPEntries {
		return Files{}, fmt.Errorf("project: ZIP contains %d entries, over the %d limit",
			len(zr.File), MaxZIPEntries)
	}

	var f Files
	seen := map[string]bool{}
	for _, e := range zr.File {
		var dst *[]byte
		switch e.Name {
		case "project.ppfx":
			dst = &f.PPFX
		case "project.tpfx":
			dst = &f.TPFX
		default:
			continue
		}
		if seen[e.Name] {
			return Files{}, fmt.Errorf("project: duplicate entry %q", e.Name)
		}
		seen[e.Name] = true
		data, err := readEntry(e)
		if err != nil {
			return Files{}, err
		}
		*dst = data
	}

	if f.PPFX == nil {
		return Files{}, fmt.Errorf("project: project.ppfx not found in ZIP")
	}
	return f, nil
}

func readEntry(e *zip.File) ([]byte, error) {
	// Sizes come from the central directory (correct even though the STM's local
	// headers are zeroed under flag bit 3), so this pre-check is meaningful.
	if e.UncompressedSize64 > MaxEntryBytes {
		return nil, fmt.Errorf("project: %s declares %d bytes, over the %d limit",
			e.Name, e.UncompressedSize64, MaxEntryBytes)
	}
	rc, err := e.Open()
	if err != nil {
		return nil, fmt.Errorf("project: opening %s: %w", e.Name, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, MaxEntryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("project: reading %s: %w", e.Name, err)
	}
	if int64(len(data)) > MaxEntryBytes {
		return nil, fmt.Errorf("project: %s exceeds %d bytes when decompressed", e.Name, MaxEntryBytes)
	}
	return data, nil
}
