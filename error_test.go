package gloo

import (
	"errors"
	"testing"

	errs "github.com/gomatic/go-error"
)

// Consumer-contract tests for the framework's sentinel-error type. The
// mechanism (Error()/With) lives upstream in github.com/gomatic/go-error;
// these tests prove the gloo.Error alias preserves exactly what consumers
// rely on: const declarations, .With wrapping, and errors.Is matching.

// errSentinel is declared exactly as every consumer declares one.
const errSentinel Error = "something broke"

func TestErrorImplementsError(t *testing.T) {
	var err error = errSentinel
	if err.Error() != "something broke" {
		t.Errorf("Error() = %q, want the declared constant text", err.Error())
	}
}

func TestErrorIsErrsConst(t *testing.T) {
	// The alias contract: a gloo.Error IS an errs.Const — same type, so a
	// sentinel declared either way with the same text is the same error value
	// under errors.Is. This is what lets consumers migrate to errs.Const
	// directly without breaking matches against framework sentinels.
	if !errors.Is(errSentinel, errs.Const("something broke")) {
		t.Error("gloo.Error and errs.Const with the same text must be the same sentinel")
	}
	if !errors.Is(errSentinel.With(nil, "ctx"), errs.Const("something broke")) {
		t.Error("a wrapped gloo.Error must match the equivalent errs.Const sentinel")
	}
}

func TestErrorWith(t *testing.T) {
	cause := errors.New("underlying cause")

	tests := []struct {
		cause     error
		name      string
		args      []any
		wantIs    []error
		wantIsNot []error
	}{
		{
			name:      "nil cause no args returns the sentinel",
			cause:     nil,
			args:      nil,
			wantIs:    []error{errSentinel},
			wantIsNot: []error{Error("different")},
		},
		{
			name:      "wraps cause",
			cause:     cause,
			args:      nil,
			wantIs:    []error{errSentinel, cause},
			wantIsNot: []error{Error("different")},
		},
		{
			name:      "args alone still match the sentinel",
			cause:     nil,
			args:      []any{"file", "x.txt"},
			wantIs:    []error{errSentinel},
			wantIsNot: []error{cause},
		},
		{
			name:      "cause and args match sentinel and cause",
			cause:     cause,
			args:      []any{"path", "/tmp/missing"},
			wantIs:    []error{errSentinel, cause},
			wantIsNot: []error{Error("different")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errSentinel.With(tt.cause, tt.args...)
			if err == nil {
				t.Fatal("With must never return nil")
			}
			for _, target := range tt.wantIs {
				if !errors.Is(err, target) {
					t.Errorf("errors.Is(err, %v) = false, want true", target)
				}
			}
			for _, target := range tt.wantIsNot {
				if errors.Is(err, target) {
					t.Errorf("errors.Is(err, %v) = true, want false", target)
				}
			}
		})
	}
}

func TestExitStatusRendersAndMatches(t *testing.T) {
	err := error(ExitStatus(1))
	if err.Error() != "exit status 1" {
		t.Errorf("got %q, want the conventional rendering", err.Error())
	}
	if !errors.Is(err, ExitStatus(1)) {
		t.Error("errors.Is must match the same status")
	}
	if errors.Is(err, ExitStatus(2)) {
		t.Error("distinct statuses must not match")
	}
	var status ExitStatus
	if !errors.As(err, &status) || status.Status() != 1 {
		t.Errorf("errors.As extracted %v, want status 1", status)
	}
}

// An ExitStatus wrapped in a stream error chain still extracts — the shape a
// binary wrapper relies on.
func TestExitStatusSurvivesWrapping(t *testing.T) {
	const wrapper Error = "grep"
	err := wrapper.With(ExitStatus(1))
	var status ExitStatus
	if !errors.As(err, &status) || status.Status() != 1 {
		t.Errorf("got %v, want the wrapped status", err)
	}
}
