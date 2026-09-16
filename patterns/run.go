package patterns

import (
	"context"
	"fmt"
	"io"
	"os"

	gloo "github.com/gloo-foo/framework"
)

// Errors surfaced by Run when the target cannot be executed at all. Pipeline
// failures (stream errors, sink errors) are returned as-is.
const (
	// ErrRunNilTarget reports a nil target.
	ErrRunNilTarget gloo.Error = "patterns: run: target is nil"
	// ErrRunUnsupportedTarget reports a target that is neither a
	// Source[[]byte] nor a Command[[]byte, []byte].
	ErrRunUnsupportedTarget gloo.Error = "patterns: run: target implements neither Source[[]byte] nor Command[[]byte, []byte]"
)

// Run executes a Source[[]byte] or Command[[]byte, []byte] and writes each
// output item as a line to os.Stdout, returning any execution error.
//
// Intended for example tests where the // Output: matcher reads stdout — an
// example asserts success by handling the returned error (test files may
// panic). For commands that need stream input, Run supplies an empty input
// stream — useful for self-sourcing commands (e.g. those that read files
// configured via opts).
func Run(target any) error {
	return runTo(os.Stdout, target)
}

// runTo is Run parameterized over the output writer, so tests can assert the
// rendered bytes instead of redirecting os.Stdout. Run delegates here with
// os.Stdout; the dispatch behavior is identical.
//
// Dispatch is by interface assertion, never by reflective invocation: a target
// whose methods have the wrong shape is rejected without being called, so a
// mismatch can neither panic nor start (and then strand) a producer.
func runTo(w io.Writer, target any) error {
	ctx := context.Background()
	sink := gloo.ByteWriteTo(w)

	switch tgt := target.(type) {
	case nil:
		return ErrRunNilTarget
	case gloo.Source[[]byte]:
		return consume(ctx, sink, tgt.Stream(ctx))
	case gloo.Command[[]byte, []byte]:
		return consume(ctx, sink, tgt.Execute(ctx, gloo.StreamOf[[]byte]()))
	}
	return ErrRunUnsupportedTarget.With(nil, "target type", fmt.Sprintf("%T", target))
}

// consume drains the stream into the sink, returning any error.
func consume(ctx context.Context, sink gloo.Sink[[]byte, int], stream gloo.Stream[[]byte]) error {
	_, err := sink.Consume(ctx, stream)
	return err
}
