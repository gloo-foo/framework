package gloo

import (
	"bufio"
	"bytes"
	"context"
	"io"

	"github.com/spf13/afero"
)

// MaxLineSize bounds how large a single input line may grow (1 GiB).
// bufio.Scanner's default 64KB token limit would abort pipelines on long
// lines (minified JSON, JSONL, long log lines), where shell tools like cat,
// grep, and sed handle arbitrary line lengths. The buffer grows on demand,
// so the cap costs no memory for typical input.
const MaxLineSize = 1 << 30

// initialScanBufferSize is the starting scan buffer; it grows as needed up
// to MaxLineSize.
const initialScanBufferSize = 64 * 1024

// scanFunc extracts the current scanner token as a value of type T.
type scanFunc[T any] func(*bufio.Scanner) T

// NewLineScanner returns a line scanner that handles lines up to MaxLineSize,
// matching shell line-tool semantics. Command authors reading lines from an
// io.Reader directly should use this instead of bufio.NewScanner to avoid
// the default 64KB line-length limit. It returns a value; take its address
// once (or store it in a variable) and scan from there — do not copy it after
// the first Scan call.
func NewLineScanner(r io.Reader) bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, initialScanBufferSize), MaxLineSize)
	return *s
}

// scanTerminated returns a bufio.SplitFunc splitting records at the given
// terminator byte, emitting a final unterminated record — bufio.ScanLines
// semantics generalized to any terminator (NUL for -z/-0 input). Unlike
// ScanLines it performs no carriage-return stripping: records pass verbatim.
func scanTerminated(t Terminator) bufio.SplitFunc {
	return func(data []byte, isEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, byte(t)); i >= 0 {
			return i + 1, data[:i], nil
		}
		if isEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	}
}

// scanText extracts the current token as a string (copies from scan buffer).
func scanText(s *bufio.Scanner) string { return s.Text() }

// scanBytes extracts the current token as a []byte copy.
// bufio.Scanner.Bytes() returns a slice sharing the scan buffer, so we must copy.
func scanBytes(s *bufio.Scanner) []byte {
	return append([]byte(nil), s.Bytes()...)
}

// closeReporting returns a deferred closer that reports any Close error.
func closeReporting(c io.Closer, sendErr func(error)) func() {
	return func() {
		if err := c.Close(); err != nil {
			sendErr(err)
		}
	}
}

// scanAll scans every record from r, sending each scanned value. A nil split
// scans lines (the default). It returns false the instant send reports the
// consumer has stopped, so callers stop early.
func scanAll[T any](r io.Reader, split bufio.SplitFunc, scan scanFunc[T], send func(T) bool, sendErr func(error)) bool {
	scanner := NewLineScanner(r)
	if split != nil {
		scanner.Split(split)
	}
	for scanner.Scan() {
		if !send(scan(&scanner)) {
			return false
		}
	}
	if err := scanner.Err(); err != nil {
		sendErr(err)
	}
	return true
}

// scanFile opens, scans, and closes a single file. It returns false once the
// consumer has stopped, so the caller stops opening further files.
func scanFile[T any](
	fs afero.Fs,
	file File,
	split bufio.SplitFunc,
	scan scanFunc[T],
	send func(T) bool,
	sendErr func(error),
) bool {
	f, err := fs.Open(string(file))
	if err != nil {
		sendErr(err)
		return true
	}
	defer closeReporting(f, sendErr)()
	return scanAll(f, split, scan, send, sendErr)
}

// scanReader scans a single reader, closing it afterward if it is a Closer.
func scanReader[T any](
	r io.Reader,
	split bufio.SplitFunc,
	scan scanFunc[T],
	send func(T) bool,
	sendErr func(error),
) bool {
	if c, ok := r.(io.Closer); ok {
		defer closeReporting(c, sendErr)()
	}
	return scanAll(r, split, scan, send, sendErr)
}

// fileSourceStream creates a stream from files using the given split and scan
// functions. Generate makes send cancellation-aware, so the source stops
// mid-file the instant a downstream Take stops reading or the context is
// cancelled.
func fileSourceStream[T any](
	ctx context.Context,
	fs afero.Fs,
	files []File,
	split bufio.SplitFunc,
	scan scanFunc[T],
) Stream[T] {
	return Generate(ctx, func(_ context.Context, send func(T) bool, sendErr func(error)) {
		for _, file := range files {
			if !scanFile(fs, file, split, scan, send, sendErr) {
				return
			}
		}
	})
}

// readerSourceStream creates a stream from readers using the given split and
// scan functions.
func readerSourceStream[T any](
	ctx context.Context,
	readers []io.Reader,
	split bufio.SplitFunc,
	scan scanFunc[T],
) Stream[T] {
	return Generate(ctx, func(_ context.Context, send func(T) bool, sendErr func(error)) {
		for _, r := range readers {
			if !scanReader(r, split, scan, send, sendErr) {
				return
			}
		}
	})
}

// SliceSource creates a Source from an in-memory slice.
// This is the equivalent of shell here-strings or echo piping.
func SliceSource[T any](items []T) Source[T] {
	return sliceSource[T]{items: items}
}

// sliceSource streams an in-memory slice. Value receiver — immutable, reusable.
type sliceSource[T any] struct {
	items []T
}

func (s sliceSource[T]) Stream(ctx context.Context) Stream[T] {
	// Generate (not StreamOf) so the source is cancellation-aware: a downstream
	// Take or a cancelled context stops emission instead of pushing the whole
	// slice into the pipe.
	return Generate(ctx, func(_ context.Context, send func(T) bool, _ func(error)) {
		for _, item := range s.items {
			if !send(item) {
				return
			}
		}
	})
}

// FileSource creates a stream of string lines from files on the given filesystem.
func FileSource(fs afero.Fs, files []File) Source[string] {
	return typedFileSource[string]{fs: fs, files: files, scan: scanText}
}

// ByteFileSource creates a stream of []byte lines from files on the given filesystem.
// Each line is an independent copy, safe to retain after the next scan.
func ByteFileSource(fs afero.Fs, files []File) Source[[]byte] {
	return typedFileSource[[]byte]{fs: fs, files: files, scan: scanBytes}
}

// FileSourceTerminated is FileSource splitting records at an explicit
// terminator instead of newlines — pass NUL for -z/-0 style input. Records
// pass verbatim (no carriage-return stripping).
func FileSourceTerminated(fs afero.Fs, files []File, t Terminator) Source[string] {
	return typedFileSource[string]{fs: fs, files: files, scan: scanText, split: scanTerminated(t)}
}

// ByteFileSourceTerminated is ByteFileSource splitting records at an explicit
// terminator — pass NUL for -z/-0 style input.
func ByteFileSourceTerminated(fs afero.Fs, files []File, t Terminator) Source[[]byte] {
	return typedFileSource[[]byte]{fs: fs, files: files, scan: scanBytes, split: scanTerminated(t)}
}

// typedFileSource streams files record-by-record. Value receiver — immutable,
// reusable. A nil split scans lines.
type typedFileSource[T any] struct {
	fs    afero.Fs
	scan  scanFunc[T]
	split bufio.SplitFunc
	files []File
}

func (s typedFileSource[T]) Stream(ctx context.Context) Stream[T] {
	return fileSourceStream(ctx, s.fs, s.files, s.split, s.scan)
}

// ReaderSource creates a stream of string lines from readers.
func ReaderSource(readers []io.Reader) Source[string] {
	return typedReaderSource[string]{readers: readers, scan: scanText}
}

// ByteReaderSource creates a stream of []byte lines from readers.
// Each line is an independent copy, safe to retain after the next scan.
func ByteReaderSource(readers []io.Reader) Source[[]byte] {
	return typedReaderSource[[]byte]{readers: readers, scan: scanBytes}
}

// ReaderSourceTerminated is ReaderSource splitting records at an explicit
// terminator instead of newlines — pass NUL for -z/-0 style input. Records
// pass verbatim (no carriage-return stripping).
func ReaderSourceTerminated(readers []io.Reader, t Terminator) Source[string] {
	return typedReaderSource[string]{readers: readers, scan: scanText, split: scanTerminated(t)}
}

// ByteReaderSourceTerminated is ByteReaderSource splitting records at an
// explicit terminator — pass NUL for -z/-0 style input.
func ByteReaderSourceTerminated(readers []io.Reader, t Terminator) Source[[]byte] {
	return typedReaderSource[[]byte]{readers: readers, scan: scanBytes, split: scanTerminated(t)}
}

// typedReaderSource streams readers record-by-record. Value receiver —
// immutable, reusable. A nil split scans lines.
type typedReaderSource[T any] struct {
	scan    scanFunc[T]
	split   bufio.SplitFunc
	readers []io.Reader
}

func (s typedReaderSource[T]) Stream(ctx context.Context) Stream[T] {
	return readerSourceStream(ctx, s.readers, s.split, s.scan)
}
