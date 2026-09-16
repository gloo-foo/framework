package patterns_test

import (
	"context"
	"errors"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func TestFail_EmitsExactlyTheError(t *testing.T) {
	wantErr := errors.New("pattern does not compile")
	out := patterns.Fail[string](wantErr).Execute(context.Background(), gloo.StreamOf("a", "b"))
	var values []string
	var errs []error
	for item := range out.Chan() {
		if item.Error != nil {
			errs = append(errs, item.Error)
			continue
		}
		values = append(values, item.Value)
	}
	if len(values) != 0 {
		t.Errorf("values = %v, want none", values)
	}
	if len(errs) != 1 || !errors.Is(errs[0], wantErr) {
		t.Errorf("errors = %v, want exactly the construction error", errs)
	}
}

func TestFail_StopsEndlessUpstream(t *testing.T) {
	done := make(chan struct{})
	src := gloo.Generate(context.Background(), func(_ context.Context, send func(string) bool, _ func(error)) {
		defer close(done)
		for send("x") {
		}
	})
	_, err := patterns.Fail[string](errors.New("boom")).Execute(context.Background(), src).Collect()
	if err == nil {
		t.Fatal("expected the failure to surface")
	}
	waitClosed(t, done, "Fail over endless upstream")
}
