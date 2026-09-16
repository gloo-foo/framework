package patterns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	gloo "github.com/gloo-foo/framework"
)

// runBytes executes cmd over input and returns the collected output, failing
// the test on any pipeline error.
func runBytes(t *testing.T, input [][]byte, cmd gloo.Command[[]byte, []byte]) [][]byte {
	t.Helper()
	ctx := context.Background()
	results, err := cmd.Execute(ctx, gloo.SliceSource(input).Stream(ctx)).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return results
}

func TestSubprocess_BasicEcho(t *testing.T) {
	cmd := Subprocess("echo", "hello world")

	results := runBytes(t, [][]byte{}, cmd)
	if len(results) != 1 || string(results[0]) != "hello world" {
		t.Errorf("got %q, want [\"hello world\"]", results)
	}
}

func TestSubprocess_CatPassthrough(t *testing.T) {
	cmd := Subprocess("cat")

	results := runBytes(t, [][]byte{[]byte("line1"), []byte("line2")}, cmd)
	if len(results) != 2 {
		t.Fatalf("got %d lines, want 2", len(results))
	}
	if string(results[0]) != "line1" {
		t.Errorf("line 0: got %q, want \"line1\"", results[0])
	}
	if string(results[1]) != "line2" {
		t.Errorf("line 1: got %q, want \"line2\"", results[1])
	}
}

func TestSubprocess_ExitCodeError(t *testing.T) {
	cmd := Subprocess("false")

	ctx := context.Background()
	src := gloo.SliceSource([][]byte{})
	stream := cmd.Execute(ctx, src.Stream(ctx))
	_, err := stream.Collect()

	if !errors.Is(err, ErrSubprocess) {
		t.Fatalf("got %v, want ErrSubprocess (non-zero exit code)", err)
	}
}

func TestSubprocess_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	cmd := Subprocess("sleep", "10")
	src := gloo.SliceSource([][]byte{})
	stream := cmd.Execute(ctx, src.Stream(ctx))
	_, err := stream.Collect()

	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from cancelled context, got: %v", err)
	}
}

// Subprocess must stream stdin to the child while reading its stdout — both
// sides run concurrently like a shell pipe. A child that exits after reading
// one line (head -1) must not deadlock or fail the pipeline, even when far
// more input than the OS pipe buffer is offered: the remaining input is
// drained and discarded, mirroring shell SIGPIPE semantics.
func TestSubprocess_ChildExitsEarly(t *testing.T) {
	cmd := Subprocess("head", "-n", "1")

	// ~1.7MB of input — far beyond the 64KB OS pipe buffer, so the test
	// deadlocks if Subprocess waits for the child to consume everything.
	input := make([][]byte, 100_000)
	for i := range input {
		input[i] = []byte(fmt.Sprintf("line-%d", i))
	}

	done := make(chan struct{})
	var results [][]byte
	var err error
	go func() {
		defer close(done)
		ctx := context.Background()
		src := gloo.SliceSource(input)
		results, err = cmd.Execute(ctx, src.Stream(ctx)).Collect()
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: Subprocess did not finish when child exited early")
	}
	if err != nil {
		t.Fatalf("child exiting early must not fail the pipeline (shell SIGPIPE semantics): %v", err)
	}
	if len(results) != 1 || string(results[0]) != "line-0" {
		t.Fatalf("expected [line-0], got %d items", len(results))
	}
}

// The child must start producing output before the input stream has ended —
// true streaming, not collect-then-run. The input stream blocks until the
// child's first output line has been observed; with the old buffering
// implementation the child never starts and this deadlocks.
func TestSubprocess_StreamsConcurrently(t *testing.T) {
	cmd := Subprocess("cat")

	firstOut := make(chan struct{})
	src := gloo.FuncCommand[[]byte, []byte](func(ctx context.Context, _ gloo.Stream[[]byte]) gloo.Stream[[]byte] {
		return gloo.Generate(ctx, func(_ context.Context, send func([]byte) bool, _ func(error)) {
			if !send([]byte("first")) {
				return
			}
			select {
			case <-firstOut:
				// Child echoed the first line while input is still open:
				// the subprocess is genuinely streaming.
			case <-time.After(10 * time.Second):
				// Give up; the receive below will fail the test.
			}
			if !send([]byte("second")) {
				return
			}
		})
	})

	ctx := context.Background()
	empty := gloo.StreamOf[[]byte]()
	out := cmd.Execute(ctx, src.Execute(ctx, empty))

	item := <-out.Chan()
	if item.Error != nil || string(item.Value) != "first" {
		t.Fatalf("expected first output line before input ended, got %v", item)
	}
	close(firstOut)

	rest, err := out.Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || string(rest[0]) != "second" {
		t.Fatalf("expected [second], got %v", rest)
	}
}

// An error item in the input stream propagates to the output stream, and the
// child still processes the lines that arrived before the error — like a
// shell pipeline where an upstream command dies partway: downstream keeps
// the data it already received, stderr carries the diagnosis.
func TestSubprocess_InputErrorPropagates(t *testing.T) {
	cmd := Subprocess("cat")

	src := gloo.FuncCommand[[]byte, []byte](func(ctx context.Context, _ gloo.Stream[[]byte]) gloo.Stream[[]byte] {
		return gloo.Generate(ctx, func(_ context.Context, send func([]byte) bool, sendErr func(error)) {
			if !send([]byte("good")) {
				return
			}
			sendErr(fmt.Errorf("upstream broke"))
		})
	})

	ctx := context.Background()
	empty := gloo.StreamOf[[]byte]()
	// Drain manually rather than via Collect: this test asserts that the data
	// delivered before the error survives, whereas Collect discards results on
	// the first error.
	var results [][]byte
	var err error
	for item := range cmd.Execute(ctx, src.Execute(ctx, empty)).Chan() {
		if item.Error != nil {
			if err == nil {
				err = item.Error
			}
			continue
		}
		results = append(results, item.Value)
	}

	if err == nil || !strings.Contains(err.Error(), "upstream broke") {
		t.Fatalf("expected upstream error to propagate, got: %v", err)
	}
	if len(results) != 1 || string(results[0]) != "good" {
		t.Fatalf("expected data before the error to survive, got %v", results)
	}
}

// Context cancellation sends SIGTERM (not SIGKILL) so the child can clean up.
func TestSubprocess_CancelSendsSigterm(t *testing.T) {
	// exec so the shell is replaced and SIGTERM reaches sleep directly.
	cmd := Subprocess("sh", "-c", "exec sleep 30")

	ctx, cancel := context.WithCancel(context.Background())
	src := gloo.SliceSource([][]byte{})

	out := cmd.Execute(ctx, src.Stream(ctx))
	// Give the child a moment to start, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := out.Collect()
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after cancellation")
		}
		// Teardown initiated by external cancellation is graceful (SIGPIPE-like):
		// the subprocess no longer reports the "signal: terminated" wait error;
		// the producer scope surfaces the context cause once instead.
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled after cancellation, got: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled subprocess did not exit")
	}
}

// Without exec, sh forks its command: SIGTERM kills sh but the orphaned
// grandchild survives, holding the stdout pipe open. The pipeline must still
// return promptly after cancellation instead of blocking on the dead pipe.
// (The orphan holds inherited fds for its remaining lifetime, so keep it short.)
func TestSubprocess_CancelWithSurvivingGrandchild(t *testing.T) {
	cmd := Subprocess("sh", "-c", "sleep 3")

	ctx, cancel := context.WithCancel(context.Background())
	src := gloo.SliceSource([][]byte{})

	out := cmd.Execute(ctx, src.Stream(ctx))
	time.Sleep(100 * time.Millisecond)
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := out.Collect()
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled after cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline blocked on pipe held open by orphaned grandchild")
	}
}

// A start failure (command not found) must surface promptly even when the
// input stream never ends: the error is sent before the input is drained.
func TestSubprocess_StartFailureWithEndlessInput(t *testing.T) {
	cmd := Subprocess("definitely-not-a-real-command-xyz")

	endless := gloo.FuncCommand[[]byte, []byte](func(ctx context.Context, _ gloo.Stream[[]byte]) gloo.Stream[[]byte] {
		return gloo.Generate(ctx, func(_ context.Context, send func([]byte) bool, _ func(error)) {
			for {
				if !send([]byte("y")) {
					return
				}
			}
		})
	})

	ctx := context.Background()
	empty := gloo.StreamOf[[]byte]()
	out := cmd.Execute(ctx, endless.Execute(ctx, empty))

	select {
	case item := <-out.Chan():
		if !errors.Is(item.Error, ErrSubprocessStart) {
			t.Fatalf("expected ErrSubprocessStart, got %v", item)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start error never surfaced: blocked draining an endless input")
	}
}

// Non-zero exit codes propagate as errors (sh -c 'exit 3').
func TestSubprocess_NonZeroExit(t *testing.T) {
	cmd := Subprocess("sh", "-c", "exit 3")

	ctx := context.Background()
	src := gloo.SliceSource([][]byte{})
	_, err := cmd.Execute(ctx, src.Stream(ctx)).Collect()
	if !errors.Is(err, ErrSubprocess) {
		t.Fatalf("expected ErrSubprocess sentinel, got: %v", err)
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("expected exit status 3 in message, got: %v", err)
	}
}

// TestSubprocess_FinalLineTerminated verifies that every input item — including
// the last — is written to the child's stdin with a trailing newline, matching
// Unix line-tool semantics. Regression test: previously the final line had no
// terminator, so newline-counting tools like `wc -l` undercounted by one.
func TestSubprocess_FinalLineTerminated(t *testing.T) {
	cmd := Subprocess("wc", "-l")

	results := runBytes(t, [][]byte{[]byte("a"), []byte("b"), []byte("c")}, cmd)
	if len(results) == 0 {
		t.Fatal("expected wc -l output, got none")
	}

	got := strings.TrimSpace(string(results[0]))
	n, err := strconv.Atoi(got)
	if err != nil {
		t.Fatalf("could not parse wc -l output %q: %v", got, err)
	}
	if n != 3 {
		t.Errorf("wc -l counted %d lines, want 3 (final line must be newline-terminated)", n)
	}
}

// A `yes`-like child upstream of Take must die of EPIPE when Take stops
// reading, and the pipeline must SUCCEED (no error) — the shell-without-pipefail
// contract from specs/005 point 6.
func TestSubprocessStop_ChildDiesOnDownstreamStop(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}

	// Infinite producer: prints "x" forever.
	yesLike := Subprocess(ProcessName(sh), "-c", "while true; do echo x; done")
	pipeline := gloo.Compose[[]byte](yesLike).To(Take[[]byte](3))

	done := make(chan struct{})
	var got [][]byte
	var collectErr error
	go func() {
		defer close(done)
		got, collectErr = pipeline.
			Execute(context.Background(), gloo.StreamOf[[]byte]()).
			Collect()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("subprocess pipeline did not terminate after downstream Take stopped")
	}

	if collectErr != nil {
		t.Fatalf("downstream-initiated stop must be graceful, got error: %v", collectErr)
	}
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3", len(got))
	}
	for _, line := range got {
		if string(line) != "x" {
			t.Errorf("got line %q, want \"x\"", line)
		}
	}
}

// fakeProcess injects pipe failures that real *exec.Cmd pipes never produce in
// practice, exercising Subprocess's setup-error paths.
type fakeProcess struct {
	stdinErr  error
	stdoutErr error
	stdout    io.ReadCloser
}

func (f fakeProcess) StdinPipe() (io.WriteCloser, error) {
	if f.stdinErr != nil {
		return nil, f.stdinErr
	}
	return nopWriteCloser{}, nil
}

func (f fakeProcess) StdoutPipe() (io.ReadCloser, error) {
	if f.stdoutErr != nil {
		return nil, f.stdoutErr
	}
	if f.stdout != nil {
		return f.stdout, nil
	}
	return io.NopCloser(nil), nil
}

func (fakeProcess) Start() error { return nil }
func (fakeProcess) Wait() error  { return nil }

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

// runFakeSubprocess runs Subprocess over empty input with an injected spawn and
// returns the surfaced error.
func runFakeSubprocess(proc process) error {
	sp := func(context.Context, ProcessName, []ProcessArg) process { return proc }
	cmd := subprocessWith(sp, "fake", nil)
	ctx := context.Background()
	_, err := gloo.From(ctx, gloo.SliceSource([][]byte{}), cmd).Collect()
	return err
}

// closeTrackingReader records whether Close was called, so a test can assert
// the subprocess closes its stdout (causing the child to die of EPIPE).
type closeTrackingReader struct {
	io.Reader
	closed bool
}

func (r *closeTrackingReader) Close() error { r.closed = true; return nil }

// TestSubprocessScanStopsWhenConsumerStops drives scan directly with a send that
// reports the consumer has stopped, so the very first line trips the downstream-
// stop branch. This is the deterministic replacement for relying on a real child
// racing Take — that race covered this branch only when the producer happened to
// lose to pipe closure, which is non-deterministic. scan must close stdout (EPIPE
// to the child) and return nil (a graceful, non-error stop).
func TestSubprocessScanStopsWhenConsumerStops(t *testing.T) {
	rc := &closeTrackingReader{Reader: strings.NewReader("line1\nline2\n")}
	s := subprocess{
		stdout: rc,
		send:   func([]byte) bool { return false }, // consumer has already stopped
	}
	if err := s.scan(); err != nil {
		t.Fatalf("scan must return nil when the consumer stops, got %v", err)
	}
	if !rc.closed {
		t.Error("scan must close stdout when the consumer stops, so the child dies of EPIPE")
	}
}

func TestSubprocessStdinPipeError(t *testing.T) {
	want := errors.New("no stdin")
	err := runFakeSubprocess(fakeProcess{stdinErr: want})
	if !errors.Is(err, ErrSubprocessStdinPipe) {
		t.Errorf("got %v, want ErrSubprocessStdinPipe", err)
	}
	if !errors.Is(err, want) {
		t.Errorf("got %v, want wrapped cause %v", err, want)
	}
}

func TestSubprocessStdoutPipeError(t *testing.T) {
	want := errors.New("no stdout")
	err := runFakeSubprocess(fakeProcess{stdoutErr: want})
	if !errors.Is(err, ErrSubprocessStdoutPipe) {
		t.Errorf("got %v, want ErrSubprocessStdoutPipe", err)
	}
	if !errors.Is(err, want) {
		t.Errorf("got %v, want wrapped cause %v", err, want)
	}
}

// ExampleSubprocess wraps an external tool as a pipeline command: input
// lines stream to the child's stdin, its stdout becomes the output stream.
// Reserved for irreplaceable external tools — prefer pure-Go patterns.
func ExampleSubprocess() {
	upper := Subprocess("tr", "a-z", "A-Z")

	src := gloo.SliceSource([][]byte{[]byte("hello"), []byte("world")})
	_, _ = gloo.Run(src, gloo.ByteWriteTo(os.Stdout), upper)
	// Output:
	// HELLO
	// WORLD
}

// waitStopped fails the test if done is not closed within a short deadline —
// the deterministic proof that an upstream producer goroutine was stopped.
func waitStopped(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("upstream producer did not exit after %s", what)
	}
}

// endlessByteSource returns an unbounded upstream and a channel closed when its
// producer goroutine exits — the leak probe for SIGPIPE-analogue tests.
func endlessByteSource(t *testing.T) (gloo.Stream[[]byte], <-chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	src := gloo.Generate(context.Background(), func(_ context.Context, send func([]byte) bool, _ func(error)) {
		defer close(done)
		for send([]byte("y")) {
		}
	})
	return src, done
}

// The `yes | head -1` analogue: when the child exits while the upstream is
// still producing, the pipeline must terminate and the upstream must be
// STOPPED — a shell kills the producer with SIGPIPE; drain-without-stop would
// hang forever on an endless source.
func TestSubprocess_ChildExitEarlyStopsEndlessUpstream(t *testing.T) {
	src, done := endlessByteSource(t)
	out := Subprocess("head", "-n", "1").Execute(context.Background(), src)

	type result struct {
		err   error
		lines [][]byte
	}
	got := make(chan result, 1)
	go func() {
		lines, err := out.Collect()
		got <- result{err: err, lines: lines}
	}()
	select {
	case r := <-got:
		if r.err != nil || len(r.lines) != 1 || string(r.lines[0]) != "y" {
			t.Fatalf("got %q err=%v, want one line %q", r.lines, r.err, "y")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pipeline did not terminate after the child exited (yes | head -1 analogue)")
	}
	waitStopped(t, done, "child exit over endless upstream")
}

// A start failure over an endless upstream must still let a consumer that
// ranges the output to completion finish, with the upstream stopped.
func TestSubprocess_StartFailureStopsEndlessUpstream(t *testing.T) {
	src, done := endlessByteSource(t)
	out := Subprocess("definitely-not-a-real-binary-gloo").Execute(context.Background(), src)
	var firstErr error
	for item := range out.Chan() { // range to completion, no Discard
		if item.Error != nil && firstErr == nil {
			firstErr = item.Error
		}
	}
	if !errors.Is(firstErr, ErrSubprocessStart) {
		t.Fatalf("got %v, want ErrSubprocessStart", firstErr)
	}
	waitStopped(t, done, "start failure over endless upstream")
}

// An upstream error must stop a still-producing upstream AND close the child's
// stdin so an EOF-bound child (cat) can exit; the upstream error is reported.
func TestSubprocess_UpstreamErrorStopsProducingUpstream(t *testing.T) {
	wantErr := errors.New("upstream broke")
	done := make(chan struct{})
	src := gloo.Generate(context.Background(), func(_ context.Context, send func([]byte) bool, sendErr func(error)) {
		defer close(done)
		if !send([]byte("a")) {
			return
		}
		sendErr(wantErr)
		for send([]byte("more")) {
		}
	})
	out := Subprocess("cat").Execute(context.Background(), src)

	errs := make(chan error, 1)
	go func() {
		var firstErr error
		for item := range out.Chan() {
			if item.Error != nil && firstErr == nil {
				firstErr = item.Error
			}
		}
		errs <- firstErr
	}()
	select {
	case firstErr := <-errs:
		if !errors.Is(firstErr, wantErr) {
			t.Fatalf("got %v, want the upstream error", firstErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pipeline did not terminate after an upstream error (cat never saw EOF)")
	}
	waitStopped(t, done, "upstream error over still-producing upstream")
}

// failingReadCloser errors on the first read — a stdout pipe torn mid-stream.
type failingReadCloser struct{ err error }

func (f failingReadCloser) Read([]byte) (int, error) { return 0, f.err }
func (failingReadCloser) Close() error               { return nil }

// A stdout read failure (not EOF) must surface as ErrSubprocessReadStdout with
// the cause wrapped — the one subprocess sentinel no other test produces.
func TestSubprocessReadStdoutError(t *testing.T) {
	want := errors.New("stdout torn")
	err := runFakeSubprocess(fakeProcess{stdout: failingReadCloser{err: want}})
	if !errors.Is(err, ErrSubprocessReadStdout) {
		t.Errorf("got %v, want ErrSubprocessReadStdout", err)
	}
	if !errors.Is(err, want) {
		t.Errorf("got %v, want wrapped cause %v", err, want)
	}
}
