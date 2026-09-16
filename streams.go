package gloo

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/destel/rill"
)

// streamBuffer gives each producer-backed stage a small channel buffer,
// mirroring the ~64KB kernel buffer behind a shell pipe. It is a throughput
// win with no semantic change: teardown still propagates, just modulo the items
// already buffered (exactly like a shell, where head can read a little past
// what it prints before the producer sees SIGPIPE).
const streamBuffer = 64

// ErrStopReading is the cancellation cause used when a downstream consumer
// stops reading — the SIGPIPE analogue. A producer cancelled with this cause
// closes its stream SILENTLY: no error item is emitted, because downstream
// completion is graceful, not a failure (a shell without pipefail does not
// fail a pipeline when `head` exits early).
const ErrStopReading Error = "gloo: downstream stopped reading"

// producerFunc emits values via send and errors via sendErr. send returns
// false once the consumer has stopped (via Stream.Discard) or the context was
// cancelled; the producer MUST return promptly when it does, exactly as a
// shell tool dies on SIGPIPE.
//
// send and sendErr are safe for concurrent use by goroutines the producer
// spawns, but MUST NOT be called after the producer returns — the stream
// closes at that point. A producer that fans out must join every goroutine
// before returning.
type producerFunc[T any] func(ctx context.Context, send func(T) bool, sendErr func(error))

// Generate runs a cancellation-aware ORIGIN producer (a Source, with no
// upstream) and returns its Stream. It is the primitive every origin producer
// is built on. For a producer derived from an input stream, use GenerateFrom so
// teardown chains upstream.
//
// Teardown semantics, derived from context.Cause of the producer scope:
//
//   - Downstream stop (cause == ErrStopReading): close silently.
//   - External cancellation (any other cause, e.g. ^C / deadline): emit the
//     cause once — unless the producer already emitted an error — then close.
//   - Natural completion: close.
//
// The returned Stream's Discard cancels this producer scope with ErrStopReading.
func Generate[T any](ctx context.Context, producer producerFunc[T]) Stream[T] {
	return generate(ctx, nil, producer)
}

// GenerateFrom is Generate for a producer that derives its output from an
// upstream stream. A downstream Discard cancels this producer AND tears the
// upstream down, so one Discard collapses the whole chain (upstream only).
// Authors pass the INPUT stream rather than a loose stop handle, so the safe
// teardown is the only thing in reach.
//
// When the producer returns, the upstream is automatically discarded (stopped
// and drained), so a producer that returns early — after an error, or once it
// has read all it needs — cannot strand an upstream stage, even one with no
// cancellation hook of its own (a pure rill transform, a bare Wrap'd channel).
// An explicit upstream.Discard() inside the producer remains correct and
// idempotent. The upstream must not be read after the producer returns.
func GenerateFrom[In, Out any](ctx context.Context, upstream Stream[In], producer producerFunc[Out]) Stream[Out] {
	return generate(ctx, upstream.stop, func(pctx context.Context, send func(Out) bool, sendErr func(error)) {
		defer upstream.Discard()
		producer(pctx, send, sendErr)
	})
}

// generate is the shared implementation behind Generate and GenerateFrom.
func generate[T any](ctx context.Context, upstreamStop func(), producer producerFunc[T]) Stream[T] {
	g := newGenerator[T](ctx)
	go g.run(producer)
	return Stream[T]{ch: g.ch, stop: g.stopper(upstreamStop)}
}

// generator holds the producer-goroutine state for one generate call. It is a
// value-receiver handle: every field is a reference (channels, context, cancel
// func, a shared error marker), so copies observe the same producer state.
type generator[T any] struct {
	ch         chan rill.Try[T]
	pctx       context.Context
	cancel     context.CancelCauseFunc
	stopped    chan struct{}
	hasErrored *atomic.Bool
}

func newGenerator[T any](ctx context.Context) generator[T] {
	pctx, cancel := context.WithCancelCause(ctx)
	return generator[T]{
		ch:         make(chan rill.Try[T], streamBuffer),
		pctx:       pctx,
		cancel:     cancel,
		stopped:    make(chan struct{}),
		hasErrored: new(atomic.Bool),
	}
}

// run drives the producer, surfaces external cancellation, then closes ch.
func (g generator[T]) run(producer producerFunc[T]) {
	defer close(g.ch)
	producer(g.pctx, g.send, g.sendErr)
	g.surfaceCancellation()
}

// send delivers a value, reporting false once the consumer has stopped or the
// context was cancelled.
func (g generator[T]) send(v T) bool {
	select {
	case g.ch <- rill.Try[T]{Value: v}:
		return true
	case <-g.pctx.Done():
		return false
	}
}

// sendErr delivers an error, abandoning it if the consumer has walked away so
// the producer never blocks forever on a send nobody reads.
func (g generator[T]) sendErr(err error) {
	if err == nil {
		return
	}
	select {
	case g.ch <- rill.Try[T]{Error: err}:
		g.hasErrored.Store(true)
	case <-g.stopped:
	}
}

// surfaceCancellation emits an external cancellation cause exactly once. A
// downstream stop is graceful and stays silent; a producer that already
// errored is not piled on.
func (g generator[T]) surfaceCancellation() {
	if g.hasErrored.Load() {
		return
	}
	cause := context.Cause(g.pctx)
	if cause == nil || errors.Is(cause, ErrStopReading) {
		return
	}
	select {
	case g.ch <- rill.Try[T]{Error: cause}:
	case <-g.stopped:
	}
}

// stopper returns the idempotent teardown handle: it cancels the producer scope
// with ErrStopReading, unblocks error delivery, and propagates upstream.
func (g generator[T]) stopper(upstreamStop func()) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			g.cancel(ErrStopReading)
			close(g.stopped)
			if upstreamStop != nil {
				upstreamStop()
			}
		})
	}
}

// Wrap adapts a standalone channel (an ORIGIN, with no upstream to tear down)
// into a Stream. StreamOf and a FuncCommand that synthesizes a channel from
// nothing use it. A transform that wraps a rill stage over an input stream must
// use WrapFrom so teardown chains upstream.
func Wrap[T any](ch <-chan rill.Try[T]) Stream[T] {
	return Stream[T]{ch: ch}
}

// WrapFrom adapts the output channel of a pure rill transform into a Stream,
// chaining teardown to the upstream stream so a downstream Discard propagates.
// A pure transform has no producer scope of its own — it terminates when its
// input closes — so forwarding teardown is all that is required. The forward is
// guarded so it stays idempotent however many times it fires.
//
//	out := rill.OrderedMap(in.Chan(), 1, fn)
//	return gloo.WrapFrom(out, in)
func WrapFrom[In, Out any](ch <-chan rill.Try[Out], upstream Stream[In]) Stream[Out] {
	if upstream.stop == nil {
		return Stream[Out]{ch: ch}
	}
	var once sync.Once
	return Stream[Out]{ch: ch, stop: func() { once.Do(upstream.stop) }}
}

// StreamOf builds a finished Stream from in-memory values. It is the canonical
// way to feed synthetic input to a command in a test — the framework analogue
// of a shell here-string — and backs SliceSource.
func StreamOf[T any](items ...T) Stream[T] {
	return Wrap(rill.FromSlice(items, nil))
}
