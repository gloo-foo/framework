package gloo

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/destel/rill"
)

func mapCmd[In, Out any](fn func(In) (Out, error)) Command[In, Out] {
	return FuncCommand[In, Out](func(_ context.Context, in Stream[In]) Stream[Out] {
		return WrapFrom(rill.OrderedMap(in.Chan(), 1, fn), in)
	})
}

func collect[T any](t *testing.T, s Stream[T]) []T {
	t.Helper()
	items, err := s.Collect()
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	return items
}

func TestFuncCommandExecute(t *testing.T) {
	upper := mapCmd(func(s string) (string, error) { return strings.ToUpper(s), nil })
	src := SliceSource([]string{"a", "b"})
	got := collect(t, upper.Execute(context.Background(), src.Stream(context.Background())))
	if got[0] != "A" || got[1] != "B" {
		t.Errorf("got %v", got)
	}
}

func TestPipeChangesTypes(t *testing.T) {
	parse := mapCmd(strconv.Atoi)
	double := mapCmd(func(n int) (int, error) { return n * 2, nil })
	format := mapCmd(func(n int) (string, error) { return strconv.Itoa(n), nil })

	// string → int → int → string
	cmd := Pipe(Pipe(parse, double), format)
	src := SliceSource([]string{"1", "21"})
	got := collect(t, cmd.Execute(context.Background(), src.Stream(context.Background())))
	if got[0] != "2" || got[1] != "42" {
		t.Errorf("got %v, want [2 42]", got)
	}
}

func TestPipeIsReusable(t *testing.T) {
	calls := 0
	bump := mapCmd(func(n int) (int, error) { calls++; return n + 1, nil })
	cmd := Pipe(bump, bump)

	for range 3 {
		got := collect(t, cmd.Execute(context.Background(), SliceSource([]int{0}).Stream(context.Background())))
		if got[0] != 2 {
			t.Fatalf("got %v, want [2]", got)
		}
	}
	if calls != 6 {
		t.Errorf("calls = %d, want 6", calls)
	}
}

func TestPipePropagatesError(t *testing.T) {
	wantErr := errors.New("stage failed")
	fail := mapCmd(func(_ string) (string, error) { return "", wantErr })
	pass := mapCmd(func(s string) (string, error) { return s, nil })
	cmd := Pipe(fail, pass)

	_, err := cmd.Execute(context.Background(), SliceSource([]string{"x"}).Stream(context.Background())).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestComposeSingleCommand(t *testing.T) {
	upper := mapCmd(func(s string) (string, error) { return strings.ToUpper(s), nil })
	p := Compose(upper)
	got := collect(t, p.Execute(context.Background(), SliceSource([]string{"hi"}).Stream(context.Background())))
	if got[0] != "HI" {
		t.Errorf("got %v", got)
	}
}

func TestPipelineToDoesNotMutateOriginal(t *testing.T) {
	addA := mapCmd(func(s string) (string, error) { return s + "a", nil })
	addB := mapCmd(func(s string) (string, error) { return s + "b", nil })
	addC := mapCmd(func(s string) (string, error) { return s + "c", nil })

	base := Compose(addA)
	withB := base.To(addB)
	withC := base.To(addC) // must not see addB

	run := func(p Pipeline[string]) string {
		got := collect(t, p.Execute(context.Background(), SliceSource([]string{""}).Stream(context.Background())))
		return got[0]
	}

	if got := run(withB); got != "ab" {
		t.Errorf("withB produced %q, want %q", got, "ab")
	}
	if got := run(withC); got != "ac" {
		t.Errorf("withC produced %q, want %q (base was mutated by a sibling To)", got, "ac")
	}
	if got := run(base); got != "a" {
		t.Errorf("base produced %q, want %q (base was mutated)", got, "a")
	}
}

func TestEmptyPipelinePassesThrough(t *testing.T) {
	var p Pipeline[string]
	got := collect(t, p.Execute(context.Background(), SliceSource([]string{"x", "y"}).Stream(context.Background())))
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Errorf("empty pipeline should pass input through, got %v", got)
	}
}

func TestPipelineUsableAsCommandInPipe(t *testing.T) {
	addBang := mapCmd(func(s string) (string, error) { return s + "!", nil })
	upper := mapCmd(func(s string) (string, error) { return strings.ToUpper(s), nil })

	// A Pipeline is a Command, so it nests inside Pipe.
	cmd := Pipe(Compose(addBang).To(addBang), upper)
	got := collect(t, cmd.Execute(context.Background(), SliceSource([]string{"hi"}).Stream(context.Background())))
	if got[0] != "HI!!" {
		t.Errorf("got %v, want [HI!!]", got)
	}
}
