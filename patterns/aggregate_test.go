package patterns_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func TestAggregate_FnErrorPropagates(t *testing.T) {
	wantErr := errors.New("hash failed")
	cmd := patterns.Aggregate(func([]string) (string, error) {
		return "", wantErr
	})
	_, err := runErr(t, []string{"a"}, cmd)
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestAggregate_UpstreamErrorSkipsFn(t *testing.T) {
	wantErr := errors.New("upstream broke")
	called := false
	cmd := patterns.Aggregate(func(items []string) (int, error) {
		called = true
		return len(items), nil
	})
	_, err := cmd.Execute(context.Background(), errStream([]string{"partial"}, wantErr)).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
	if called {
		t.Error("fn must not run on incomplete input")
	}
}

func TestAggregate_EmitsExactlyOneItem(t *testing.T) {
	count := patterns.Aggregate(func(lines []string) (int, error) {
		return len(lines), nil
	})
	got, err := runErr(t, []string{"a", "b", "c"}, count)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != 3 {
		t.Errorf("got %v, want exactly [3]", got)
	}
}

func TestAggregate_EmptyInputStillEmits(t *testing.T) {
	// wc -l on empty input prints 0 — an Aggregate always produces a result.
	count := patterns.Aggregate(func(lines []string) (int, error) {
		return len(lines), nil
	})
	got, err := runErr(t, []string{}, count)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("got %v, want [0]", got)
	}
}

// ExampleAggregate builds a shell-`wc -l`-like command: all input reduces to
// a single value, and the output type can differ from the input type.
func ExampleAggregate() {
	countLines := patterns.Aggregate(func(lines []string) (int, error) {
		return len(lines), nil
	})

	src := gloo.SliceSource([]string{"one", "two", "three"})
	result, _ := gloo.Chain(src).To(countLines).Collect()
	fmt.Println(result.([]int)[0])
	// Output:
	// 3
}

// Wc builds a wc -l command using Aggregate.
func Wc() gloo.Command[string, int] {
	return patterns.Aggregate(func(lines []string) (int, error) {
		return len(lines), nil
	})
}

func TestAggregate_Wc(t *testing.T) {
	cmd := Wc()
	results := run(t, []string{"a", "b", "c"}, cmd)
	if len(results) != 1 || results[0] != 3 {
		t.Errorf("expected [3], got %v", results)
	}
}

// Join builds a join command using Aggregate (like paste -s -d,).
func Join(sep string) gloo.Command[string, string] {
	return patterns.Aggregate(func(lines []string) (string, error) {
		return strings.Join(lines, sep), nil
	})
}

func TestAggregate_Join(t *testing.T) {
	cmd := Join(",")
	results := run(t, []string{"a", "b", "c"}, cmd)
	expectStrings(t, results, "a,b,c")
}

func TestAggregate_EmptyInput(t *testing.T) {
	cmd := Wc()
	results := run(t, []string{}, cmd)
	if len(results) != 1 || results[0] != 0 {
		t.Errorf("expected [0], got %v", results)
	}
}
