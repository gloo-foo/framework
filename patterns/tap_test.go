package patterns_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func TestTap_ObservesEveryItemInOrder(t *testing.T) {
	var seen []string
	cmd := patterns.Tap(func(s string) error {
		seen = append(seen, s)
		return nil
	})
	got, err := runErr(t, []string{"a", "b", "c"}, cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("passthrough lost items: %v", got)
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i] != want {
			t.Errorf("got[%d] = %q, want %q (Tap must pass items unchanged)", i, got[i], want)
		}
		if seen[i] != want {
			t.Errorf("seen[%d] = %q, want %q", i, seen[i], want)
		}
	}
}

func TestTap_UpstreamErrorPropagates(t *testing.T) {
	wantErr := errors.New("upstream broke")
	cmd := patterns.Tap(func(string) error { return nil })
	_, err := cmd.Execute(context.Background(), errStream([]string{}, wantErr)).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

// ExampleTap builds a shell-`tee`-like observer: items pass through unchanged
// while a side effect sees each one.
func ExampleTap() {
	logged := patterns.Tap(func(line string) error {
		fmt.Println("seen:", line)
		return nil
	})

	// The sink buffers its writes, so the tap's log lines appear first.
	src := gloo.SliceSource([]string{"alpha", "beta"})
	_, _ = gloo.Run(src, gloo.WriteTo(os.Stdout), logged)
	// Output:
	// seen: alpha
	// seen: beta
	// alpha
	// beta
}

func TestTap_PassthroughUnchanged(t *testing.T) {
	var seen []string
	cmd := patterns.Tap(func(s string) error {
		seen = append(seen, s)
		return nil
	})

	results := run(t, []string{"alpha", "bravo", "charlie"}, cmd)

	// Items pass through unchanged.
	expectStrings(t, results, "alpha", "bravo", "charlie")

	// Side effect was called for each item.
	expectStrings(t, seen, "alpha", "bravo", "charlie")
}

func TestTap_SideEffectError(t *testing.T) {
	sentinel := errors.New("side effect error")
	cmd := patterns.Tap(func(s string) error {
		if s == "bravo" {
			return sentinel
		}
		return nil
	})

	ctx := context.Background()
	src := gloo.SliceSource([]string{"alpha", "bravo", "charlie"})
	stream := cmd.Execute(ctx, src.Stream(ctx))
	_, err := stream.Collect()

	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the side-effect sentinel, got %v", err)
	}
}

func TestTap_EmptyInput(t *testing.T) {
	called := false
	cmd := patterns.Tap(func(_ string) error {
		called = true
		return nil
	})

	results := run(t, []string{}, cmd)
	if len(results) != 0 {
		t.Errorf("got %d items, want 0", len(results))
	}
	if called {
		t.Error("side effect called on empty input")
	}
}
