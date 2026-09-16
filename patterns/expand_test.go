package patterns_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func TestExpand_FnErrorStopsStream(t *testing.T) {
	wantErr := errors.New("split failed")
	cmd := patterns.Expand(func(s string) ([]string, error) {
		if s == "boom" {
			return nil, wantErr
		}
		return []string{s}, nil
	})

	ctx := context.Background()
	src := gloo.SliceSource([]string{"ok", "boom", "after"})
	var got []string
	var streamErr error
	for item := range cmd.Execute(ctx, src.Stream(ctx)).Chan() {
		if item.Error != nil {
			streamErr = item.Error
			continue
		}
		got = append(got, item.Value)
	}
	if !errors.Is(streamErr, wantErr) {
		t.Errorf("got %v, want %v", streamErr, wantErr)
	}
	if len(got) == 0 || got[0] != "ok" {
		t.Errorf("outputs before the failure should be delivered, got %v", got)
	}
}

func TestExpand_UpstreamErrorPropagates(t *testing.T) {
	wantErr := errors.New("upstream broke")
	cmd := patterns.Expand(func(s string) ([]string, error) { return []string{s}, nil })
	_, err := cmd.Execute(context.Background(), errStream([]string{}, wantErr)).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestExpand_PreservesInputOrder(t *testing.T) {
	cmd := patterns.Expand(func(s string) ([]string, error) {
		return strings.Split(s, ","), nil
	})
	got, err := runErr(t, []string{"a,b", "c,d"}, cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v (no extra outputs)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (all outputs of item 1 before item 2)", got, want)
		}
	}
}

func TestExpand_EmptyResultDropsItem(t *testing.T) {
	cmd := patterns.Expand(func(s string) ([]string, error) {
		if s == "drop" {
			return nil, nil
		}
		return []string{s}, nil
	})
	got, err := runErr(t, []string{"keep", "drop", "keep2"}, cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "keep" || got[1] != "keep2" {
		t.Errorf("got %v, want [keep keep2]", got)
	}
}

// ExampleExpand builds a word-splitting command (like `xargs -n1`): one input
// line fans out to zero or more output lines.
func ExampleExpand() {
	words := patterns.Expand(func(line string) ([]string, error) {
		return strings.Fields(line), nil
	})

	src := gloo.SliceSource([]string{"hello world", "foo bar baz"})
	_, _ = gloo.Run(src, gloo.WriteTo(os.Stdout), words)
	// Output:
	// hello
	// world
	// foo
	// bar
	// baz
}

// Words builds a words command using Expand (split each line into words).
func Words() gloo.Command[string, string] {
	return patterns.Expand(func(line string) ([]string, error) {
		return strings.Fields(line), nil
	})
}

func TestExpand_Words(t *testing.T) {
	cmd := Words()
	results := run(t, []string{"hello world", "foo bar baz"}, cmd)
	expectStrings(t, results, "hello", "world", "foo", "bar", "baz")
}

// Split builds a split command using Expand.
func Split(delim string) gloo.Command[string, string] {
	return patterns.Expand(func(line string) ([]string, error) {
		return strings.Split(line, delim), nil
	})
}

func TestExpand_Split(t *testing.T) {
	cmd := Split(",")
	results := run(t, []string{"a,b,c", "d,e"}, cmd)
	expectStrings(t, results, "a", "b", "c", "d", "e")
}

func TestExpand_EmptyExpansion(t *testing.T) {
	// An item that expands to nothing is simply dropped
	cmd := patterns.Expand(func(line string) ([]string, error) {
		if line == "skip" {
			return nil, nil
		}
		return []string{line}, nil
	})
	results := run(t, []string{"keep", "skip", "also keep"}, cmd)
	expectStrings(t, results, "keep", "also keep")
}

// A downstream stop mid-expansion must end the emit loop early: Expand of one
// item into many more than the stream buffer holds, capped by Take(1), must
// stop INSIDE the expansion instead of emitting the whole slice.
func TestExpandStopsMidExpansionOnDownstreamStop(t *testing.T) {
	// One input item expanding to far more outputs than the stream buffer, so
	// the producer observes the stop mid-expansion rather than finishing.
	wide := make([]string, 500)
	for i := range wide {
		wide[i] = strconv.Itoa(i)
	}
	expand := patterns.Expand(func(string) ([]string, error) { return wide, nil })
	pipe := gloo.Pipe(expand, patterns.Take[string](1))
	got, err := pipe.Execute(context.Background(), gloo.StreamOf("seed")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "0" {
		t.Errorf("got %v, want just the first expansion", got)
	}
}
