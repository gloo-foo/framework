package gloo

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/afero"
)

// ChunkSize is the size in bytes of the buffers a chunk source reads into.
type ChunkSize int

// DefaultChunkSize is the chunk sources' read size when none is given —
// the scale of a kernel pipe buffer.
const DefaultChunkSize ChunkSize = 64 * 1024

// ChunkReaderSource creates a BINARY-SAFE stream of []byte chunks from
// readers: bytes pass through verbatim — no line splitting, no terminator
// stripping — so NUL bytes, partial lines, and missing final newlines survive
// untouched. Wire it to RawWriteTo for a byte-identical copy (`cat` of a
// binary file). A size <= 0 uses DefaultChunkSize. Each chunk is independently
// allocated and safe to retain.
func ChunkReaderSource(readers []io.Reader, size ChunkSize) Source[[]byte] {
	return chunkReaderSource{readers: readers, size: size.orDefault()}
}

// ChunkFileSource is ChunkReaderSource over files on the given filesystem.
func ChunkFileSource(fs afero.Fs, files []File, size ChunkSize) Source[[]byte] {
	return chunkFileSource{fs: fs, files: files, size: size.orDefault()}
}

// orDefault substitutes DefaultChunkSize for a non-positive size.
func (c ChunkSize) orDefault() ChunkSize {
	if c <= 0 {
		return DefaultChunkSize
	}
	return c
}

// chunkReaderSource streams readers chunk-by-chunk. Value receiver —
// immutable, reusable.
type chunkReaderSource struct {
	readers []io.Reader
	size    ChunkSize
}

func (s chunkReaderSource) Stream(ctx context.Context) Stream[[]byte] {
	return Generate(ctx, func(_ context.Context, send func([]byte) bool, sendErr func(error)) {
		for _, r := range s.readers {
			if !chunkReader(r, s.size, send, sendErr) {
				return
			}
		}
	})
}

// chunkFileSource streams files chunk-by-chunk. Value receiver — immutable,
// reusable.
type chunkFileSource struct {
	fs    afero.Fs
	files []File
	size  ChunkSize
}

func (s chunkFileSource) Stream(ctx context.Context) Stream[[]byte] {
	return Generate(ctx, func(_ context.Context, send func([]byte) bool, sendErr func(error)) {
		for _, file := range s.files {
			if !chunkFile(s.fs, file, s.size, send, sendErr) {
				return
			}
		}
	})
}

// chunkFile opens, chunks, and closes one file. It returns false once the
// consumer has stopped, so the caller stops opening further files.
func chunkFile(fs afero.Fs, file File, size ChunkSize, send func([]byte) bool, sendErr func(error)) bool {
	f, err := fs.Open(string(file))
	if err != nil {
		sendErr(err)
		return true
	}
	defer closeReporting(f, sendErr)()
	return chunkAll(f, size, send, sendErr)
}

// chunkReader chunks a single reader, closing it afterward if it is a Closer.
func chunkReader(r io.Reader, size ChunkSize, send func([]byte) bool, sendErr func(error)) bool {
	if c, ok := r.(io.Closer); ok {
		defer closeReporting(c, sendErr)()
	}
	return chunkAll(r, size, send, sendErr)
}

// chunkAll reads r to EOF in size-byte chunks, sending each verbatim. Each
// chunk is a fresh allocation because downstream stages may retain it.
func chunkAll(r io.Reader, size ChunkSize, send func([]byte) bool, sendErr func(error)) bool {
	for {
		buf := make([]byte, size)
		n, err := r.Read(buf)
		if n > 0 && !send(buf[:n]) {
			return false
		}
		if err != nil {
			return reportReadEnd(err, sendErr)
		}
	}
}

// reportReadEnd reports a read failure, staying silent on a normal end of
// input.
func reportReadEnd(err error, sendErr func(error)) bool {
	if !errors.Is(err, io.EOF) {
		sendErr(err)
	}
	return true
}
