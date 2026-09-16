package verification

import (
	"errors"
	"fmt"
	"testing"

	gloo "github.com/gloo-foo/framework"
)

const testSentinel gloo.Error = "test error"

func TestErrorWithNilErrAndArgs(t *testing.T) {
	// BUG-1 fix: sentinel must be preserved when err is nil but args are provided.
	result := testSentinel.With(nil, "extra context")
	if result == nil {
		t.Fatal("expected non-nil error, got nil")
	}
	if !errors.Is(result, testSentinel) {
		t.Errorf("expected error to wrap sentinel %q, got %q", testSentinel, result)
	}
	if errors.Is(result, gloo.Error("different")) {
		t.Error("expected the wrapped error not to match an unrelated sentinel")
	}
}

func TestErrorWithErrAndArgs(t *testing.T) {
	inner := fmt.Errorf("inner failure")
	result := testSentinel.With(inner, "detail")
	if !errors.Is(result, testSentinel) {
		t.Error("expected sentinel to be wrapped")
	}
	if !errors.Is(result, inner) {
		t.Error("expected inner error to be wrapped")
	}
}

func TestErrorWithNilErrNoArgs(t *testing.T) {
	result := testSentinel.With(nil)
	if result == nil {
		t.Fatal("expected sentinel, got nil")
	}
	if !errors.Is(result, testSentinel) {
		t.Error("expected sentinel to be returned as-is")
	}
}

func TestErrorWithErrOnly(t *testing.T) {
	inner := fmt.Errorf("wrapped")
	result := testSentinel.With(inner)
	if !errors.Is(result, testSentinel) {
		t.Error("expected sentinel")
	}
	if !errors.Is(result, inner) {
		t.Error("expected inner")
	}
}
