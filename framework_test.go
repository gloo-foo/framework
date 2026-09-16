package gloo

import (
	"context"
	"strings"
	"testing"

	"github.com/destel/rill"
)

// The core types are pure interfaces; verify that minimal hand-rolled
// implementations (no patterns, no helpers) satisfy them and compose.

type countingSource struct{ n int }

func (s *countingSource) Stream(ctx context.Context) Stream[int] {
	return Generate(ctx, func(_ context.Context, send func(int) bool, _ func(error)) {
		for i := 1; i <= s.n; i++ {
			if !send(i) {
				return
			}
		}
	})
}

type doubler struct{}

func (doubler) Execute(_ context.Context, in Stream[int]) Stream[int] {
	return WrapFrom(rill.OrderedMap(in.Chan(), 1, func(v int) (int, error) { return v * 2, nil }), in)
}

type summer struct{}

func (summer) Consume(_ context.Context, in Stream[int]) (int, error) {
	total := 0
	err := rill.ForEach(in.Chan(), 1, func(v int) error {
		total += v
		return nil
	})
	return total, err
}

func TestCustomSourceCommandSinkRoundtrip(t *testing.T) {
	ctx := context.Background()
	var src Source[int] = &countingSource{n: 4}
	var cmd Command[int, int] = doubler{}
	var sink Sink[int, int] = summer{}

	got, err := sink.Consume(ctx, cmd.Execute(ctx, src.Stream(ctx)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := 2 + 4 + 6 + 8; got != want {
		t.Errorf("sum = %d, want %d", got, want)
	}
}

func TestStreamWrapsChannelOfTry(t *testing.T) {
	ch := make(chan rill.Try[string], 2)
	ch <- rill.Try[string]{Value: "hello"}
	ch <- rill.Try[string]{Value: "world"}
	close(ch)

	s := Wrap(ch)
	var got []string
	for item := range s.Chan() {
		if item.Error != nil {
			t.Fatalf("unexpected error item: %v", item.Error)
		}
		got = append(got, item.Value)
	}
	if strings.Join(got, " ") != "hello world" {
		t.Errorf("got %v", got)
	}
	// Discard must be safe (a no-op) even after the stream is fully drained.
	s.Discard()
}
