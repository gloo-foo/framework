package verification

import (
	"context"
	"testing"

	gloo "github.com/gloo-foo/framework"
)

// T016: Sink interface contract — trivial collect-to-slice sink.

// collectSink collects all stream items into a slice.
type collectSink[T any] struct{}

func (s collectSink[T]) Consume(_ context.Context, input gloo.Stream[T]) ([]T, error) {
	return input.Collect()
}

// Compile-time check that collectSink satisfies Sink interface.
var _ gloo.Sink[string, []string] = collectSink[string]{}

func TestSinkCollect(t *testing.T) {
	ctx := context.Background()
	input := gloo.StreamOf("a", "b", "c")

	sink := collectSink[string]{}
	result, err := sink.Consume(ctx, input)
	if err != nil {
		t.Fatalf("Sink.Consume failed: %v", err)
	}

	expected := []string{"a", "b", "c"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d items, got %d", len(expected), len(result))
	}
	for i, v := range result {
		if v != expected[i] {
			t.Errorf("item %d: expected %q, got %q", i, expected[i], v)
		}
	}
}
