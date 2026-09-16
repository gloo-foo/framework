package verification

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/destel/rill"

	gloo "github.com/gloo-foo/framework"
)

func TestPipeline(t *testing.T) {
	inputData := []string{
		"apple red",
		"banana yellow",
		"cherry red",
		"date brown",
	}

	ctx := context.Background()
	input := gloo.StreamOf(inputData...)

	// Grep: filter lines containing "red"
	grepCmd := gloo.FuncCommand[string, string](func(_ context.Context, in gloo.Stream[string]) gloo.Stream[string] {
		return gloo.WrapFrom(rill.OrderedFilter(in.Chan(), 1, func(line string) (bool, error) {
			return strings.Contains(line, "red"), nil
		}), in)
	})

	grepOut := grepCmd.Execute(ctx, input)

	// Awk: print first field
	awkCmd := gloo.FuncCommand[string, string](func(_ context.Context, in gloo.Stream[string]) gloo.Stream[string] {
		return gloo.WrapFrom(rill.OrderedMap(in.Chan(), 1, func(line string) (string, error) {
			var f1 string
			// Best-effort first-field extraction (awk '{print $1}'): a blank line
			// yields an empty field, which is the intended result, not an error.
			_, _ = fmt.Sscanf(line, "%s", &f1)
			return f1, nil
		}), in)
	})

	awkOut := awkCmd.Execute(ctx, grepOut)

	// Collect results
	results, err := awkOut.Collect()
	if err != nil {
		t.Fatalf("Pipeline failed: %v", err)
	}

	expected := []string{"apple", "cherry"}
	if len(results) != len(expected) {
		t.Errorf("Expected %d results, got %d", len(expected), len(results))
	}

	for i, res := range results {
		if res != expected[i] {
			t.Errorf("Result %d: expected %s, got %s", i, expected[i], res)
		}
	}
}
