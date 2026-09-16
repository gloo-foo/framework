package verification

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/destel/rill"

	gloo "github.com/gloo-foo/framework"
)

// Compile-time check that FuncCommand satisfies Command interface.
var _ gloo.Command[string, string] = gloo.FuncCommand[string, string](nil)

// Single FuncCommand execution — filter lines containing "red".
func TestFuncCommandFilter(t *testing.T) {
	ctx := context.Background()
	input := gloo.StreamOf(
		"apple red",
		"banana yellow",
		"cherry red",
		"date brown",
	)

	grepCmd := gloo.FuncCommand[string, string](func(_ context.Context, in gloo.Stream[string]) gloo.Stream[string] {
		return gloo.WrapFrom(rill.OrderedFilter(in.Chan(), 1, func(line string) (bool, error) {
			return strings.Contains(line, "red"), nil
		}), in)
	})

	results, err := grepCmd.Execute(ctx, input).Collect()
	if err != nil {
		t.Fatalf("FuncCommand filter failed: %v", err)
	}

	expected := []string{"apple red", "cherry red"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d", len(expected), len(results))
	}
	for i, res := range results {
		if res != expected[i] {
			t.Errorf("result %d: expected %q, got %q", i, expected[i], res)
		}
	}
}

// Pipe composition — Command[string, string] + Command[string, string] → Command[string, string].
func TestPipeComposition(t *testing.T) {
	ctx := context.Background()
	input := gloo.StreamOf(
		"apple red",
		"banana yellow",
		"cherry red",
		"date brown",
	)

	// First command: filter lines containing "red"
	grepCmd := gloo.FuncCommand[string, string](func(_ context.Context, in gloo.Stream[string]) gloo.Stream[string] {
		return gloo.WrapFrom(rill.OrderedFilter(in.Chan(), 1, func(line string) (bool, error) {
			return strings.Contains(line, "red"), nil
		}), in)
	})

	// Second command: extract first field
	awkCmd := gloo.FuncCommand[string, string](func(_ context.Context, in gloo.Stream[string]) gloo.Stream[string] {
		return gloo.WrapFrom(rill.OrderedMap(in.Chan(), 1, func(line string) (string, error) {
			var f1 string
			// Best-effort first-field extraction (awk '{print $1}'): a blank line
			// yields an empty field, which is the intended result, not an error.
			_, _ = fmt.Sscanf(line, "%s", &f1)
			return f1, nil
		}), in)
	})

	// Compose via Pipe
	composed := gloo.Pipe(grepCmd, awkCmd)

	results, err := composed.Execute(ctx, input).Collect()
	if err != nil {
		t.Fatalf("Pipe composition failed: %v", err)
	}

	expected := []string{"apple", "cherry"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d", len(expected), len(results))
	}
	for i, res := range results {
		if res != expected[i] {
			t.Errorf("result %d: expected %q, got %q", i, expected[i], res)
		}
	}
}
