package verification

// Integration tests for the shell-pipeline cancellation contract
// (specs/005-pipeline-cancellation). Each test maps to a row of the target
// semantics table: termination, boundedness, downstream survival, graceful vs.
// error close, mid-stream cancellation, and chained stops.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

// infiniteSource emits 0,1,2,… forever. emitted tracks how many values were
// produced (boundedness), and done closes when the producer goroutine exits
// (no leak).
type infiniteSource struct {
	emitted *int64
	done    chan struct{}
}

func newInfinite() infiniteSource {
	return infiniteSource{emitted: new(int64), done: make(chan struct{})}
}

func (s infiniteSource) Stream(ctx context.Context) gloo.Stream[int] {
	return gloo.Generate(ctx, func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(s.done)
		for i := 0; ; i++ {
			atomic.AddInt64(s.emitted, 1)
			if !send(i) {
				return
			}
		}
	})
}

func mustExit(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("upstream goroutine leaked: did not exit after %s", what)
	}
}

// Row 1+2: `yes | head -3` terminates instantly AND reads only a small prefix.
func TestCancel_TakeTerminatesAndBoundsInfiniteSource(t *testing.T) {
	src := newInfinite()
	got, err := patterns.Take[int](3).
		Execute(context.Background(), src.Stream(context.Background())).
		Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %v, want 3 items", got)
	}
	mustExit(t, src.done, "Take(3)")
	if n := atomic.LoadInt64(src.emitted); n > 10_000 {
		t.Errorf("source over-produced %d items — not bounded", n)
	}
}

// Row 5: stages downstream of the stopper run to completion.
// `seq inf | head -3 | sort` prints three sorted lines.
func TestCancel_DownstreamSurvivesUpstreamStop(t *testing.T) {
	src := newInfinite()
	pipeline := gloo.Compose[int](patterns.Take[int](3)).To(sortInts())

	got, err := pipeline.
		Execute(context.Background(), src.Stream(context.Background())).
		Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 0 || got[2] != 2 {
		t.Errorf("got %v, want [0 1 2]", got)
	}
	mustExit(t, src.done, "Take(3) | sort")
}

// Row 3: a downstream-initiated stop is graceful — no error item reaches the
// consumer.
func TestCancel_DownstreamStopIsGraceful(t *testing.T) {
	src := newInfinite()
	stream := patterns.Take[int](2).Execute(context.Background(), src.Stream(context.Background()))

	var sawErr bool
	for item := range stream.Chan() {
		if item.Error != nil {
			sawErr = true
		}
	}
	if sawErr {
		t.Error("downstream stop must not surface an error")
	}
	mustExit(t, src.done, "graceful stop")
}

// Row 4: external cancellation tears down every stage and surfaces ctx.Err()
// exactly once at the sink.
func TestCancel_ExternalCancellationSurfacesOnce(t *testing.T) {
	src := newInfinite()
	ctx, cancel := context.WithCancel(context.Background())

	// A pass-through then a sink-side count; cancel after a few reads.
	stream := patterns.Take[int](1_000_000). // effectively unbounded for this test
							Execute(ctx, src.Stream(ctx))

	ch := stream.Chan()
	<-ch // ensure flowing
	cancel()

	errCount := 0
	var last error
	for item := range ch {
		if item.Error != nil {
			errCount++
			last = item.Error
		}
	}
	if errCount != 1 {
		t.Fatalf("external cancel must surface exactly one error, got %d", errCount)
	}
	if !errors.Is(last, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", last)
	}
	stream.Discard()
	mustExit(t, src.done, "external cancel")
}

// Mid-stream cancellation of a long single source unblocks promptly (the
// per-line cancellation guarantee — no waiting for a file/line boundary).
func TestCancel_MidStreamUnblocksPromptly(t *testing.T) {
	src := newInfinite()
	ctx, cancel := context.WithCancel(context.Background())
	stream := src.Stream(ctx)

	<-stream.Chan()
	cancel()
	// Drain to completion: the channel closes only once the producer goroutine
	// has observed the cancellation and returned. The decorative
	// "elapsed > time.Second" window proved nothing; mustExit below is the real,
	// deterministic proof that cancellation unblocked the producer promptly
	// (it fails if the goroutine has not exited within a bounded deadline).
	for {
		if _, ok := <-stream.Chan(); !ok {
			break
		}
	}
	mustExit(t, src.done, "mid-stream cancel")
}

// Chained stops are idempotent: Take(5) | Take(3) yields three items and the
// source exits once.
func TestCancel_ChainedStopsAreIdempotent(t *testing.T) {
	src := newInfinite()
	pipeline := gloo.Compose[int](patterns.Take[int](5)).To(patterns.Take[int](3))

	got, err := pipeline.
		Execute(context.Background(), src.Stream(context.Background())).
		Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("got %v, want 3 items", got)
	}
	mustExit(t, src.done, "Take(5) | Take(3)")
}

// An unconsumed lazy chain starts no goroutines and leaks nothing.
func TestCancel_UnconsumedChainLeaksNothing(t *testing.T) {
	src := newInfinite()
	// Build a fluent chain but never run a terminal.
	_ = gloo.Chain(src).To(patterns.Take[int](3))
	// The source must NOT have started producing.
	select {
	case <-src.done:
		t.Error("unconsumed chain started (and finished) the source producer")
	case <-time.After(100 * time.Millisecond):
		// expected: nothing happened
	}
	if n := atomic.LoadInt64(src.emitted); n != 0 {
		t.Errorf("unconsumed chain produced %d items — wiring is not lazy", n)
	}
}

// Stop must chain through pure transform stages (Map, Filter) and Pipe — a
// Take at the end of `infinite | map | filter | take` tears the source down.
func TestCancel_StopChainsThroughPureTransforms(t *testing.T) {
	src := newInfinite()

	double := patterns.Map(func(n int) (int, error) { return n * 2, nil })
	keepAll := patterns.Filter(func(int) (bool, error) { return true, nil })
	// double | keepAll | Take(3), composed via Pipe (type-changing binary form).
	pipeline := gloo.Pipe(gloo.Pipe(double, keepAll), patterns.Take[int](3))

	got, err := pipeline.
		Execute(context.Background(), src.Stream(context.Background())).
		Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 0 || got[1] != 2 || got[2] != 4 {
		t.Errorf("got %v, want [0 2 4]", got)
	}
	mustExit(t, src.done, "infinite | map | filter | Take(3)")
}

// sortInts is a tiny Accumulate used as a downstream stage.
func sortInts() gloo.Command[int, int] {
	return patterns.Accumulate(func(xs []int) ([]int, error) {
		for i := 1; i < len(xs); i++ {
			for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
				xs[j-1], xs[j] = xs[j], xs[j-1]
			}
		}
		return xs, nil
	})
}
