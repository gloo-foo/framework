package verification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/destel/rill"

	gloo "github.com/gloo-foo/framework"
)

// genSource is a Source whose producer goroutine sends n items (via
// gloo.Generate) and closes done when the generator returns. The producer
// honors the send bool, returning as soon as the stream is stopped; if a
// downstream terminal abandoned the stream without draining or stopping it,
// the producer would block forever on send and done would never close —
// letting tests detect the goroutine leak via a timeout.
type genSource struct {
	done chan struct{}
	n    int
}

func newGenSource(n int) *genSource {
	return &genSource{n: n, done: make(chan struct{})}
}

func (s *genSource) Stream(ctx context.Context) gloo.Stream[int] {
	return gloo.Generate(ctx, func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(s.done)
		for i := 0; i < s.n; i++ {
			if !send(i) {
				return
			}
		}
	})
}

// errAt returns a command that errors when it encounters the value target.
func errAt(target int) gloo.Command[int, int] {
	return gloo.FuncCommand[int, int](func(_ context.Context, in gloo.Stream[int]) gloo.Stream[int] {
		return gloo.WrapFrom(rill.OrderedMap(in.Chan(), 1, func(v int) (int, error) {
			if v == target {
				return 0, fmt.Errorf("boom at %d", v)
			}
			return v, nil
		}), in)
	})
}

func assertProducerDrained(t *testing.T, src *genSource) {
	t.Helper()
	select {
	case <-src.done:
		// Producer ran to completion — no goroutine leak.
	case <-time.After(2 * time.Second):
		t.Fatal("producer goroutine did not finish: stream was not drained after the error (goroutine leak)")
	}
}

// TestFluentCollectDrainsOnError verifies that Collect drains the remaining
// stream after surfacing an error, so the upstream producer goroutine finishes.
func TestFluentCollectDrainsOnError(t *testing.T) {
	src := newGenSource(100)

	_, err := gloo.Chain(src).To(errAt(2)).Collect()
	if err == nil {
		t.Fatal("expected error from pipeline, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected 'boom' error, got: %v", err)
	}
	assertProducerDrained(t, src)
}

// TestFluentForEachDrainsOnStreamError verifies ForEach drains the rest of the
// stream when an upstream stage produces an error.
func TestFluentForEachDrainsOnStreamError(t *testing.T) {
	src := newGenSource(100)

	err := gloo.Chain(src).To(errAt(2)).ForEach(func(int) error { return nil })
	if err == nil {
		t.Fatal("expected error from pipeline, got nil")
	}
	assertProducerDrained(t, src)
}

// TestFluentForEachDrainsOnCallbackError verifies ForEach drains the rest of the
// stream when the callback itself returns an error part-way through.
func TestFluentForEachDrainsOnCallbackError(t *testing.T) {
	src := newGenSource(100)

	err := gloo.Chain(src).ForEach(func(v int) error {
		if v == 2 {
			return fmt.Errorf("stop at %d", v)
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected error from callback, got nil")
	}
	assertProducerDrained(t, src)
}

// oneItemSink reads a single item and returns WITHOUT draining or discarding —
// the early-exit shape SinkContext's deferred Discard exists to make safe.
type oneItemSink struct{}

func (oneItemSink) Consume(_ context.Context, in gloo.Stream[int]) (int, error) {
	item, ok := <-in.Chan()
	if !ok {
		return 0, nil
	}
	return item.Value, item.Error
}

// A sink that returns early must not strand the producer: the fluent terminal
// owns the stream and must tear it down. Deleting SinkContext's deferred
// discardStream leaves the producer blocked forever and fails this test.
func TestFluentSinkEarlyReturnTearsDownProducer(t *testing.T) {
	src := newGenSource(1_000_000)
	got, err := gloo.Chain(src).Sink(oneItemSink{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.(int) != 0 {
		t.Fatalf("got %v, want the first item (0)", got)
	}
	assertProducerDrained(t, src)
}

// --- Reflection signature validation (sentinel errors on misuse — never a panic) ---

type badArityCmd struct{}

// Execute has the wrong number of return values (2 instead of 1).
func (badArityCmd) Execute(_ context.Context, in gloo.Stream[int]) (gloo.Stream[int], error) {
	return in, nil
}

type badAritySink struct{}

// Consume returns a single value instead of (Res, error).
func (badAritySink) Consume(_ context.Context, _ gloo.Stream[int]) int { return 0 }

func TestFluentToRejectsBadCommandSignature(t *testing.T) {
	src := newSliceSource([]int{1, 2, 3})
	_, err := gloo.Chain(src).To(badArityCmd{}).Collect()
	if !errors.Is(err, gloo.ErrNotCommand) {
		t.Errorf("got %v, want ErrNotCommand", err)
	}
}

func TestFluentSinkRejectsBadSinkSignature(t *testing.T) {
	src := newSliceSource([]int{1, 2, 3})
	_, err := gloo.Chain(src).Sink(badAritySink{})
	if !errors.Is(err, gloo.ErrNotSink) {
		t.Errorf("got %v, want ErrNotSink", err)
	}
}

func TestFluentForEachRejectsNonErrorReturn(t *testing.T) {
	src := newSliceSource([]int{1, 2, 3})
	err := gloo.Chain(src).ForEach(func(int) int { return 0 })
	if !errors.Is(err, gloo.ErrNotForEachFunc) {
		t.Errorf("got %v, want ErrNotForEachFunc", err)
	}
}

// concreteErr is a non-interface error type. A sink/function returning it can
// never report success (the value is never == nil), so the reflection layer
// must require the return type to be exactly the error interface, not merely
// assignable to it.
type concreteErr struct{}

func (concreteErr) Error() string { return "concrete" }

type concreteErrSink struct{}

func (concreteErrSink) Consume(_ context.Context, _ gloo.Stream[int]) (int, concreteErr) {
	return 0, concreteErr{}
}

func TestFluentSinkRejectsConcreteErrorReturn(t *testing.T) {
	src := newSliceSource([]int{1, 2, 3})
	_, err := gloo.Chain(src).Sink(concreteErrSink{})
	if !errors.Is(err, gloo.ErrNotSink) {
		t.Errorf("got %v, want ErrNotSink", err)
	}
}

func TestFluentForEachRejectsConcreteErrorReturn(t *testing.T) {
	src := newSliceSource([]int{1, 2, 3})
	err := gloo.Chain(src).ForEach(func(int) concreteErr { return concreteErr{} })
	if !errors.Is(err, gloo.ErrNotForEachFunc) {
		t.Errorf("got %v, want ErrNotForEachFunc", err)
	}
}
