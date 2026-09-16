package patterns

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/destel/rill"

	gloo "github.com/gloo-foo/framework"
)

// sigtermGracePeriod is how long a cancelled child process gets to exit after
// SIGTERM before it is forcibly killed.
const sigtermGracePeriod = 10 * time.Second

// Errors emitted by Subprocess. Each wraps the underlying cause and the process
// name, so callers can match with errors.Is(err, ErrSubprocessStart) etc.
const (
	ErrSubprocessStdinPipe  gloo.Error = "subprocess: stdin pipe"
	ErrSubprocessStdoutPipe gloo.Error = "subprocess: stdout pipe"
	ErrSubprocessStart      gloo.Error = "subprocess: start"
	ErrSubprocessReadStdout gloo.Error = "subprocess: read stdout"
	ErrSubprocess           gloo.Error = "subprocess"
)

// process is the subset of *exec.Cmd that the runner drives. The seam lets
// tests inject pipe/start failures that are otherwise unreachable.
type process interface {
	StdinPipe() (io.WriteCloser, error)
	StdoutPipe() (io.ReadCloser, error)
	Start() error
	Wait() error
}

// ProcessName names the executable a Subprocess runs — the argv[0] handed to
// the OS (resolved via PATH exactly as exec.Command does).
type ProcessName string

// ProcessArg is a single command-line argument passed to the child process.
type ProcessArg string

// spawn creates a context-bound process for name/args. realSpawn is the
// production implementation; tests substitute a fake.
type spawn func(ctx context.Context, name ProcessName, args []ProcessArg) process

// realSpawn builds an *exec.Cmd configured for shell-pipe cancellation
// semantics: SIGTERM on context cancel, escalating to SIGKILL after a grace
// period, with stderr passed through to the parent.
func realSpawn(ctx context.Context, name ProcessName, args []ProcessArg) process {
	executable := string(name)
	cmd := exec.CommandContext(ctx, executable, argStrings(args)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = sigtermGracePeriod
	cmd.Stderr = os.Stderr
	return cmd
}

// argStrings converts typed child-process arguments to the plain strings
// os/exec expects.
func argStrings(args []ProcessArg) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = string(a)
	}
	return out
}

// Subprocess creates a Command that forks an external process, streaming the
// input stream to the child's stdin while capturing the child's stdout as the
// output stream. Both directions run concurrently, exactly like a process in a
// shell pipeline: the child starts immediately and produces output while input
// is still arriving, so memory stays bounded regardless of input size.
//
// Stderr passes through to the parent's stderr. Non-zero exit codes propagate
// as errors. A downstream stop (the SIGPIPE analogue) or context cancellation
// closes the pipes and sends SIGTERM, escalating to SIGKILL after a grace
// period — so `subprocess | Take(3)` terminates the child promptly.
//
// If the child stops reading stdin early (e.g. head -1 exiting), the upstream
// is STOPPED and its remaining output discarded — so even `infinite | child`
// terminates, exactly as `yes | head -1` does — and the pipeline result is
// determined by the child's exit code, mirroring shell SIGPIPE semantics.
//
// This pattern is reserved for commands that wrap irreplaceable external tools
// (e.g., perl, git). Per constitution INV-1, subprocess usage is discouraged —
// if the algorithm can be implemented in pure Go, it must be.
//
// Example — building a Perl command:
//
//	func Perl(script string) gloo.Command[[]byte, []byte] {
//	    return patterns.Subprocess("perl", "-e", patterns.ProcessArg(script))
//	}
func Subprocess(name ProcessName, args ...ProcessArg) gloo.Command[[]byte, []byte] {
	return subprocessWith(realSpawn, name, args)
}

// subprocessWith is Subprocess with an injectable spawn, used by tests. The
// child runs under the producer scope (pctx), so both a downstream Stop and an
// external cancellation tear it down.
func subprocessWith(sp spawn, name ProcessName, args []ProcessArg) gloo.Command[[]byte, []byte] {
	return gloo.FuncCommand[[]byte, []byte](func(ctx context.Context, in gloo.Stream[[]byte]) gloo.Stream[[]byte] {
		return gloo.GenerateFrom(ctx, in, func(pctx context.Context, send func([]byte) bool, sendErr func(error)) {
			subprocess{spawn: sp, ctx: pctx, name: name, args: args, in: in, send: send, sendErr: sendErr}.run()
		})
	})
}

// subprocess owns one execution of a child process. It is an immutable value:
// setup steps (start, openPipes) return an UPDATED copy carrying the process
// handle and pipes rather than mutating in place.
type subprocess struct {
	ctx     context.Context
	in      gloo.Stream[[]byte]
	proc    process
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	spawn   spawn
	send    func([]byte) bool
	sendErr func(error)
	name    ProcessName
	args    []ProcessArg
}

// run starts the child and pumps data. On a setup failure it simply returns:
// GenerateFrom discards the input stream (stop + drain) once the producer
// returns, so the upstream is stopped rather than drained forever.
func (s subprocess) run() {
	started, ok := s.start()
	if !ok {
		return
	}
	started.pump()
}

// start spawns the process, opens its pipes, and launches it, returning the
// subprocess value carrying the process handle and pipes.
func (s subprocess) start() (subprocess, bool) {
	s.proc = s.spawn(s.ctx, s.name, s.args)
	piped, ok := s.openPipes()
	if !ok {
		return piped, false
	}
	return piped, piped.launch()
}

// openPipes wires the child's stdin and stdout, reporting the first failure.
func (s subprocess) openPipes() (subprocess, bool) {
	stdin, err := s.proc.StdinPipe()
	if err != nil {
		s.sendErr(ErrSubprocessStdinPipe.With(err, "process", s.name))
		return s, false
	}
	stdout, err := s.proc.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		s.sendErr(ErrSubprocessStdoutPipe.With(err, "process", s.name))
		return s, false
	}
	s.stdin, s.stdout = stdin, stdout
	return s, true
}

// launch starts the child, closing the pipes if it fails to start.
func (s subprocess) launch() bool {
	if err := s.proc.Start(); err != nil {
		s.closePipes()
		s.sendErr(ErrSubprocessStart.With(err, "process", s.name))
		return false
	}
	return true
}

// pump feeds stdin and scans stdout concurrently, then reports any errors from
// the input stream, the scan, and the child's exit status.
func (s subprocess) pump() {
	inErrCh := make(chan error, 1)
	go func() { inErrCh <- feedStdin(s.in, s.stdin) }()
	stop := s.watchCancel()
	scanErr := s.scan()
	close(stop)
	s.report(<-inErrCh, scanErr, s.proc.Wait())
}

// watchCancel closes the pipes on context cancellation (downstream Stop or
// external cancel) so the scanner and stdin feeder unblock even when a
// grandchild keeps the pipes open. It returns a channel to close on a normal
// finish.
func (s subprocess) watchCancel() chan struct{} {
	stop := make(chan struct{})
	go func() {
		select {
		case <-s.ctx.Done():
			s.closePipes()
		case <-stop:
		}
	}()
	return stop
}

// closePipes tears down both pipe ends (best-effort).
func (s subprocess) closePipes() {
	_ = s.stdin.Close()
	_ = s.stdout.Close()
}

// scan reads stdout line by line, emitting each line. A downstream stop (send
// returns false) or a scan error closes stdout so the child dies of EPIPE
// rather than blocking on a write.
func (s subprocess) scan() error {
	scanner := gloo.NewLineScanner(s.stdout)
	for scanner.Scan() {
		if !s.send(append([]byte(nil), scanner.Bytes()...)) {
			_ = s.stdout.Close()
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		_ = s.stdout.Close()
		return err
	}
	return nil
}

// report surfaces the three possible failures. When the producer scope was
// cancelled (downstream Stop or external cancel), all fallout is teardown noise
// — Generate reports external cancellation itself — so report nothing.
func (s subprocess) report(inErr, scanErr, waitErr error) {
	if s.ctx.Err() != nil {
		return
	}
	s.reportRaw(inErr)
	s.reportWrapped(ErrSubprocessReadStdout, scanErr)
	s.reportWrapped(ErrSubprocess, waitErr)
}

// reportRaw forwards an upstream stream error unchanged.
func (s subprocess) reportRaw(err error) {
	if err != nil {
		s.sendErr(err)
	}
}

// reportWrapped forwards err wrapped in sentinel with the process name.
func (s subprocess) reportWrapped(sentinel gloo.Error, err error) {
	if err != nil {
		s.sendErr(sentinel.With(err, "process", s.name))
	}
}

// feedStdin writes each input item to the child's stdin, newline-terminated.
// Each line is written immediately (no user-space buffering) so a streaming
// child sees input as it arrives; the kernel pipe buffer provides batching.
// Write failures are not reported — they mean the child stopped reading (e.g.
// head -1 exiting), which a shell silences via SIGPIPE. Once writing stops —
// the child is gone, or the upstream errored — the input is DISCARDED (stopped
// and drained, the SIGPIPE analogue), so even an unbounded upstream terminates
// exactly as `yes | head -1` does in a shell. The first upstream error, if
// any, is returned.
func feedStdin(in gloo.Stream[[]byte], stdin io.WriteCloser) error {
	defer func() { _ = stdin.Close() }()
	f := stdinFeeder{stdin: stdin, isWriting: true}
	for item := range in.Chan() {
		f = f.feed(item)
		if !f.isWriting {
			in.Discard()
			break
		}
	}
	return f.inErr
}

// stdinFeeder carries the per-line state of feedStdin. It is an immutable
// value: each step returns the updated feeder.
type stdinFeeder struct {
	stdin     io.WriteCloser
	inErr     error
	line      []byte
	isWriting bool
}

// feed handles one stream item: an upstream error stops writing; a value is
// written. The caller stops feeding (and discards the input) as soon as
// isWriting drops, so feed never sees an item after writing stopped.
func (f stdinFeeder) feed(item rill.Try[[]byte]) stdinFeeder {
	if item.Error != nil {
		return f.recordErr(item.Error)
	}
	return f.write(item.Value)
}

// recordErr records the first upstream error and stops further writes; the
// caller then discards the remaining input.
func (f stdinFeeder) recordErr(err error) stdinFeeder {
	if f.inErr == nil {
		f.inErr = err
	}
	f.isWriting = false
	return f
}

// write sends one newline-terminated line, disabling further writes if the
// child has stopped reading.
func (f stdinFeeder) write(value []byte) stdinFeeder {
	f.line = append(f.line[:0], value...)
	f.line = append(f.line, '\n')
	if _, err := f.stdin.Write(f.line); err != nil {
		f.isWriting = false
	}
	return f
}
