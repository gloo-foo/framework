package patterns_test

import (
	"context"
	"errors"
	"os"
	"sort"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func TestAccumulate_FnErrorPropagates(t *testing.T) {
	wantErr := errors.New("sort failed")
	cmd := patterns.Accumulate(func([]string) ([]string, error) {
		return nil, wantErr
	})
	_, err := runErr(t, []string{"a", "b"}, cmd)
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestAccumulate_UpstreamErrorSkipsFn(t *testing.T) {
	wantErr := errors.New("upstream broke")
	called := false
	cmd := patterns.Accumulate(func(items []string) ([]string, error) {
		called = true
		return items, nil
	})
	_, err := cmd.Execute(context.Background(), errStream([]string{"partial"}, wantErr)).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
	if called {
		t.Error("fn must not run on incomplete input")
	}
}

func TestAccumulate_CanGrowAndShrinkOutput(t *testing.T) {
	duplicate := patterns.Accumulate(func(items []string) ([]string, error) {
		return append(items, items...), nil
	})
	got, err := runErr(t, []string{"a"}, duplicate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "a" {
		t.Errorf("grow should duplicate the input, got %v, want [a a]", got)
	}

	dropAll := patterns.Accumulate(func([]string) ([]string, error) { return nil, nil })
	got, err = runErr(t, []string{"a", "b"}, dropAll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nil result should emit nothing, got %v", got)
	}
}

func TestAccumulate_ReusableAcrossPipelines(t *testing.T) {
	sortCmd := patterns.Accumulate(func(lines []string) ([]string, error) {
		sort.Strings(lines)
		return lines, nil
	})
	for range 2 {
		got, err := runErr(t, []string{"c", "a", "b"}, sortCmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0] != "a" || got[1] != "b" || got[2] != "c" {
			t.Errorf("got %v, want [a b c]", got)
		}
	}
}

// ExampleAccumulate builds a shell-`sort`-like command: all input is
// collected, processed as one slice, then re-emitted.
func ExampleAccumulate() {
	sortLines := patterns.Accumulate(func(lines []string) ([]string, error) {
		sort.Strings(lines)
		return lines, nil
	})

	src := gloo.SliceSource([]string{"cherry", "apple", "banana"})
	_, _ = gloo.Run(src, gloo.WriteTo(os.Stdout), sortLines)
	// Output:
	// apple
	// banana
	// cherry
}

// Sort builds a sort command using Accumulate.
func Sort() gloo.Command[string, string] {
	return patterns.Accumulate(func(lines []string) ([]string, error) {
		sort.Strings(lines)
		return lines, nil
	})
}

func TestAccumulate_Sort(t *testing.T) {
	cmd := Sort()
	results := run(t, []string{"banana", "apple", "cherry"}, cmd)
	expectStrings(t, results, "apple", "banana", "cherry")
}

// Tac builds a tac command using Accumulate.
func Tac() gloo.Command[string, string] {
	return patterns.Accumulate(func(lines []string) ([]string, error) {
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
		return lines, nil
	})
}

func TestAccumulate_Tac(t *testing.T) {
	cmd := Tac()
	results := run(t, []string{"first", "second", "third"}, cmd)
	expectStrings(t, results, "third", "second", "first")
}

// Tail builds a tail command using Accumulate.
func Tail(n int) gloo.Command[string, string] {
	return patterns.Accumulate(func(lines []string) ([]string, error) {
		if len(lines) <= n {
			return lines, nil
		}
		return lines[len(lines)-n:], nil
	})
}

func TestAccumulate_Tail(t *testing.T) {
	cmd := Tail(2)
	results := run(t, []string{"a", "b", "c", "d", "e"}, cmd)
	expectStrings(t, results, "d", "e")
}

func TestAccumulate_EmptyInput(t *testing.T) {
	cmd := Sort()
	results := run(t, []string{}, cmd)
	expectStrings(t, results)
}

// TestAccumulateStopsWhenDownstreamStops drives an Accumulate result larger than
// the stream buffer into a Take(1), so Accumulate's emit loop is still sending
// when the downstream stops — exercising the early-return (stopped) branch.
func TestAccumulateStopsWhenDownstreamStops(t *testing.T) {
	big := make([]int, 256)
	for i := range big {
		big[i] = i // distinct values, so the first emitted item is meaningfully 0
	}
	acc := patterns.Accumulate(func(xs []int) ([]int, error) { return xs, nil })
	pipeline := gloo.Compose[int](acc).To(patterns.Take[int](1))
	got, err := pipeline.Execute(context.Background(), gloo.SliceSource(big).Stream(context.Background())).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d items, want 1", len(got))
	}
	if got[0] != 0 {
		t.Errorf("got[0] = %d, want 0 (first accumulated item)", got[0])
	}
}

// An upstream that errors mid-stream and keeps producing must be STOPPED by
// Accumulate's error path (the SIGPIPE analogue), even when the consumer only
// ranges the output to completion and never calls Discard — one of the three
// sanctioned leak-free consumer modes.
func TestAccumulate_ErrorStopsProducingUpstream(t *testing.T) {
	wantErr := errors.New("mid-stream failure")
	done := make(chan struct{})
	src := gloo.Generate(context.Background(), func(_ context.Context, send func(string) bool, sendErr func(error)) {
		defer close(done)
		if !send("a") {
			return
		}
		sendErr(wantErr)
		for send("more") { // keeps producing until told to stop
		}
	})
	out := patterns.Accumulate(func(s []string) ([]string, error) { return s, nil }).Execute(context.Background(), src)
	var firstErr error
	for item := range out.Chan() { // range to completion, no Discard
		if item.Error != nil && firstErr == nil {
			firstErr = item.Error
		}
	}
	if !errors.Is(firstErr, wantErr) {
		t.Fatalf("got %v, want %v", firstErr, wantErr)
	}
	waitClosed(t, done, "accumulate error over still-producing upstream")
}
