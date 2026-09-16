package verification

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/destel/rill"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func grepCmd(pattern string) gloo.Command[string, string] {
	return patterns.Filter(func(line string) (bool, error) {
		return strings.Contains(line, pattern), nil
	})
}

func mapCmd(fn func(string) string) gloo.Command[string, string] {
	return patterns.Map(func(line string) (string, error) {
		return fn(line), nil
	})
}

func TestChainTwoStages(t *testing.T) {
	ctx := context.Background()
	input := gloo.StreamOf("apple red", "banana yellow", "cherry red")

	composed := gloo.Compose(grepCmd("red")).To(mapCmd(strings.ToUpper))

	results, err := composed.Execute(ctx, input).Collect()
	if err != nil {
		t.Fatalf("Chain failed: %v", err)
	}

	expected := []string{"APPLE RED", "CHERRY RED"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d", len(expected), len(results))
	}
	for i, res := range results {
		if res != expected[i] {
			t.Errorf("result %d: expected %q, got %q", i, expected[i], res)
		}
	}
}

func TestChainThreeStages(t *testing.T) {
	ctx := context.Background()
	input := gloo.StreamOf("apple red", "banana yellow", "cherry red", "date brown")

	// grep "red" | uppercase | prefix ">> "
	composed := gloo.Compose(grepCmd("red")).
		To(mapCmd(strings.ToUpper)).
		To(mapCmd(func(s string) string { return ">> " + s }))

	results, err := composed.Execute(ctx, input).Collect()
	if err != nil {
		t.Fatalf("Chain failed: %v", err)
	}

	expected := []string{">> APPLE RED", ">> CHERRY RED"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d", len(expected), len(results))
	}
	for i, res := range results {
		if res != expected[i] {
			t.Errorf("result %d: expected %q, got %q", i, expected[i], res)
		}
	}
}

func TestChainFiveStages(t *testing.T) {
	ctx := context.Background()
	input := gloo.StreamOf("hello world", "foo bar", "hello there")

	composed := gloo.Compose(grepCmd("hello")).
		To(mapCmd(strings.ToUpper)).
		To(mapCmd(strings.TrimSpace)).
		To(mapCmd(func(s string) string { return s + "!" })).
		To(mapCmd(func(s string) string { return "[" + s + "]" }))

	results, err := composed.Execute(ctx, input).Collect()
	if err != nil {
		t.Fatalf("Chain failed: %v", err)
	}

	expected := []string{"[HELLO WORLD!]", "[HELLO THERE!]"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d", len(expected), len(results))
	}
	for i, res := range results {
		if res != expected[i] {
			t.Errorf("result %d: expected %q, got %q", i, expected[i], res)
		}
	}
}

func TestChainImplementsCommand(t *testing.T) {
	// Chain should be usable as a Command[T,T] argument to Pipe or another Chain.
	ctx := context.Background()
	input := gloo.StreamOf("abc", "def", "ghi")

	inner := gloo.Compose(mapCmd(strings.ToUpper)).To(mapCmd(func(s string) string { return s + "!" }))
	outer := gloo.Compose(inner).To(mapCmd(func(s string) string { return "[" + s + "]" }))

	results, err := outer.Execute(ctx, input).Collect()
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"[ABC!]", "[DEF!]", "[GHI!]"}
	for i, res := range results {
		if res != expected[i] {
			t.Errorf("result %d: expected %q, got %q", i, expected[i], res)
		}
	}
}

func TestChainBytes(t *testing.T) {
	ctx := context.Background()
	input := gloo.StreamOf([]byte("hello"), []byte("world"))

	upper := gloo.FuncCommand[[]byte, []byte](func(_ context.Context, in gloo.Stream[[]byte]) gloo.Stream[[]byte] {
		return gloo.WrapFrom(rill.OrderedMap(in.Chan(), 1, func(b []byte) ([]byte, error) {
			return []byte(strings.ToUpper(string(b))), nil
		}), in)
	})
	prefix := gloo.FuncCommand[[]byte, []byte](func(_ context.Context, in gloo.Stream[[]byte]) gloo.Stream[[]byte] {
		return gloo.WrapFrom(rill.OrderedMap(in.Chan(), 1, func(b []byte) ([]byte, error) {
			return append([]byte(">> "), b...), nil
		}), in)
	})

	composed := gloo.Compose(upper).To(prefix)
	results, err := composed.Execute(ctx, input).Collect()
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{">> HELLO", ">> WORLD"}
	for i, res := range results {
		if string(res) != expected[i] {
			t.Errorf("result %d: expected %q, got %q", i, expected[i], string(res))
		}
	}
}

func TestChainErrorPropagation(t *testing.T) {
	ctx := context.Background()
	input := gloo.StreamOf("a", "b", "c")

	wantErr := errors.New("fail on b")
	failCmd := gloo.FuncCommand[string, string](func(_ context.Context, in gloo.Stream[string]) gloo.Stream[string] {
		return gloo.WrapFrom(rill.OrderedMap(in.Chan(), 1, func(line string) (string, error) {
			if line == "b" {
				return "", wantErr
			}
			return line, nil
		}), in)
	})

	composed := gloo.Compose(failCmd).To(mapCmd(strings.ToUpper))
	_, err := composed.Execute(ctx, input).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want the stage's own error", err)
	}
}
