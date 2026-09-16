package verification

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/destel/rill"

	gloo "github.com/gloo-foo/framework"
)

// sliceSource is a test helper that creates a Source from a slice.
type sliceSource[T any] struct {
	items []T
}

func (s *sliceSource[T]) Stream(_ context.Context) gloo.Stream[T] {
	return gloo.StreamOf(s.items...)
}

func newSliceSource[T any](items []T) gloo.Source[T] {
	return &sliceSource[T]{items: items}
}

// TestFluentPipelineTypeChanging verifies a 3+ stage type-changing
// pipeline can be expressed in a single fluent chain and produces correct results.
func TestFluentPipelineTypeChanging(t *testing.T) {
	ctx := context.Background()
	src := newSliceSource([]string{"apple red", "banana yellow", "cherry red"})

	// Stage 1: filter strings containing "red"
	filterCmd := gloo.FuncCommand[string, string](
		func(_ context.Context, in gloo.Stream[string]) gloo.Stream[string] {
			return gloo.WrapFrom(rill.OrderedFilter(in.Chan(), 1, func(line string) (bool, error) {
				return strings.Contains(line, "red"), nil
			}), in)
		},
	)

	// Stage 2: convert string → []byte
	toBytesCmd := gloo.FuncCommand[string, []byte](
		func(_ context.Context, in gloo.Stream[string]) gloo.Stream[[]byte] {
			return gloo.WrapFrom(rill.OrderedMap(in.Chan(), 1, func(line string) ([]byte, error) {
				return []byte(line), nil
			}), in)
		},
	)

	// Stage 3: convert []byte → string (uppercase)
	toStringCmd := gloo.FuncCommand[[]byte, string](
		func(_ context.Context, in gloo.Stream[[]byte]) gloo.Stream[string] {
			return gloo.WrapFrom(rill.OrderedMap(in.Chan(), 1, func(line []byte) (string, error) {
				return strings.ToUpper(string(line)), nil
			}), in)
		},
	)

	result, err := gloo.ChainContext(ctx, src).
		To(filterCmd).
		To(toBytesCmd).
		To(toStringCmd).
		Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, ok := result.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", result)
	}

	expected := []string{"APPLE RED", "CHERRY RED"}
	if len(items) != len(expected) {
		t.Fatalf("expected %d results, got %d: %v", len(expected), len(items), items)
	}
	for i, item := range items {
		if item != expected[i] {
			t.Errorf("result[%d]: expected %q, got %q", i, expected[i], item)
		}
	}
}

// TestFluentPipelineTypeMismatch verifies type mismatches between
// pipeline stages produce a clear error identifying expected and actual types.
func TestFluentPipelineTypeMismatch(t *testing.T) {
	ctx := context.Background()
	src := newSliceSource([]string{"hello"})

	// This command expects []byte input, but the source produces strings.
	bytesCmd := gloo.FuncCommand[[]byte, []byte](func(_ context.Context, in gloo.Stream[[]byte]) gloo.Stream[[]byte] {
		return in
	})

	_, err := gloo.ChainContext(ctx, src).To(bytesCmd).Collect()
	if !errors.Is(err, gloo.ErrStageTypeMismatch) {
		t.Fatalf("got %v, want ErrStageTypeMismatch", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "string") || !strings.Contains(msg, "uint8") {
		t.Errorf("error should identify expected ([]byte/uint8) and actual (string) types, got: %s", msg)
	}
}

// TestFluentPipelineSink verifies .Sink() calls the sink's Consume method.
func TestFluentPipelineSink(t *testing.T) {
	ctx := context.Background()
	src := newSliceSource([]string{"line1", "line2", "line3"})

	// Use a counting sink.
	var collected []string
	countSink := &testSink{collected: &collected}

	result, err := gloo.ChainContext(ctx, src).Sink(countSink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	count, ok := result.(int)
	if !ok {
		t.Fatalf("expected int result, got %T", result)
	}
	if count != 3 {
		t.Errorf("expected count 3, got %d", count)
	}
	want := []string{"line1", "line2", "line3"}
	if len(collected) != len(want) {
		t.Fatalf("collected %v, want %v", collected, want)
	}
	for i, w := range want {
		if collected[i] != w {
			t.Errorf("collected[%d] = %q, want %q", i, collected[i], w)
		}
	}
}

type testSink struct {
	collected *[]string
}

func (s *testSink) Consume(_ context.Context, input gloo.Stream[string]) (int, error) {
	count := 0
	err := rill.ForEach(input.Chan(), 1, func(line string) error {
		*s.collected = append(*s.collected, line)
		count++
		return nil
	})
	return count, err
}

// TestFluentPipelineForEach verifies .ForEach() calls the function for each item.
func TestFluentPipelineForEach(t *testing.T) {
	ctx := context.Background()
	src := newSliceSource([]string{"a", "b", "c"})

	var collected []string
	err := gloo.ChainContext(ctx, src).ForEach(func(line string) error {
		collected = append(collected, line)
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(collected) != 3 {
		t.Fatalf("expected 3 items, got %d", len(collected))
	}
	expected := []string{"a", "b", "c"}
	for i, item := range collected {
		if item != expected[i] {
			t.Errorf("item[%d]: expected %q, got %q", i, expected[i], item)
		}
	}
}

// TestFluentPipelineErrorPropagation verifies errors propagate through
// the pipeline and surface at the terminal operation.
func TestFluentPipelineErrorPropagation(t *testing.T) {
	ctx := context.Background()
	src := newSliceSource([]string{"good", "bad", "good2"})

	// Command that errors on "bad"
	wantErr := errors.New("encountered bad line")
	errorCmd := gloo.FuncCommand[string, string](func(_ context.Context, in gloo.Stream[string]) gloo.Stream[string] {
		return gloo.WrapFrom(rill.OrderedMap(in.Chan(), 1, func(line string) (string, error) {
			if line == "bad" {
				return "", wantErr
			}
			return line, nil
		}), in)
	})

	_, err := gloo.ChainContext(ctx, src).To(errorCmd).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want the stage's own error", err)
	}
}

// TestFluentPipelineZeroStages verifies that Source(...).Collect() works
// with zero .To() stages — the source flows directly to the terminal.
func TestFluentPipelineZeroStages(t *testing.T) {
	ctx := context.Background()
	src := newSliceSource([]string{"hello", "world"})

	result, err := gloo.ChainContext(ctx, src).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, ok := result.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", result)
	}
	want := []string{"hello", "world"}
	if len(items) != len(want) {
		t.Fatalf("got %v, want %v", items, want)
	}
	for i, w := range want {
		if items[i] != w {
			t.Errorf("items[%d] = %q, want %q (source flows through unchanged, in order)", i, items[i], w)
		}
	}
}

// TestFluentPipelinePostTerminalErrors verifies that recording a stage after a
// terminal operation surfaces ErrPipelineConsumed from the next terminal.
func TestFluentPipelinePostTerminalErrors(t *testing.T) {
	ctx := context.Background()
	src := newSliceSource([]string{"hello"})

	pipeline := gloo.ChainContext(ctx, src)
	_, _ = pipeline.Collect() // consume the pipeline

	identity := gloo.FuncCommand[string, string](func(_ context.Context, in gloo.Stream[string]) gloo.Stream[string] {
		return in
	})
	_, err := pipeline.To(identity).Collect()
	if !errors.Is(err, gloo.ErrPipelineConsumed) {
		t.Errorf("got %v, want ErrPipelineConsumed", err)
	}
}
