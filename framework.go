package gloo

import (
	"context"

	"github.com/destel/rill"
)

// Stream is the framework's pipe: a channel of Try containers (each holding a
// value or an error) carrying a teardown handle.
//
// Teardown is the SIGPIPE analogue. In a shell, when a downstream stage exits
// the pipe closes and the next upstream write dies of SIGPIPE, terminating the
// producer. Stream reproduces that — but makes the safe path the ONLY path. A
// consumer has exactly three operations, all leak-free: range Chan() to
// completion, Collect() every item, or Discard() to abandon early. There is
// deliberately no bare "stop": stopping the upstream without also draining
// would strand a producer blocked on a send nobody reads (a goroutine leak), so
// the framework fuses the two into Discard and never exposes the unsafe half.
// Teardown propagates UPSTREAM only, so `infinite | Take(3) | sort` still sorts
// the three items.
//
// Command authors using a pattern from patterns/ never touch the channel or
// teardown. Authors writing an exotic FuncCommand build output with
// GenerateFrom/WrapFrom, passing the INPUT stream so teardown chains to it —
// they hold no loose stop handle either.
type Stream[T any] struct {
	ch   <-chan rill.Try[T]
	stop func()
}

// Chan returns the underlying channel for direct rill interop. Pattern plumbing
// hides this; only FuncCommand authors handling cases no pattern covers use it.
// A consumer that ranges Chan() must read it to completion or hand the stream to
// Discard — never abandon it half-read.
func (s Stream[T]) Chan() <-chan rill.Try[T] { return s.ch }

// Collect drains the stream into a slice. On the first error it abandons the
// rest via Discard (stop + drain) so no producer goroutine leaks, and returns
// the error. It is the canonical terminal for tests and simple consumers.
func (s Stream[T]) Collect() ([]T, error) {
	var out []T
	for item := range s.ch {
		if item.Error != nil {
			s.Discard()
			return nil, item.Error
		}
		out = append(out, item.Value)
	}
	return out, nil
}

// Discard abandons a stream you will not finish consuming. It signals every
// upstream producer to stop and drains the channel, releasing each one —
// including pure rill transform stages that have no cancellation hook and would
// otherwise block on a send nobody reads. It is idempotent and is the ONLY way
// to walk away from a partially-consumed stream; the framework exposes no
// stop-without-drain precisely because that combination leaks.
func (s Stream[T]) Discard() {
	if s.stop != nil {
		s.stop()
	}
	s.drain()
}

// drain receives and discards remaining items so a stopped upstream producer
// can run to completion instead of blocking on a send nobody is reading.
func (s Stream[T]) drain() {
	for {
		if _, ok := <-s.ch; !ok {
			return
		}
	}
}

// Command transforms an input stream to an output stream.
type Command[In, Out any] interface {
	Execute(ctx context.Context, input Stream[In]) Stream[Out]
}

// Source produces a stream from external data.
type Source[Out any] interface {
	Stream(ctx context.Context) Stream[Out]
}

// Sink consumes a stream and produces a typed result.
type Sink[In, Res any] interface {
	Consume(ctx context.Context, input Stream[In]) (Res, error)
}
