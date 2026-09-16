package gloo

import (
	"bytes"
	"context"
	"errors"
	"io"
	iofs "io/fs"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// binaryBlob is content no line-oriented source can survive: NUL bytes,
// bare carriage returns, and no trailing newline.
var binaryBlob = []byte("head\x00mid\r\nno-final-newline")

func TestChunkReaderSourceRoundTripsBinaryExactly(t *testing.T) {
	src := ChunkReaderSource([]io.Reader{bytes.NewReader(binaryBlob)}, 4)
	var out bytes.Buffer
	n, err := RawWriteTo(&out).Consume(context.Background(), src.Stream(context.Background()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out.Bytes(), binaryBlob) {
		t.Errorf("round trip corrupted the bytes: got %q, want %q", out.Bytes(), binaryBlob)
	}
	if int(n) != len(binaryBlob) {
		t.Errorf("byte count = %d, want %d", n, len(binaryBlob))
	}
}

func TestChunkFileSourceConcatenatesFilesExactly(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "a.bin", []byte("one\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "b.bin", []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := ChunkFileSource(fs, []File{"a.bin", "b.bin"}, 0) // 0 → DefaultChunkSize
	var out bytes.Buffer
	if _, err := RawWriteTo(&out).Consume(context.Background(), src.Stream(context.Background())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.String() != "one\x00two" {
		t.Errorf("got %q, want the files concatenated verbatim", out.String())
	}
}

func TestChunkFileSourceMissingFileErrorsThenContinues(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "real.bin", []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := ChunkFileSource(fs, []File{"missing.bin", "real.bin"}, 0)
	var values [][]byte
	var errs []error
	for item := range src.Stream(context.Background()).Chan() {
		if item.Error != nil {
			errs = append(errs, item.Error)
			continue
		}
		values = append(values, item.Value)
	}
	if len(errs) != 1 || !errors.Is(errs[0], iofs.ErrNotExist) {
		t.Errorf("errors = %v, want the missing file's fs.ErrNotExist", errs)
	}
	if len(values) != 1 || string(values[0]) != "data" {
		t.Errorf("values = %v, want the later file still read", values)
	}
}

// failingChunkReader returns data then a non-EOF error.
type failingChunkReader struct {
	err  error
	done bool
}

func (r *failingChunkReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	return copy(p, "data"), nil
}

func TestChunkReaderSourceReadErrorSurfaces(t *testing.T) {
	wantErr := errors.New("device gone")
	src := ChunkReaderSource([]io.Reader{&failingChunkReader{err: wantErr}}, 4)
	_, err := src.Stream(context.Background()).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want the read error", err)
	}
}

func TestChunkReaderSourceClosesClosers(t *testing.T) {
	r := &closeTrackingChunkReader{Reader: strings.NewReader("x")}
	if _, err := ChunkReaderSource([]io.Reader{r}, 1).Stream(context.Background()).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.closed {
		t.Error("reader was not closed")
	}
}

type closeTrackingChunkReader struct {
	io.Reader
	closed bool
}

func (c *closeTrackingChunkReader) Close() error {
	c.closed = true
	return nil
}

func TestChunkSourceStopsMidReadOnDiscard(t *testing.T) {
	done := make(chan struct{})
	src := ChunkReaderSource([]io.Reader{endlessReader{}}, 8)
	stream := src.Stream(context.Background())
	go func() {
		defer close(done)
		count := 0
		for range stream.Chan() {
			count++
			if count == 2 {
				break
			}
		}
		stream.Discard()
	}()
	waitClosed(t, done, "chunk source discard")
}

// endlessReader yields data forever.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'y'
	}
	return len(p), nil
}

func TestChunkFileSourceStopsAcrossFilesOnDiscard(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "big.bin", bytes.Repeat([]byte("y"), 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "never.bin", []byte("unread"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	stream := ChunkFileSource(fs, []File{"big.bin", "never.bin"}, 8).Stream(context.Background())
	go func() {
		defer close(done)
		<-stream.Chan() // one chunk is enough
		stream.Discard()
	}()
	waitClosed(t, done, "chunk file source discard")
}
