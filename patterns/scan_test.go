package patterns_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

// uniqC builds the `uniq -c` scan: counted groups, flushed at boundaries and
// at end of stream.
func uniqC() gloo.Command[string, string] {
	return patterns.Scan(func() (func(string) ([]string, error), func() ([]string, error)) {
		var cur string
		n := 0
		group := func() []string {
			if n == 0 {
				return nil
			}
			return []string{strconv.Itoa(n) + " " + cur}
		}
		step := func(line string) ([]string, error) {
			if n > 0 && line == cur {
				n++
				return nil, nil
			}
			out := group()
			cur, n = line, 1
			return out, nil
		}
		end := func() ([]string, error) { return group(), nil }
		return step, end
	})
}

func TestScan_FlushesFinalGroupAtEndOfStream(t *testing.T) {
	got, err := uniqC().Execute(context.Background(), gloo.StreamOf("a", "a", "b", "c", "c")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"2 a", "1 b", "2 c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestScan_EmptyInputFlushesNothing(t *testing.T) {
	got, err := uniqC().Execute(context.Background(), gloo.StreamOf[string]()).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestScan_ReusableWithFreshState(t *testing.T) {
	cmd := uniqC()
	for run := 0; run < 2; run++ {
		got, err := cmd.Execute(context.Background(), gloo.StreamOf("x", "x")).Collect()
		if err != nil || len(got) != 1 || got[0] != "2 x" {
			t.Fatalf("run %d: got %v err=%v, want fresh state per Execute", run, got, err)
		}
	}
}

func TestScan_StepErrorEmittedAndStreamContinues(t *testing.T) {
	wantErr := errors.New("bad line")
	cmd := patterns.Scan(func() (func(string) ([]string, error), func() ([]string, error)) {
		step := func(line string) ([]string, error) {
			if line == "bad" {
				return nil, wantErr
			}
			return []string{line}, nil
		}
		end := func() ([]string, error) { return []string{"END"}, nil }
		return step, end
	})
	var values []string
	var errs []error
	out := cmd.Execute(context.Background(), gloo.StreamOf("a", "bad", "b"))
	for item := range out.Chan() {
		if item.Error != nil {
			errs = append(errs, item.Error)
			continue
		}
		values = append(values, item.Value)
	}
	if len(values) != 3 || values[2] != "END" {
		t.Errorf("values = %v, want survivors plus the END flush", values)
	}
	if len(errs) != 1 || !errors.Is(errs[0], wantErr) {
		t.Errorf("errors = %v, want exactly the step error", errs)
	}
}

func TestScan_EndErrorEmitted(t *testing.T) {
	wantErr := errors.New("flush failed")
	cmd := patterns.Scan(func() (func(string) ([]string, error), func() ([]string, error)) {
		step := func(line string) ([]string, error) { return []string{line}, nil }
		end := func() ([]string, error) { return nil, wantErr }
		return step, end
	})
	_, err := cmd.Execute(context.Background(), gloo.StreamOf("a")).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want the end error", err)
	}
}

func TestScan_DownstreamStopSkipsFlushAndTearsDown(t *testing.T) {
	done := make(chan struct{})
	src := gloo.Generate(context.Background(), func(_ context.Context, send func(string) bool, _ func(error)) {
		defer close(done)
		for send("x") {
		}
	})
	// Each step emits far more than the stream buffer holds, so the stop is
	// observed DURING the step phase deterministically, never after it.
	wide := make([]string, 500)
	for i := range wide {
		wide[i] = "x"
	}
	cmd := patterns.Scan(func() (func(string) ([]string, error), func() ([]string, error)) {
		step := func(string) ([]string, error) { return wide, nil }
		end := func() ([]string, error) { return []string{"END"}, nil }
		return step, end
	})
	pipe := gloo.Pipe(cmd, patterns.Take[string](2))
	got, err := pipe.Execute(context.Background(), src).Collect()
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v err=%v, want two items", got, err)
	}
	for _, v := range got {
		if v == "END" {
			t.Error("flush ran despite the downstream stop")
		}
	}
	waitClosed(t, done, "Scan under Take")
}
