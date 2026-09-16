package patterns_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func TestFilterMap_TransformsAndSelects(t *testing.T) {
	// grep -n over lines containing "x": number every line, keep matches only.
	grepN := patterns.StatefulFilterMap(func() func(string) (string, bool, error) {
		n := 0
		return func(line string) (string, bool, error) {
			n++
			if !strings.Contains(line, "x") {
				return "", false, nil
			}
			return strconv.Itoa(n) + ":" + line, true, nil
		}
	})
	got, err := grepN.Execute(context.Background(), gloo.StreamOf("ax", "b", "cx")).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "1:ax" || got[1] != "3:cx" {
		t.Errorf("got %v, want [1:ax 3:cx]", got)
	}
}

func TestFilterMap_EmitsErrorAndContinues(t *testing.T) {
	wantErr := errors.New("bad item")
	fm := patterns.FilterMap(func(s string) (string, bool, error) {
		if s == "bad" {
			return "", false, wantErr
		}
		return strings.ToUpper(s), true, nil
	})
	var values []string
	var errs []error
	out := fm.Execute(context.Background(), gloo.StreamOf("a", "bad", "b"))
	for item := range out.Chan() {
		if item.Error != nil {
			errs = append(errs, item.Error)
			continue
		}
		values = append(values, item.Value)
	}
	if len(values) != 2 || values[0] != "A" || values[1] != "B" {
		t.Errorf("values = %v, want the items around the failure", values)
	}
	if len(errs) != 1 || !errors.Is(errs[0], wantErr) {
		t.Errorf("errors = %v, want exactly the item's error", errs)
	}
}

func TestFilterMap_UpstreamErrorPropagates(t *testing.T) {
	wantErr := errors.New("upstream broke")
	src := gloo.Generate(context.Background(), func(_ context.Context, send func(string) bool, sendErr func(error)) {
		if send("a") {
			sendErr(wantErr)
		}
	})
	fm := patterns.FilterMap(func(s string) (string, bool, error) { return s, true, nil })
	_, err := fm.Execute(context.Background(), src).Collect()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want the upstream error", err)
	}
}

func TestFilterMap_DownstreamStopTearsDown(t *testing.T) {
	done := make(chan struct{})
	src := gloo.Generate(context.Background(), func(_ context.Context, send func(string) bool, _ func(error)) {
		defer close(done)
		for send("x") {
		}
	})
	fm := patterns.FilterMap(func(s string) (string, bool, error) { return s, true, nil })
	pipe := gloo.Pipe(fm, patterns.Take[string](2))
	got, err := pipe.Execute(context.Background(), src).Collect()
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v err=%v, want two items", got, err)
	}
	waitClosed(t, done, "FilterMap under Take")
}
