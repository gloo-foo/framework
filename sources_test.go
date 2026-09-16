package gloo

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

// --- helpers: fault injection ---

// errAfterReader yields data, then returns err instead of io.EOF.
type errAfterReader struct {
	err  error
	data string
	pos  int
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// closeErrReader reads successfully but fails on Close.
type closeErrReader struct {
	io.Reader
	err    error
	closed bool
}

func (r *closeErrReader) Close() error {
	r.closed = true
	return r.err
}

// closeErrFs wraps a filesystem so every opened file fails on Close.
type closeErrFs struct {
	afero.Fs
	closeErr error
}

func (f *closeErrFs) Open(name string) (afero.File, error) {
	file, err := f.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &closeErrFile{File: file, err: f.closeErr}, nil
}

type closeErrFile struct {
	afero.File
	err error
}

func (f *closeErrFile) Close() error {
	// Best-effort close of the wrapped file; this fake deliberately reports the
	// injected err to exercise the caller's error path.
	_ = f.File.Close()
	return f.err
}

// --- NewLineScanner ---

func TestNewLineScannerHandlesLinesBeyondBufioDefault(t *testing.T) {
	// bufio.Scanner's default token limit is 64KB; ours must go far beyond.
	long := strings.Repeat("x", 256*1024)
	s := NewLineScanner(strings.NewReader(long + "\nshort"))

	if !s.Scan() {
		t.Fatalf("Scan failed on long line: %v", s.Err())
	}
	if len(s.Text()) != len(long) {
		t.Errorf("long line truncated: got %d bytes, want %d", len(s.Text()), len(long))
	}
	if !s.Scan() || s.Text() != "short" {
		t.Errorf("second line: got %q, want %q", s.Text(), "short")
	}
}

func TestScanBytesReturnsIndependentCopy(t *testing.T) {
	s := NewLineScanner(strings.NewReader("first\nsecond"))
	s.Scan()
	first := scanBytes(&s)
	s.Scan() // advancing may overwrite the scanner's internal buffer
	if string(first) != "first" {
		t.Errorf("earlier line corrupted by later scan: %q", first)
	}
}

// --- FileSource / ByteFileSource error paths ---

func TestFileSourceReportsCloseError(t *testing.T) {
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "a.txt", []byte("line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	closeErr := errors.New("close failed")
	fs := &closeErrFs{Fs: mem, closeErr: closeErr}

	src := FileSource(fs, []File{"a.txt"})
	_, err := src.Stream(context.Background()).Collect()
	if !errors.Is(err, closeErr) {
		t.Errorf("got %v, want %v", err, closeErr)
	}
}

func TestFileSourceOpenErrorDoesNotStopLaterFiles(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "b.txt", []byte("from-b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := FileSource(fs, []File{"missing.txt", "b.txt"})
	var lines []string
	var errs []error
	for item := range src.Stream(context.Background()).Chan() {
		if item.Error != nil {
			errs = append(errs, item.Error)
			continue
		}
		lines = append(lines, item.Value)
	}
	if len(errs) != 1 {
		t.Fatalf("want 1 error for the missing file, got %v", errs)
	}
	if len(lines) != 1 || lines[0] != "from-b" {
		t.Errorf("later file should still be read, got %v", lines)
	}
}

func TestFileSourceCancelledContext(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "a.txt", []byte("line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := FileSource(fs, []File{"a.txt"})
	_, err := src.Stream(ctx).Collect()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

// --- ReaderSource / ByteReaderSource error paths ---

func TestReaderSourceReportsReadError(t *testing.T) {
	readErr := errors.New("connection reset")
	src := ReaderSource([]io.Reader{&errAfterReader{data: "ok\n", err: readErr}})

	var lines []string
	var streamErr error
	for item := range src.Stream(context.Background()).Chan() {
		if item.Error != nil {
			streamErr = item.Error
			continue
		}
		lines = append(lines, item.Value)
	}
	if !errors.Is(streamErr, readErr) {
		t.Errorf("got %v, want %v", streamErr, readErr)
	}
	if len(lines) != 1 || lines[0] != "ok" {
		t.Errorf("lines before the failure should be delivered, got %v", lines)
	}
}

func TestReaderSourceClosesClosers(t *testing.T) {
	r := &closeErrReader{Reader: strings.NewReader("line\n"), err: nil}
	src := ReaderSource([]io.Reader{r})
	if _, err := src.Stream(context.Background()).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.closed {
		t.Error("reader implementing io.Closer should be closed")
	}
}

func TestReaderSourceReportsCloseError(t *testing.T) {
	closeErr := errors.New("close failed")
	r := &closeErrReader{Reader: strings.NewReader("line\n"), err: closeErr}
	src := ReaderSource([]io.Reader{r})
	_, err := src.Stream(context.Background()).Collect()
	if !errors.Is(err, closeErr) {
		t.Errorf("got %v, want %v", err, closeErr)
	}
}

func TestReaderSourceCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := ReaderSource([]io.Reader{strings.NewReader("line\n")})
	_, err := src.Stream(ctx).Collect()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestByteReaderSourceCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := ByteReaderSource([]io.Reader{strings.NewReader("line\n")})
	_, err := src.Stream(ctx).Collect()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

// --- SliceSource ---

func TestSliceSourceEmpty(t *testing.T) {
	src := SliceSource([]string{})
	items, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("got %v, want empty", items)
	}
}

func TestSliceSourcePreservesOrder(t *testing.T) {
	src := SliceSource([]int{3, 1, 2})
	items, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items[0] != 3 || items[1] != 1 || items[2] != 2 {
		t.Errorf("got %v, want [3 1 2]", items)
	}
}

// --- final-line semantics (no trailing newline) ---

func TestFileSourceNoTrailingNewline(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "a.txt", []byte("one\ntwo"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := FileSource(fs, []File{"a.txt"})
	items, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 || items[1] != "two" {
		t.Errorf("final unterminated line should be emitted, got %v", items)
	}
}

// readErrFs wraps a filesystem so opened files fail mid-read.
type readErrFs struct {
	afero.Fs
	readErr error
}

func (f *readErrFs) Open(name string) (afero.File, error) {
	file, err := f.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &readErrFile{File: file, err: f.readErr}, nil
}

type readErrFile struct {
	afero.File
	err error
}

func (f *readErrFile) Read([]byte) (int, error) { return 0, f.err }

func TestFileSourceReportsReadError(t *testing.T) {
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "a.txt", []byte("line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readErr := errors.New("io failure")
	fs := &readErrFs{Fs: mem, readErr: readErr}

	src := FileSource(fs, []File{"a.txt"})
	_, err := src.Stream(context.Background()).Collect()
	if !errors.Is(err, readErr) {
		t.Errorf("got %v, want %v", err, readErr)
	}
}

// manyLines builds an input far larger than the scanner's initial read buffer
// (initialScanBufferSize) so that an early-stopping scan provably leaves bytes
// unread: the first Read cannot slurp the whole input, so a byte counter can
// distinguish "stopped mid-scan" from "drained everything".
func manyLines() string {
	var sb strings.Builder
	for range initialScanBufferSize { // >> one buffer's worth of 5-byte lines
		_, _ = sb.WriteString("line\n")
	}
	return sb.String()
}

// --- sources: early stop and ByteFileSource (gloo-package coverage) ---

func TestSliceSourceStopsWhenConsumerStops(t *testing.T) {
	items := make([]int, streamBuffer*2)
	for i := range items {
		items[i] = i
	}
	s := SliceSource(items).Stream(context.Background())
	first := <-s.Chan() // read one, leaving the producer blocked on a full buffer
	if first.Error != nil {
		t.Fatalf("unexpected error: %v", first.Error)
	}
	if first.Value != 0 {
		t.Errorf("first value = %d, want 0", first.Value)
	}
	s.Discard() // stop → the producer's send returns false → early-return branch
}

// countingReader wraps a reader and records how many bytes it has served, so a
// test can assert that an early-stopping scan did NOT drain the whole input.
type countingReader struct {
	r      io.Reader
	served int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.served += n
	return n, err
}

func TestReaderSourceStopsMidScan(t *testing.T) {
	full := manyLines()
	cr := &countingReader{r: strings.NewReader(full)}
	s := ReaderSource([]io.Reader{cr}).Stream(context.Background())
	<-s.Chan()  // read one, leaving the scanner blocked on a full buffer
	s.Discard() // stop → scanAll's send returns false → early-return branch
	if cr.served >= len(full) {
		t.Errorf("reader served all %d bytes; scan did not stop early", len(full))
	}
}

// countingFs counts bytes read from files it opens, proving an early-stopping
// file scan did not read the whole file.
type countingFs struct {
	afero.Fs
	served *int
}

func (c countingFs) Open(name string) (afero.File, error) {
	f, err := c.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return countingFile{File: f, served: c.served}, nil
}

type countingFile struct {
	afero.File
	served *int
}

func (c countingFile) Read(p []byte) (int, error) {
	n, err := c.File.Read(p)
	*c.served += n
	return n, err
}

func TestFileSourceStopsMidScan(t *testing.T) {
	mem := afero.NewMemMapFs()
	full := []byte(manyLines())
	if err := afero.WriteFile(mem, "big.txt", full, 0o644); err != nil {
		t.Fatal(err)
	}
	served := 0
	fs := countingFs{Fs: mem, served: &served}
	s := FileSource(fs, []File{"big.txt"}).Stream(context.Background())
	<-s.Chan()
	s.Discard()
	if served >= len(full) {
		t.Errorf("file served all %d bytes; scan did not stop early", len(full))
	}
}

func TestByteFileSourceCollects(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "a.txt", []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ByteFileSource(fs, []File{"a.txt"}).Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || string(got[0]) != "one" || string(got[1]) != "two" {
		t.Errorf("got %q, want [one two]", got)
	}
}

// NUL-terminated input: the -z/-0 convention, including a final unterminated
// record and verbatim carriage returns.
func TestReaderSourceTerminatedNUL(t *testing.T) {
	in := strings.NewReader("a\x00b\r\n\x00tail")
	src := ReaderSourceTerminated([]io.Reader{in}, Terminator(0))
	got, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a", "b\r\n", "tail"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestByteReaderSourceTerminatedNUL(t *testing.T) {
	in := strings.NewReader("x\x00y")
	src := ByteReaderSourceTerminated([]io.Reader{in}, Terminator(0))
	got, err := src.Stream(context.Background()).Collect()
	if err != nil || len(got) != 2 || string(got[0]) != "x" || string(got[1]) != "y" {
		t.Fatalf("got %v err=%v, want [x y]", got, err)
	}
}

func TestFileSourceTerminatedNUL(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "z.list", []byte("one\x00two\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := FileSourceTerminated(fs, []File{"z.list"}, Terminator(0)).
		Stream(context.Background()).Collect()
	if err != nil || len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("got %v err=%v, want [one two]", got, err)
	}
}

func TestByteFileSourceTerminatedNUL(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "z.list", []byte("p\x00q"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ByteFileSourceTerminated(fs, []File{"z.list"}, Terminator(0)).
		Stream(context.Background()).Collect()
	if err != nil || len(got) != 2 || string(got[0]) != "p" || string(got[1]) != "q" {
		t.Fatalf("got %v err=%v, want [p q]", got, err)
	}
}
