package gloo

import (
	"bytes"
	"errors"
	"testing"
)

// failAfterWriter succeeds for the first n Write calls, then fails.
type failAfterWriter struct {
	err error
	n   int
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if w.n <= 0 {
		return 0, w.err
	}
	w.n--
	return len(p), nil
}

func TestOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := Output(&buf, []byte("hello")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "hello\n" {
		t.Errorf("got %q, want %q", buf.String(), "hello\n")
	}
}

func TestOutputWithTerminator(t *testing.T) {
	var buf bytes.Buffer
	if err := OutputWithTerminator(&buf, 0, []byte("record")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "record\x00" {
		t.Errorf("got %q, want %q", buf.String(), "record\x00")
	}
}

func TestOutputWithTerminatorDataWriteFails(t *testing.T) {
	wantErr := errors.New("disk full")
	w := &failAfterWriter{n: 0, err: wantErr}
	if err := OutputWithTerminator(w, NewLine, []byte("data")); !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestOutputWithTerminatorTerminatorWriteFails(t *testing.T) {
	wantErr := errors.New("disk full")
	w := &failAfterWriter{n: 1, err: wantErr}
	if err := OutputWithTerminator(w, NewLine, []byte("data")); !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestOutputString(t *testing.T) {
	var buf bytes.Buffer
	if err := OutputString(&buf, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "hello\n" {
		t.Errorf("got %q, want %q", buf.String(), "hello\n")
	}
}

func TestOutputStringEmptyLine(t *testing.T) {
	var buf bytes.Buffer
	if err := OutputString(&buf, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "\n" {
		t.Errorf("got %q, want bare newline", buf.String())
	}
}

func TestOutputStringWithTerminatorDataWriteFails(t *testing.T) {
	wantErr := errors.New("broken pipe")
	w := &failAfterWriter{n: 0, err: wantErr}
	if err := OutputStringWithTerminator(w, NewLine, "data"); !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestOutputStringWithTerminatorTerminatorWriteFails(t *testing.T) {
	wantErr := errors.New("broken pipe")
	w := &failAfterWriter{n: 1, err: wantErr}
	if err := OutputStringWithTerminator(w, NewLine, "data"); !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}
