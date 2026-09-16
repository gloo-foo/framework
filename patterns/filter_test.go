package patterns_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func TestFilter_PredicateErrorStopsStream(t *testing.T) {
	wantErr := errors.New("bad pattern")
	cmd := patterns.Filter(func(s string) (bool, error) {
		if s == "boom" {
			return false, wantErr
		}
		return true, nil
	})
	_, err := runErr(t, []string{"ok", "boom"}, cmd)
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestFilter_UpstreamErrorPropagates(t *testing.T) {
	wantErr := errors.New("upstream broke")
	cmd := patterns.Filter(func(_ string) (bool, error) { return true, nil })
	_, err := cmd.Execute(context.Background(), errStream([]string{}, wantErr)).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestFilter_EmptyInput(t *testing.T) {
	cmd := patterns.Filter(func(_ string) (bool, error) { return true, nil })
	got, err := runErr(t, []string{}, cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestFilter_DropsEverything(t *testing.T) {
	cmd := patterns.Filter(func(_ string) (bool, error) { return false, nil })
	got, err := runErr(t, []string{"a", "b", "c"}, cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestStatefulFilter_PredicateErrorStopsStream(t *testing.T) {
	wantErr := errors.New("state corrupted")
	cmd := patterns.StatefulFilter(func() func(string) (bool, error) {
		return func(string) (bool, error) { return false, wantErr }
	})
	_, err := runErr(t, []string{"a"}, cmd)
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

// ExampleFilter builds a shell-`grep`-like command: each line either passes
// through unchanged or is dropped.
func ExampleFilter() {
	grep := patterns.Filter(func(line string) (bool, error) {
		return strings.Contains(line, "red"), nil
	})

	src := gloo.SliceSource([]string{"apple red", "banana yellow", "cherry red"})
	_, _ = gloo.Run(src, gloo.WriteTo(os.Stdout), grep)
	// Output:
	// apple red
	// cherry red
}

// ExampleStatefulFilter builds a shell-`head -n 2`-like command: the factory
// runs once per Execute, so the line counter starts fresh for each pipeline.
func ExampleStatefulFilter() {
	head := patterns.StatefulFilter(func() func(string) (bool, error) {
		count := 0
		return func(string) (bool, error) {
			count++
			return count <= 2, nil
		}
	})

	src := gloo.SliceSource([]string{"one", "two", "three", "four"})
	_, _ = gloo.Run(src, gloo.WriteTo(os.Stdout), head)
	// Output:
	// one
	// two
}

// Grep builds a grep command using Filter.
func Grep(pattern string) gloo.Command[string, string] {
	return patterns.Filter(func(line string) (bool, error) {
		return strings.Contains(line, pattern), nil
	})
}

func TestFilter_Grep(t *testing.T) {
	cmd := Grep("error")
	results := run(t, []string{"error: bad", "info: ok", "error: worse"}, cmd)
	expectStrings(t, results, "error: bad", "error: worse")
}

func TestFilter_NoMatches(t *testing.T) {
	cmd := Grep("missing")
	results := run(t, []string{"foo", "bar", "baz"}, cmd)
	expectStrings(t, results)
}

// Head builds a head command using StatefulFilter.
func Head(n int) gloo.Command[string, string] {
	return patterns.StatefulFilter(func() func(string) (bool, error) {
		count := 0
		return func(_ string) (bool, error) {
			count++
			return count <= n, nil
		}
	})
}

func TestStatefulFilter_Head(t *testing.T) {
	cmd := Head(2)
	results := run(t, []string{"a", "b", "c", "d", "e"}, cmd)
	expectStrings(t, results, "a", "b")
}

// Uniq builds a uniq command using StatefulFilter.
func Uniq() gloo.Command[string, string] {
	return patterns.StatefulFilter(func() func(string) (bool, error) {
		var prev string
		first := true
		return func(line string) (bool, error) {
			if first || line != prev {
				prev = line
				first = false
				return true, nil
			}
			return false, nil
		}
	})
}

func TestStatefulFilter_Uniq(t *testing.T) {
	cmd := Uniq()
	results := run(t, []string{"a", "a", "b", "b", "b", "c"}, cmd)
	expectStrings(t, results, "a", "b", "c")
}

// Verify StatefulFilter resets state on reuse.
func TestStatefulFilter_Reuse(t *testing.T) {
	cmd := Head(2)

	r1 := run(t, []string{"a", "b", "c"}, cmd)
	expectStrings(t, r1, "a", "b")

	// Second use — must get first 2 again, not 0 (exhausted counter)
	r2 := run(t, []string{"x", "y", "z"}, cmd)
	expectStrings(t, r2, "x", "y")
}
