package gloo

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/destel/rill"
)

// alwaysFailWriter fails every write.
type alwaysFailWriter struct{ err error }

func (w *alwaysFailWriter) Write([]byte) (int, error) { return 0, w.err }

// bufioSize is bufio.NewWriter's default buffer; lines longer than this
// force a mid-stream flush so write errors surface inside the consume loop
// rather than at the final Flush.
const bufioSize = 4096

func TestWriteToCountsLines(t *testing.T) {
	var buf bytes.Buffer
	sink := WriteTo(&buf)
	src := SliceSource([]string{"a", "b", "c"})
	n, err := sink.Consume(context.Background(), src.Stream(context.Background()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
	if buf.String() != "a\nb\nc\n" {
		t.Errorf("got %q", buf.String())
	}
}

func TestWriteToFlushError(t *testing.T) {
	wantErr := errors.New("flush failed")
	sink := WriteTo(&alwaysFailWriter{err: wantErr})
	src := SliceSource([]string{"small"}) // fits the buffer; error surfaces at Flush
	_, err := sink.Consume(context.Background(), src.Stream(context.Background()))
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestWriteToMidStreamWriteError(t *testing.T) {
	wantErr := errors.New("write failed")
	sink := WriteTo(&alwaysFailWriter{err: wantErr})
	big := strings.Repeat("x", bufioSize+1) // overflows the buffer mid-loop
	src := SliceSource([]string{big, "never reached"})
	n, err := sink.Consume(context.Background(), src.Stream(context.Background()))
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0 (failed line must not be counted)", n)
	}
}

func TestWriteToStreamErrorWins(t *testing.T) {
	// When both the stream and the flush fail, the stream error is the cause
	// and must be the one reported.
	streamErr := errors.New("upstream broke")
	flushErr := errors.New("flush failed")
	sink := WriteTo(&alwaysFailWriter{err: flushErr})

	in := rill.FromSlice([]string{"x"}, streamErr)
	_, err := sink.Consume(context.Background(), Wrap(in))
	if !errors.Is(err, streamErr) {
		t.Errorf("got %v, want stream error %v", err, streamErr)
	}
}

func TestByteWriteToCountsLines(t *testing.T) {
	var buf bytes.Buffer
	sink := ByteWriteTo(&buf)
	src := SliceSource([][]byte{[]byte("a"), []byte("b")})
	n, err := sink.Consume(context.Background(), src.Stream(context.Background()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
	if buf.String() != "a\nb\n" {
		t.Errorf("got %q", buf.String())
	}
}

func TestByteWriteToFlushError(t *testing.T) {
	wantErr := errors.New("flush failed")
	sink := ByteWriteTo(&alwaysFailWriter{err: wantErr})
	src := SliceSource([][]byte{[]byte("small")})
	_, err := sink.Consume(context.Background(), src.Stream(context.Background()))
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestByteWriteToMidStreamWriteError(t *testing.T) {
	wantErr := errors.New("write failed")
	sink := ByteWriteTo(&alwaysFailWriter{err: wantErr})
	big := bytes.Repeat([]byte("x"), bufioSize+1)
	src := SliceSource([][]byte{big})
	n, err := sink.Consume(context.Background(), src.Stream(context.Background()))
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
}

func TestByteWriteToStreamErrorWins(t *testing.T) {
	streamErr := errors.New("upstream broke")
	sink := ByteWriteTo(&alwaysFailWriter{err: errors.New("flush failed")})
	in := rill.FromSlice([][]byte{[]byte("x")}, streamErr)
	_, err := sink.Consume(context.Background(), Wrap(in))
	if !errors.Is(err, streamErr) {
		t.Errorf("got %v, want stream error %v", err, streamErr)
	}
}

func TestFromWiresSourceToCommand(t *testing.T) {
	src := SliceSource([]string{"a", "bb"})
	length := mapCmd(func(s string) (int, error) { return len(s), nil })
	got, err := Collect(context.Background(), From(context.Background(), src, length))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0] != 1 || got[1] != 2 {
		t.Errorf("got %v, want [1 2]", got)
	}
}

func TestIntoReturnsTypedResult(t *testing.T) {
	var buf bytes.Buffer
	src := SliceSource([]string{"x", "y"})
	identity := mapCmd(func(s string) (string, error) { return s, nil })
	n, err := Into(context.Background(), src, identity, WriteTo(&buf))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("n = %d, want 2", n)
	}
	if buf.String() != "x\ny\n" {
		t.Errorf("buf = %q, want %q", buf.String(), "x\ny\n")
	}
}

func TestCollectPropagatesStreamError(t *testing.T) {
	wantErr := errors.New("bad stream")
	in := rill.FromSlice([]string{"x"}, wantErr)
	_, err := Collect(context.Background(), Wrap(in))
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

// --- Pump / PumpBytes: stdin→command→stdout bridging ---

func TestPump_FiltersStdinToStdout(t *testing.T) {
	upper := mapCmd(func(s string) (string, error) { return strings.ToUpper(s), nil })
	var out bytes.Buffer
	n, err := Pump(context.Background(), upper, strings.NewReader("hello\nworld\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("line count = %d, want 2", n)
	}
	if out.String() != "HELLO\nWORLD\n" {
		t.Errorf("got %q", out.String())
	}
}

func TestPumpBytes_FiltersStdinToStdout(t *testing.T) {
	rev := mapCmd(func(b []byte) ([]byte, error) {
		out := make([]byte, len(b))
		for i := range b {
			out[len(b)-1-i] = b[i]
		}
		return out, nil
	})
	var out bytes.Buffer
	n, err := PumpBytes(context.Background(), rev, strings.NewReader("abc\nxy\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("line count = %d, want 2", n)
	}
	if out.String() != "cba\nyx\n" {
		t.Errorf("got %q", out.String())
	}
}

// endlessSource is a Source over an unbounded producer with a done probe: if a
// consumer abandons it without stop+drain, done never closes.
type endlessSource struct{ done chan struct{} }

func (s endlessSource) Stream(ctx context.Context) Stream[string] {
	return Generate(ctx, func(_ context.Context, send func(string) bool, _ func(error)) {
		defer close(s.done)
		for send("x") {
		}
	})
}

// A write error must STOP an endless upstream, not merely error out: deleting
// writeLines' input.Discard() leaves the producer running and fails this test.
func TestWriteToWriteErrorStopsEndlessUpstream(t *testing.T) {
	src := endlessSource{done: make(chan struct{})}
	wantErr := errors.New("write failed")
	_, err := WriteTo(&alwaysFailWriter{err: wantErr}).Consume(context.Background(), src.Stream(context.Background()))
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
	waitClosed(t, src.done, "write error over endless upstream")
}

// firstLineSink consumes one line and returns early without draining — the
// shape Into's deferred Discard exists to make safe.
type firstLineSink struct{}

func (firstLineSink) Consume(_ context.Context, in Stream[string]) (string, error) {
	item, ok := <-in.Chan()
	if !ok {
		return "", nil
	}
	return item.Value, item.Error
}

// Into must tear the stream down when its sink returns early: deleting Into's
// deferred stream.Discard() leaves the endless producer running forever.
func TestIntoEarlyExitSinkStopsEndlessUpstream(t *testing.T) {
	src := endlessSource{done: make(chan struct{})}
	identity := FuncCommand[string, string](func(_ context.Context, in Stream[string]) Stream[string] { return in })
	got, err := Into[string, string, string](context.Background(), src, identity, firstLineSink{})
	if err != nil || got != "x" {
		t.Fatalf("got %q err=%v, want first line", got, err)
	}
	waitClosed(t, src.done, "early-exit sink via Into")
}

// failAfterReader yields some content, then a read error — a broken stdin.
type failAfterReader struct {
	err     error
	content string
	read    bool
}

func (r *failAfterReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, r.err
	}
	r.read = true
	return copy(p, r.content), nil
}

// A stdin read error must surface from Pump along with the count of lines
// already written — the error path of the func-main bridge.
func TestPumpStdinReadErrorSurfaces(t *testing.T) {
	wantErr := errors.New("stdin torn")
	identity := FuncCommand[string, string](func(_ context.Context, in Stream[string]) Stream[string] { return in })
	var out strings.Builder
	n, err := Pump(context.Background(), identity, &failAfterReader{content: "one\n", err: wantErr}, &out)
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want the reader's error", err)
	}
	if n != 1 || out.String() != "one\n" {
		t.Errorf("wrote %d %q, want the 1 complete line before the failure", n, out.String())
	}
}

// NUL-terminated output: the -z/-0 convention end to end.
func TestWriteToTerminatedNUL(t *testing.T) {
	var out strings.Builder
	sink := WriteToTerminated(&out, Terminator(0))
	n, err := sink.Consume(context.Background(), StreamOf("a", "b"))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v, want 2 records", n, err)
	}
	if out.String() != "a\x00b\x00" {
		t.Errorf("got %q, want NUL-terminated records", out.String())
	}
}

func TestByteWriteToTerminatedNUL(t *testing.T) {
	var out strings.Builder
	sink := ByteWriteToTerminated(&out, Terminator(0))
	n, err := sink.Consume(context.Background(), StreamOf([]byte("a"), []byte("b")))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v, want 2 records", n, err)
	}
	if out.String() != "a\x00b\x00" {
		t.Errorf("got %q, want NUL-terminated records", out.String())
	}
}

// A raw sink flush error must surface when the stream itself succeeded.
func TestRawWriteToFlushErrorSurfaces(t *testing.T) {
	wantErr := errors.New("flush failed")
	_, err := RawWriteTo(&alwaysFailWriter{err: wantErr}).
		Consume(context.Background(), StreamOf([]byte("tiny")))
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want the flush error", err)
	}
}

// A raw sink write error must stop the upstream and report the stream error.
func TestRawWriteToWriteErrorStopsEndlessUpstream(t *testing.T) {
	byteSrc := Generate(context.Background(), func(_ context.Context, send func([]byte) bool, _ func(error)) {
		for send(bytes.Repeat([]byte("y"), 1024)) {
		}
	})
	wantErr := errors.New("write failed")
	_, err := RawWriteTo(&alwaysFailWriter{err: wantErr}).Consume(context.Background(), byteSrc)
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want the write error", err)
	}
}
