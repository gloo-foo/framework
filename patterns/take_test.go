package patterns_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

// countUp is an unbounded source emitting 0,1,2,… It closes done when its
// producer goroutine returns, so tests can prove the upstream was torn down.
type countUp struct {
	done    chan struct{}
	maxSeen *int
}

func (c countUp) Stream(ctx context.Context) gloo.Stream[int] {
	return gloo.Generate(ctx, func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(c.done)
		for i := 0; ; i++ {
			if c.maxSeen != nil {
				*c.maxSeen = i
			}
			if !send(i) {
				return
			}
		}
	})
}

func waitClosed(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("upstream did not terminate after %s", what)
	}
}

func TestTake_FewerThanAvailable(t *testing.T) {
	got := run(t, []int{1, 2, 3, 4, 5}, patterns.Take[int](3))
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("got %v, want [1 2 3]", got)
	}
}

func TestTake_MoreThanAvailable(t *testing.T) {
	got := run(t, []int{1, 2}, patterns.Take[int](5))
	if len(got) != 2 {
		t.Errorf("Take(5) over 2 items = %v, want [1 2]", got)
	}
}

func TestTake_ZeroAndNegativeEmitNothing(t *testing.T) {
	for _, n := range []patterns.Count{0, -1} {
		got := run(t, []int{1, 2, 3}, patterns.Take[int](n))
		if len(got) != 0 {
			t.Errorf("Take(%d) = %v, want empty", n, got)
		}
	}
}

func TestTake_ReusableAcrossPipelines(t *testing.T) {
	take2 := patterns.Take[int](2)
	for range 2 {
		got := run(t, []int{9, 8, 7, 6}, take2)
		if len(got) != 2 || got[0] != 9 || got[1] != 8 {
			t.Errorf("got %v, want [9 8]", got)
		}
	}
}

func TestTake_UpstreamErrorPropagates(t *testing.T) {
	wantErr := errors.New("upstream broke")
	got, err := patterns.Take[string](5).
		Execute(context.Background(), errStream([]string{"a", "b"}, wantErr)).
		Collect()
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v (items %v), want %v", err, got, wantErr)
	}
}

// The headline guarantee: Take terminates an infinite source and tears it down.
func TestTake_TerminatesInfiniteSource(t *testing.T) {
	done := make(chan struct{})
	maxSeen := 0
	src := countUp{done: done, maxSeen: &maxSeen}

	got, err := patterns.Take[int](3).Execute(context.Background(), src.Stream(context.Background())).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 0 || got[2] != 2 {
		t.Errorf("got %v, want [0 1 2]", got)
	}
	waitClosed(t, done, "Take(3)")
	// Boundedness: the source produced only a little past what was taken
	// (modulo the stage buffer), nowhere near "infinite".
	if maxSeen > 10_000 {
		t.Errorf("source over-produced: maxSeen=%d", maxSeen)
	}
}

// Stages downstream of Take run to completion normally — sort (an Accumulate)
// can only emit if Take closes its output cleanly, exactly like
// `seq inf | head -3 | sort`.
func TestTake_DownstreamSurvives(t *testing.T) {
	done := make(chan struct{})
	// Emit an UNSORTED prefix (2,0,1) so a no-op sort would NOT yield [0,1,2];
	// only a real downstream sort produces the ascending result. The source then
	// keeps emitting (unbounded) so Take's early-stop teardown is still proven.
	src := unboundedPrefix{done: done, prefix: []int{2, 0, 1}}

	sortAsc := patterns.Accumulate(func(xs []int) ([]int, error) {
		sort.Ints(xs)
		return xs, nil
	})
	pipeline := gloo.Compose[int](patterns.Take[int](3)).To(sortAsc)

	got, err := pipeline.Execute(context.Background(), src.Stream(context.Background())).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("got %v, want [0 1 2] (sorted by the downstream stage)", got)
	}
	waitClosed(t, done, "Take(3) | sort")
}

// unboundedPrefix emits a fixed prefix and then keeps emitting, so a downstream
// Take both sees the prefix in order and provably tears down the unbounded tail.
type unboundedPrefix struct {
	done   chan struct{}
	prefix []int
}

func (c unboundedPrefix) Stream(ctx context.Context) gloo.Stream[int] {
	return gloo.Generate(ctx, func(_ context.Context, send func(int) bool, _ func(error)) {
		defer close(c.done)
		for _, v := range c.prefix {
			if !send(v) {
				return
			}
		}
		for i := 100; ; i++ {
			if !send(i) {
				return
			}
		}
	})
}

// Take(5) | Take(3): chained early-stop stages compose and the upstream exits.
func TestTake_Chained(t *testing.T) {
	done := make(chan struct{})
	src := countUp{done: done}
	pipeline := gloo.Compose[int](patterns.Take[int](5)).To(patterns.Take[int](3))

	got, err := pipeline.Execute(context.Background(), src.Stream(context.Background())).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Errorf("got %v, want [0 1 2]", got)
	}
	waitClosed(t, done, "Take(5) | Take(3)")
}

// ExampleTake builds a shell-`head`-like command that stops its upstream.
func ExampleTake() {
	src := gloo.SliceSource([]int{10, 20, 30, 40, 50})
	got, _ := patterns.Take[int](2).Execute(context.Background(), src.Stream(context.Background())).Collect()
	fmt.Println(got)
	// Output: [10 20]
}

// Take stops at the FIRST upstream error: the error is forwarded, values after
// it are never emitted, and the upstream is stopped — pinning the deliberate
// divergence from the per-item transforms, which continue past error items.
func TestTakeStopsAtFirstError(t *testing.T) {
	wantErr := errors.New("mid-stream failure")
	src := gloo.Generate(context.Background(), func(_ context.Context, send func(string) bool, sendErr func(error)) {
		if !send("before") {
			return
		}
		sendErr(wantErr)
		send("after")
	})
	var values []string
	var errs []error
	out := patterns.Take[string](10).Execute(context.Background(), src)
	for item := range out.Chan() {
		if item.Error != nil {
			errs = append(errs, item.Error)
			continue
		}
		values = append(values, item.Value)
	}
	if len(values) != 1 || values[0] != "before" {
		t.Errorf("values = %v, want only the pre-error item", values)
	}
	if len(errs) != 1 || !errors.Is(errs[0], wantErr) {
		t.Errorf("errors = %v, want exactly the upstream error", errs)
	}
}
