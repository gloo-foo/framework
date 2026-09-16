package patterns_test

import (
	"context"
	"testing"

	gloo "github.com/gloo-foo/framework"
)

// run executes cmd over input built from a slice and returns the collected
// results, failing the test on any pipeline error. Shared by the pattern tests
// in this package.
func run[In, Out any](t *testing.T, input []In, cmd gloo.Command[In, Out]) []Out {
	t.Helper()
	ctx := context.Background()
	stream := cmd.Execute(ctx, gloo.StreamOf(input...))
	results, err := stream.Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return results
}

// expectStrings asserts got equals expected element-wise.
func expectStrings(t *testing.T, got []string, expected ...string) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("expected %d items, got %d: %v", len(expected), len(got), got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Errorf("item %d: expected %q, got %q", i, expected[i], got[i])
		}
	}
}

// Source(...).To(Grep("error")).To(Sort()).To(Head(2)).Collect()
func TestComposition_FluentPipeline(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{
		"info: starting",
		"error: disk full",
		"error: timeout",
		"info: healthy",
		"error: auth failed",
		"error: rate limit",
	})

	result, err := gloo.ChainContext(ctx, src).
		To(Grep("error")).
		To(Sort()).
		To(Head(2)).
		Collect()
	if err != nil {
		t.Fatal(err)
	}

	items := result.([]string)
	expectStrings(t, items, "error: auth failed", "error: disk full")
}

// Compose(cmd).To(cmd).To(cmd) with pattern-built commands.
func TestComposition_Compose(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{
		"hello world",
		"foo bar",
		"hello earth",
	})

	cmd := gloo.Compose(Grep("hello")).
		To(ToUpper()).
		To(Sort())

	stream := gloo.From(ctx, src, cmd)
	results, err := stream.Collect()
	if err != nil {
		t.Fatal(err)
	}
	expectStrings(t, results, "HELLO EARTH", "HELLO WORLD")
}

// Commands are values — assign, pass around, compose.
func TestComposition_CommandsAsValues(t *testing.T) {
	// Build commands as values
	grep := Grep("error")
	upper := ToUpper()
	first3 := Head(3)

	// Compose them
	pipeline := gloo.Compose(grep).To(upper).To(first3)

	// Use the composed pipeline
	results := run(t, []string{
		"error: one",
		"info: two",
		"error: three",
		"error: four",
		"error: five",
	}, pipeline)
	expectStrings(t, results, "ERROR: ONE", "ERROR: THREE", "ERROR: FOUR")
}
