package patterns_test

import (
	"context"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func TestHead_TakesPrefix(t *testing.T) {
	got := run(t, []int{1, 2, 3, 4, 5}, patterns.Head[int](2))
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("Head(2) = %v, want [1 2]", got)
	}
}

func TestHead_TerminatesInfiniteSource(t *testing.T) {
	done := make(chan struct{})
	maxSeen := 0
	src := countUp{done: done, maxSeen: &maxSeen}
	got, err := patterns.Head[int](3).Execute(t.Context(), src.Stream(t.Context())).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("got %v, want [0 1 2]", got)
	}
	waitClosed(t, done, "Head(3)")
	// Boundedness: the source produced only a little past what was taken
	// (modulo the stage buffer), nowhere near "infinite".
	if maxSeen > 10_000 {
		t.Errorf("source over-produced: maxSeen=%d", maxSeen)
	}
}

func TestDrop_SkipsPrefix(t *testing.T) {
	got := run(t, []string{"header", "a", "b", "c"}, patterns.Drop[string](1))
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("Drop(1) = %v, want [a b c]", got)
	}
}

func TestDrop_MoreThanAvailable(t *testing.T) {
	got := run(t, []int{1, 2}, patterns.Drop[int](5))
	if len(got) != 0 {
		t.Errorf("Drop(5) over 2 items = %v, want empty", got)
	}
}

// Drop with zero and negative counts must pass everything through — the
// `tail -n +1` identity edge.
func TestDropZeroAndNegativePassEverything(t *testing.T) {
	for _, n := range []patterns.Count{0, -1} {
		got, err := patterns.Drop[string](n).
			Execute(context.Background(), gloo.StreamOf("a", "b")).Collect()
		if err != nil {
			t.Fatalf("Drop(%d): unexpected error: %v", n, err)
		}
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("Drop(%d) = %v, want all items", n, got)
		}
	}
}
