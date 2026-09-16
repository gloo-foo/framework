package gloo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/destel/rill"
)

// waitClosed fails the test if done is not closed within a short deadline. Used
// to assert that a producer goroutine actually returned (no leak) after a stop
// or cancellation, deterministically and without goroutine-count heuristics.
func waitClosed(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("producer goroutine did not exit after %s", what)
	}
}

func TestGenerate_NaturalClose(t *testing.T) {
	s := Generate(context.Background(), func(_ context.Context, send func(int) bool, _ func(error)) {
		for i := 1; i <= 3; i++ {
			if !send(i) {
				return
			}
		}
	})
	got, err := s.Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("got %v, want [1 2 3]", got)
	}
}

// Downstream stop is the SIGPIPE analogue: an infinite producer must exit
// silently (no error item) once the consumer stops reading. This is a white-box
// test of the internal teardown: it drives the unexported stop directly and
// inspects the remainder, which the public Discard (stop + drain) hides.
func TestGenerate_DownstreamStopIsSilentAndTerminates(t *testing.T) {
	done := make(chan struct{})
	s := Generate(context.Background(), func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(done)
		for i := 0; ; i++ { // infinite
			if !send(i) {
				return
			}
		}
	})

	// Read a couple of items, then stop and drain, inspecting the remainder.
	ch := s.Chan()
	<-ch
	<-ch
	s.stop()
	sawErr := false
	for item := range ch { // drain the buffered remainder
		if item.Error != nil {
			sawErr = true
		}
	}
	if sawErr {
		t.Error("downstream stop must be graceful — no error item")
	}
	waitClosed(t, done, "downstream stop")
}

// External cancellation is the ^C analogue: the producer surfaces the context
// cause exactly once, then closes.
func TestGenerate_ExternalCancelSurfacesCauseOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s := Generate(ctx, func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(done)
		for i := 0; ; i++ {
			if !send(i) {
				return
			}
		}
	})

	ch := s.Chan()
	<-ch
	cancel()

	errCount := 0
	var lastErr error
	for item := range ch {
		if item.Error != nil {
			errCount++
			lastErr = item.Error
		}
	}
	if errCount != 1 {
		t.Fatalf("external cancellation must surface exactly one error, got %d", errCount)
	}
	if !errors.Is(lastErr, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", lastErr)
	}
	waitClosed(t, done, "external cancel")
}

func TestGenerate_ProducerErrorThenNoSynthesizedCancelError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wantErr := errors.New("boom")
	s := Generate(ctx, func(pctx context.Context, _ func(int) bool, sendErr func(error)) {
		cancel()
		<-pctx.Done() // the scope is cancelled BEFORE the producer errors...
		sendErr(wantErr)
	})
	// ...so without the errored-producer guard, surfaceCancellation would pile
	// a synthesized context.Canceled on top. Walk the whole channel and demand
	// exactly the producer's error.
	var errs []error
	for item := range s.Chan() {
		if item.Error != nil {
			errs = append(errs, item.Error)
		}
	}
	if len(errs) != 1 || !errors.Is(errs[0], wantErr) {
		t.Fatalf("got %v, want exactly the producer's own error", errs)
	}
}

func TestStreamOf_RoundTrips(t *testing.T) {
	got, err := StreamOf("a", "b", "c").Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[1] != "b" {
		t.Errorf("got %v", got)
	}
}

// WrapFrom must chain teardown to the upstream stream: discarding the derived
// stream tears the upstream producer down (the SIGPIPE analogue across a pure
// transform boundary).
func TestWrapFrom_ForwardsTeardownToUpstream(t *testing.T) {
	done := make(chan struct{})
	upstream := Generate(context.Background(), func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(done)
		for i := 0; ; i++ {
			if !send(i) {
				return
			}
		}
	})
	derived := WrapFrom(upstream.Chan(), upstream)

	<-derived.Chan() // pull one item so the producer is running
	derived.Discard()
	waitClosed(t, done, "WrapFrom downstream discard")
}

// Wrap builds an origin stream with no upstream; discarding it must be safe even
// when there is nothing to tear down.
func TestWrap_OriginDiscardIsSafe(_ *testing.T) {
	ch := make(chan rill.Try[int])
	close(ch)
	Wrap(ch).Discard() // must not panic
}

// WrapFrom over an ORIGIN stream (StreamOf has no teardown handle) is a plain
// pass-through: the derived stream carries no stop, collects normally, and is
// safe to Discard. This is the nil-upstream branch of WrapFrom.
func TestWrapFrom_OverOriginHasNoTeardown(t *testing.T) {
	origin := StreamOf(1, 2, 3) // Wrap-backed: stop is nil
	doubled := rill.OrderedMap(origin.Chan(), 1, func(v int) (int, error) { return v * 2, nil })
	derived := WrapFrom(doubled, origin)

	got, err := derived.Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 || got[0] != 2 || got[2] != 6 {
		t.Errorf("got %v, want [2 4 6]", got)
	}
	derived.Discard() // safe no-op: nothing upstream to tear down
}

// WrapFrom's teardown forward must be idempotent: discarding the derived stream
// repeatedly runs the upstream teardown exactly once.
func TestWrapFrom_TeardownIdempotent(t *testing.T) {
	calls := 0
	closed := make(chan rill.Try[int])
	close(closed)
	upstream := Stream[int]{ch: closed, stop: func() { calls++ }}

	derived := WrapFrom(closed, upstream)
	derived.Discard()
	derived.Discard()
	derived.Discard()

	if calls != 1 {
		t.Errorf("upstream teardown must run once, ran %d times", calls)
	}
}

// Collect abandons the upstream (stop + drain) on the first error so an infinite
// producer behind the error cannot leak.
func TestStreamCollect_StopsAndDrainsOnError(t *testing.T) {
	done := make(chan struct{})
	wantErr := errors.New("mid-stream failure")
	s := Generate(context.Background(), func(_ context.Context, send func(int) bool, sendErr func(error)) {
		defer close(done)
		if !send(1) {
			return
		}
		sendErr(wantErr)
		// Keep trying to produce after the error; Collect must stop us.
		for i := 0; ; i++ {
			if !send(i) {
				return
			}
		}
	})
	_, err := s.Collect()
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
	waitClosed(t, done, "Collect error drain")
}

// Regression: on external cancellation the post-loop error send must not block
// forever even when the upstream is torn down without a full drain. Driving the
// unexported stop directly (the dangerous case the public API no longer allows)
// proves the producer still exits, because sendErr also selects on the closed
// stopped signal.
func TestGenerate_CancelThenTeardownDoesNotLeak(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s := Generate(ctx, func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(done)
		for i := 0; ; i++ {
			if !send(i) {
				return
			}
		}
	})

	// Fill the buffer, then cancel and tear down without draining.
	time.Sleep(20 * time.Millisecond) // let the producer fill the buffered channel
	cancel()
	s.stop() // teardown closes the stopped signal so the error send cannot block

	waitClosed(t, done, "cancel + teardown without drain")
}

// --- Generate: error abandoned once the consumer has stopped ---

func TestGenerateSendErrAbandonedAfterStop(t *testing.T) {
	stopDone := make(chan struct{})
	filled := make(chan struct{})
	done := make(chan struct{})
	s := Generate(context.Background(), func(_ context.Context, send func(int) bool, sendErr func(error)) {
		defer close(done)
		for i := 0; i < streamBuffer; i++ {
			send(i) // fill the buffer so a later sendErr cannot enqueue
		}
		close(filled) // all slots taken: a later sendErr cannot enqueue
		<-stopDone
		sendErr(errors.New("abandoned")) // buffer full + stopped → <-stopped branch
	})
	<-filled // ensure the buffer is full BEFORE stopping, so sendErr is dropped
	s.stop()
	close(stopDone)
	// The producer must NOT block on the abandoned sendErr: the <-stopped branch
	// lets it return. Prove that by waiting for its goroutine to exit BEFORE we
	// drain, keeping the buffer full so the abandon branch is taken.
	waitClosed(t, done, "abandoned sendErr after stop")
	for item := range s.Chan() {
		if item.Error != nil {
			t.Fatalf("abandoned error surfaced after stop: %v", item.Error)
		}
	}
}

func TestGenerateSendErrNilIsNoop(t *testing.T) {
	got, err := Generate(context.Background(), func(_ context.Context, send func(int) bool, sendErr func(error)) {
		sendErr(nil) // no-op: nil errors are ignored
		send(1)
	}).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("got %v, want [1]", got)
	}
}

// GenerateFrom must release the upstream when its producer returns early
// WITHOUT calling in.Discard(): the auto-discard is what makes the documented
// "one Discard collapses the whole chain" promise hold over stages that have no
// cancellation hook of their own (a bare channel behind Wrap, a pure rill
// transform behind WrapFrom). The sender here is such a stage: it exits only if
// something drains its unread items.
func TestGenerateFrom_EarlyReturnReleasesNonCancellableUpstream(t *testing.T) {
	senderDone := make(chan struct{})
	ch := make(chan rill.Try[int])
	go func() {
		defer close(senderDone)
		defer close(ch)
		for i := 0; i < 100; i++ {
			ch <- rill.Try[int]{Value: i}
		}
	}()
	out := GenerateFrom(context.Background(), Wrap(ch), func(_ context.Context, send func(int) bool, _ func(error)) {
		item, ok := <-ch
		if !ok || item.Error != nil {
			return
		}
		send(item.Value) // read ONE item, then return without Discard
	})
	got, err := out.Collect()
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v err=%v, want one item", got, err)
	}
	waitClosed(t, senderDone, "GenerateFrom early return over non-cancellable upstream")
}

// GenerateFrom must stop a cancellation-aware upstream when its producer
// returns early and the downstream consumes the (already complete) output to
// natural close — the consumer never calls Discard, so only the auto-discard
// can stop the infinite origin.
func TestGenerateFrom_EarlyReturnStopsInfiniteUpstream(t *testing.T) {
	done := make(chan struct{})
	origin := Generate(context.Background(), func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(done)
		i := 0
		for send(i) {
			i++
		}
	})
	out := GenerateFrom(context.Background(), origin, func(_ context.Context, send func(int) bool, _ func(error)) {
		item, ok := <-origin.Chan()
		if ok && item.Error == nil {
			send(item.Value)
		}
	})
	if got, err := out.Collect(); err != nil || len(got) != 1 {
		t.Fatalf("got %v err=%v, want one item", got, err)
	}
	waitClosed(t, done, "GenerateFrom early return over infinite origin")
}

// send and sendErr must be safe for concurrent use by goroutines the producer
// spawns (and joins before returning): the errored marker is shared state.
// Run under -race this pins the atomicity of that marker.
func TestGenerate_ConcurrentSendAndSendErrAreRaceFree(t *testing.T) {
	boom := errors.New("boom")
	s := Generate(context.Background(), func(_ context.Context, send func(int) bool, sendErr func(error)) {
		done := make(chan struct{}, 4)
		for i := 0; i < 2; i++ {
			go func() { sendErr(boom); done <- struct{}{} }()
			go func() { send(1); done <- struct{}{} }()
		}
		for i := 0; i < 4; i++ {
			<-done // join every sender before returning
		}
	})
	values, errs := 0, 0
	for item := range s.Chan() {
		if item.Error != nil {
			errs++
			continue
		}
		values++
	}
	if values != 2 || errs != 2 {
		t.Errorf("got %d values and %d errors, want 2 and 2", values, errs)
	}
}
