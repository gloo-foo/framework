package gloo

import (
	"bufio"
	"context"
	"io"
)

// --- User API (primary interface) ---
// See Run, RunContext, Chain, ChainContext in fluent.go

// WriteTo creates a Sink that writes each string item as a line to the
// given writer.
func WriteTo(w io.Writer) Sink[string, int] {
	return WriteToTerminated(w, NewLine)
}

// WriteToTerminated is WriteTo with an explicit record terminator — pass NUL
// for -z/-0 style output.
func WriteToTerminated(w io.Writer, t Terminator) Sink[string, int] {
	return writerSink{w: bufio.NewWriter(w), t: t}
}

// ByteWriteTo creates a Sink that writes each []byte item as a line to
// the given writer.
func ByteWriteTo(w io.Writer) Sink[[]byte, int] {
	return ByteWriteToTerminated(w, NewLine)
}

// ByteWriteToTerminated is ByteWriteTo with an explicit record terminator —
// pass NUL for -z/-0 style output.
func ByteWriteToTerminated(w io.Writer, t Terminator) Sink[[]byte, int] {
	return byteWriterSink{w: bufio.NewWriter(w), t: t}
}

// RawWriteTo creates a Sink that writes each []byte item VERBATIM — no
// terminator is appended. It is the binary-safe sink: wired to a chunk source
// (ChunkReaderSource, ChunkFileSource) it reproduces input byte-for-byte, the
// way `cat` treats a binary file. The result is the total byte count written.
func RawWriteTo(w io.Writer) Sink[[]byte, ByteCount] {
	return rawWriterSink{w: bufio.NewWriter(w)}
}

// Pump runs a string command as a Unix-style filter: it pumps lines from stdin
// through cmd and out to stdout, returning the number of lines written. It is
// the bridge from a composed gloo command to a `func main` — the framework
// analogue of wiring a tool into a shell pipe. Nothing is shelled out; the work
// stays in-process.
//
//	func main() {
//	    upper := patterns.Map(strings.ToUpper) // string→string
//	    if _, err := gloo.Pump(context.Background(), upper, os.Stdin, os.Stdout); err != nil {
//	        log.Fatal(err)
//	    }
//	}
func Pump(ctx context.Context, cmd Command[string, string], stdin io.Reader, stdout io.Writer) (int, error) {
	return Into(ctx, ReaderSource([]io.Reader{stdin}), cmd, WriteTo(stdout))
}

// PumpBytes is Pump for byte commands: it pumps lines from stdin through a
// Command[[]byte, []byte] and out to stdout.
func PumpBytes(ctx context.Context, cmd Command[[]byte, []byte], stdin io.Reader, stdout io.Writer) (int, error) {
	return Into(ctx, ByteReaderSource([]io.Reader{stdin}), cmd, ByteWriteTo(stdout))
}

// --- Command Author API (lower-level wiring) ---
// These are for command authors building commands and tests,
// not for end users composing pipelines.

// From wires a source into a command, returning the output stream. The caller
// owns the returned stream: consume it fully (e.g. via Collect, which drains),
// or call Discard to abandon it — Discard both stops the upstream and drains,
// which Stop alone does not (pure rill transform stages must be drained to
// unblock).
func From[A, B any](ctx context.Context, src Source[A], cmd Command[A, B]) Stream[B] {
	return cmd.Execute(ctx, src.Stream(ctx))
}

// Into wires a source through a command into a sink, returning the sink's
// result. If the sink returns early (an error, or an early-exit sink), the
// stream is stopped AND drained so no upstream producer goroutine leaks —
// including pure rill transform stages with no cancellation hook.
func Into[A, B, R any](ctx context.Context, src Source[A], cmd Command[A, B], sink Sink[B, R]) (R, error) {
	stream := From(ctx, src, cmd)
	defer stream.Discard()
	return sink.Consume(ctx, stream)
}

// Collect gathers all stream items into a slice. On the first error it stops
// the upstream and drains, so no producer goroutine leaks.
func Collect[T any](_ context.Context, stream Stream[T]) ([]T, error) {
	return stream.Collect()
}

// --- Sink implementations ---

// lineWriter writes one stream item to an underlying writer.
type lineWriter[T any] func(T) error

// writeLines writes each stream item via write until the stream ends or errors.
// On error it stops and drains the upstream (so no producer leaks), returning
// the count written so far.
func writeLines[T any](input Stream[T], write lineWriter[T]) (int, error) {
	count := 0
	for item := range input.Chan() {
		if item.Error != nil {
			input.Discard()
			return count, item.Error
		}
		if err := write(item.Value); err != nil {
			input.Discard()
			return count, err
		}
		count++
	}
	return count, nil
}

// flushReporting flushes buffered output, reporting the stream error first or
// the flush error otherwise — buffered records written before a failure are
// never lost.
func flushReporting[N int | ByteCount](w *bufio.Writer, count N, streamErr error) (N, error) {
	flushErr := w.Flush()
	if streamErr != nil {
		return count, streamErr
	}
	return count, flushErr
}

// writerSink writes string stream items to a buffered writer. Value receivers
// are safe: the stateful bufio.Writer is held by pointer, so every copy shares
// it. Not safe for concurrent use — single consumer only.
type writerSink struct {
	w *bufio.Writer
	t Terminator
}

func (s writerSink) Consume(_ context.Context, input Stream[string]) (int, error) {
	count, streamErr := writeLines(input, func(line string) error {
		return OutputStringWithTerminator(s.w, s.t, Line(line))
	})
	return flushReporting(s.w, count, streamErr)
}

// byteWriterSink writes []byte stream items to a buffered writer. Value
// receivers are safe: the stateful bufio.Writer is held by pointer, so every
// copy shares it. Not safe for concurrent use — single consumer only.
type byteWriterSink struct {
	w *bufio.Writer
	t Terminator
}

func (s byteWriterSink) Consume(_ context.Context, input Stream[[]byte]) (int, error) {
	count, streamErr := writeLines(input, func(line []byte) error {
		return OutputWithTerminator(s.w, s.t, line)
	})
	return flushReporting(s.w, count, streamErr)
}

// ByteCount is a total number of bytes written by a raw (binary-safe) sink.
type ByteCount int64

// rawWriterSink writes []byte stream items verbatim to a buffered writer.
// Value receivers are safe: the stateful bufio.Writer is held by pointer.
// Not safe for concurrent use — single consumer only.
type rawWriterSink struct {
	w *bufio.Writer
}

func (s rawWriterSink) Consume(_ context.Context, input Stream[[]byte]) (ByteCount, error) {
	var total ByteCount
	_, streamErr := writeLines(input, func(chunk []byte) error {
		n, err := s.w.Write(chunk)
		total += ByteCount(n)
		return err
	})
	return flushReporting(s.w, total, streamErr)
}
