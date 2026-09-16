package patterns_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/destel/rill"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

// runErr executes a command over input and returns the collected results and
// the first stream error. Shared by the per-pattern error-path tests.
func runErr[In, Out any](t *testing.T, input []In, cmd gloo.Command[In, Out]) ([]Out, error) {
	t.Helper()
	ctx := context.Background()
	return cmd.Execute(ctx, gloo.StreamOf(input...)).Collect()
}

// errStream returns a stream that carries the given items followed by an error,
// simulating a failing upstream stage.
func errStream[T any](items []T, err error) gloo.Stream[T] {
	ch := make(chan rill.Try[T], len(items)+1)
	for _, it := range items {
		ch <- rill.Try[T]{Value: it}
	}
	ch <- rill.Try[T]{Error: err}
	close(ch)
	return gloo.Wrap(ch)
}

func TestMap_FnErrorStopsStream(t *testing.T) {
	wantErr := errors.New("bad item")
	cmd := patterns.Map(func(s string) (string, error) {
		if s == "boom" {
			return "", wantErr
		}
		return s, nil
	})
	_, err := runErr(t, []string{"ok", "boom"}, cmd)
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestMap_UpstreamErrorPropagates(t *testing.T) {
	wantErr := errors.New("upstream broke")
	cmd := patterns.Map(func(s string) (string, error) { return s, nil })
	_, err := cmd.Execute(context.Background(), errStream([]string{}, wantErr)).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestMap_EmptyInput(t *testing.T) {
	cmd := patterns.Map(func(s string) (string, error) { return s, nil })
	got, err := runErr(t, []string{}, cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestStatefulMap_FactoryErrorPropagates(t *testing.T) {
	wantErr := errors.New("counter overflow")
	cmd := patterns.StatefulMap(func() func(string) (string, error) {
		n := 0
		return func(s string) (string, error) {
			n++
			if n > 1 {
				return "", wantErr
			}
			return s, nil
		}
	})
	// Collect item-by-item so we can prove the pre-error output ("a") is
	// delivered BEFORE the factory's per-item error surfaces.
	ctx := context.Background()
	src := gloo.SliceSource([]string{"a", "b"})
	var got []string
	var gotErr error
	for item := range cmd.Execute(ctx, src.Stream(ctx)).Chan() {
		if item.Error != nil {
			gotErr = item.Error
			break
		}
		got = append(got, item.Value)
	}
	if !errors.Is(gotErr, wantErr) {
		t.Errorf("got %v, want %v", gotErr, wantErr)
	}
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("pre-error output = %v, want [a] emitted before the error", got)
	}
}

// ExampleMap builds a shell-`tr`-like command: each input line is
// transformed independently, one line in, one line out.
func ExampleMap() {
	upper := patterns.Map(func(line string) (string, error) {
		return strings.ToUpper(line), nil
	})

	src := gloo.SliceSource([]string{"hello", "world"})
	_, _ = gloo.Run(src, gloo.WriteTo(os.Stdout), upper)
	// Output:
	// HELLO
	// WORLD
}

// ExampleStatefulMap builds a shell-`nl`-like command: the factory runs once
// per Execute, giving each pipeline its own fresh counter.
func ExampleStatefulMap() {
	numbered := patterns.StatefulMap(func() func(string) (string, error) {
		n := 0
		return func(line string) (string, error) {
			n++
			return fmt.Sprintf("%d %s", n, line), nil
		}
	})

	src := gloo.SliceSource([]string{"alpha", "beta"})
	_, _ = gloo.Run(src, gloo.WriteTo(os.Stdout), numbered)
	// Output:
	// 1 alpha
	// 2 beta
}

// Tr builds a tr-like command using Map — the command author writes ONLY the
// algorithm.
func Tr(from, to string) gloo.Command[string, string] {
	replacer := strings.NewReplacer(from, to)
	return patterns.Map(func(line string) (string, error) {
		return replacer.Replace(line), nil
	})
}

func TestMap_Tr(t *testing.T) {
	cmd := Tr("world", "earth")
	results := run(t, []string{"hello world", "world peace"}, cmd)
	expectStrings(t, results, "hello earth", "earth peace")
}

// ToUpper builds an upper-casing command using Map.
func ToUpper() gloo.Command[string, string] {
	return patterns.Map(func(line string) (string, error) {
		return strings.ToUpper(line), nil
	})
}

func TestMap_ToUpper(t *testing.T) {
	cmd := ToUpper()
	results := run(t, []string{"hello", "world"}, cmd)
	expectStrings(t, results, "HELLO", "WORLD")
}

// Strlen maps with a type change: string → int (line length).
func Strlen() gloo.Command[string, int] {
	return patterns.Map(func(line string) (int, error) {
		return len(line), nil
	})
}

func TestMap_TypeChange(t *testing.T) {
	cmd := Strlen()
	results := run(t, []string{"hi", "hello"}, cmd)
	if len(results) != 2 || results[0] != 2 || results[1] != 5 {
		t.Errorf("expected [2, 5], got %v", results)
	}
}

// Nl builds an nl command using StatefulMap.
func Nl() gloo.Command[string, string] {
	return patterns.StatefulMap(func() func(string) (string, error) {
		n := 0
		return func(line string) (string, error) {
			n++
			return fmt.Sprintf("%d\t%s", n, line), nil
		}
	})
}

func TestStatefulMap_Nl(t *testing.T) {
	cmd := Nl()
	results := run(t, []string{"alpha", "beta", "gamma"}, cmd)
	expectStrings(t, results, "1\talpha", "2\tbeta", "3\tgamma")
}

// Verify StatefulMap resets state on reuse — same command value, two pipelines.
func TestStatefulMap_Reuse(t *testing.T) {
	cmd := Nl()

	// First use
	r1 := run(t, []string{"a", "b"}, cmd)
	expectStrings(t, r1, "1\ta", "2\tb")

	// Second use — counter must restart at 1, not continue from 3
	r2 := run(t, []string{"x", "y", "z"}, cmd)
	expectStrings(t, r2, "1\tx", "2\ty", "3\tz")
}
