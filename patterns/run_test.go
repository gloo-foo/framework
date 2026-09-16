package patterns

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/destel/rill"

	gloo "github.com/gloo-foo/framework"
)

// TestRunTo_DispatchesSource exercises the Source dispatch path and asserts
// the exact rendered bytes (one item per line) — not merely "did not error".
func TestRunTo_DispatchesSource(t *testing.T) {
	var buf bytes.Buffer
	src := gloo.SliceSource([][]byte{[]byte("a"), []byte("b")})
	if err := runTo(&buf, src); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "a\nb\n" {
		t.Errorf("buf = %q, want %q", buf.String(), "a\nb\n")
	}
}

// constByteCommand is a Command[[]byte, []byte] that ignores its input and emits
// a fixed line, so it produces deterministic, non-empty output even on the empty
// input stream Run supplies — exercising the Execute dispatch path.
type constByteCommand struct{ out []byte }

func (c constByteCommand) Execute(_ context.Context, _ gloo.Stream[[]byte]) gloo.Stream[[]byte] {
	return gloo.StreamOf(c.out)
}

// TestRunTo_DispatchesCommand exercises the Execute dispatch path and asserts
// the exact rendered bytes.
func TestRunTo_DispatchesCommand(t *testing.T) {
	var buf bytes.Buffer
	if err := runTo(&buf, constByteCommand{out: []byte("emitted")}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "emitted\n" {
		t.Errorf("buf = %q, want %q", buf.String(), "emitted\n")
	}
}

func TestRunTo_NilTarget(t *testing.T) {
	var buf bytes.Buffer
	err := runTo(&buf, nil)
	if !errors.Is(err, ErrRunNilTarget) {
		t.Fatalf("got %v, want ErrRunNilTarget", err)
	}
}

// expectUnsupported asserts runTo rejects target with ErrRunUnsupportedTarget.
func expectUnsupported(t *testing.T, target any) {
	t.Helper()
	var buf bytes.Buffer
	err := runTo(&buf, target)
	if !errors.Is(err, ErrRunUnsupportedTarget) {
		t.Fatalf("got %v, want ErrRunUnsupportedTarget", err)
	}
}

func TestRunTo_RejectsNeitherSourceNorCommand(t *testing.T) {
	expectUnsupported(t, struct{}{})
}

// wrongStreamType has a Stream method that does not return gloo.Stream[[]byte];
// Run must not treat it as a Source.
type wrongStreamType struct{}

func (wrongStreamType) Stream(context.Context) int { return 0 }

// wrongStreamArity has a Stream method returning two values; Run must not
// treat it as a Source.
type wrongStreamArity struct{}

func (wrongStreamArity) Stream(context.Context) (int, error) { return 0, nil }

// wrongExecuteType has an Execute method with the right arity but the wrong
// return type; Run must not treat it as a Command.
type wrongExecuteType struct{}

func (wrongExecuteType) Execute(context.Context, gloo.Stream[[]byte]) int { return 0 }

// wrongExecuteArity has an Execute method returning two values.
type wrongExecuteArity struct{}

func (wrongExecuteArity) Execute(context.Context, gloo.Stream[[]byte]) (int, error) {
	return 0, nil
}

func TestRunTo_RejectsWrongStreamReturnType(t *testing.T) {
	expectUnsupported(t, wrongStreamType{})
}

func TestRunTo_RejectsWrongStreamArity(t *testing.T) {
	expectUnsupported(t, wrongStreamArity{})
}

func TestRunTo_RejectsWrongExecuteReturnType(t *testing.T) {
	expectUnsupported(t, wrongExecuteType{})
}

func TestRunTo_RejectsWrongExecuteArity(t *testing.T) {
	expectUnsupported(t, wrongExecuteArity{})
}

// errorSource implements Source[[]byte] and emits a single error.
type errorSource struct{ err error }

func (s errorSource) Stream(context.Context) gloo.Stream[[]byte] {
	ch := make(chan rill.Try[[]byte], 1)
	ch <- rill.Try[[]byte]{Error: s.err}
	close(ch)
	return gloo.Wrap(ch)
}

func TestRunTo_ReturnsStreamError(t *testing.T) {
	var buf bytes.Buffer
	want := errors.New("source failed")
	err := runTo(&buf, errorSource{err: want})
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want the source's error", err)
	}
}

// ExampleRun executes a Source and writes each item as a line to stdout — the
// // Output: matcher observes Run's os.Stdout path end-to-end.
func ExampleRun() {
	src := gloo.SliceSource([][]byte{[]byte("hello"), []byte("world")})
	if err := Run(src); err != nil {
		panic(err)
	}
	// Output:
	// hello
	// world
}

// wrongStreamInputArity has a Stream method that takes no context; Run must
// reject it without a reflective call (which would panic on arity).
type wrongStreamInputArity struct{}

func (wrongStreamInputArity) Stream() gloo.Stream[[]byte] { return gloo.StreamOf[[]byte]() }

func TestRunTo_RejectsWrongStreamInputArity(t *testing.T) {
	expectUnsupported(t, wrongStreamInputArity{})
}

// wrongElementCommand is a well-formed Command over the wrong element type;
// Run must reject it without invoking Execute (a reflective call would panic
// on the Stream[[]byte] argument).
type wrongElementCommand struct{}

func (wrongElementCommand) Execute(_ context.Context, in gloo.Stream[string]) gloo.Stream[string] {
	return in
}

func TestRunTo_RejectsWrongElementCommand(t *testing.T) {
	expectUnsupported(t, wrongElementCommand{})
}

// leakProbeSource records whether Run invoked it despite the wrong element
// type — dispatch must reject by shape, never by calling and abandoning the
// returned stream (a producer-goroutine leak).
type leakProbeSource struct{ invoked *bool }

func (s leakProbeSource) Stream(ctx context.Context) gloo.Stream[string] {
	*s.invoked = true
	return gloo.SliceSource([]string{"x"}).Stream(ctx)
}

func TestRunTo_DoesNotInvokeMismatchedTarget(t *testing.T) {
	invoked := false
	expectUnsupported(t, leakProbeSource{invoked: &invoked})
	if invoked {
		t.Error("Run invoked a mismatched target's Stream method; rejection must be by shape only")
	}
}
