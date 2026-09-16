package verification

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/afero"

	gloo "github.com/gloo-foo/framework"
)

func TestFrom(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "data.txt", []byte("apple red\nbanana yellow\ncherry red"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := gloo.FileSource(fs, []gloo.File{"data.txt"})
	cmd := grepCmd("red")

	results, err := gloo.From(context.Background(), src, cmd).Collect()
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"apple red", "cherry red"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d, got %d", len(expected), len(results))
	}
	for i, res := range results {
		if res != expected[i] {
			t.Errorf("result %d: expected %q, got %q", i, expected[i], res)
		}
	}
}

func TestInto(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "data.txt", []byte("a\nb\nc"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := gloo.FileSource(fs, []gloo.File{"data.txt"})
	cmd := mapCmd(strings.ToUpper)
	sink := collectSink[string]{}

	results, err := gloo.Into(context.Background(), src, cmd, sink)
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"A", "B", "C"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d, got %d", len(expected), len(results))
	}
	for i, res := range results {
		if res != expected[i] {
			t.Errorf("result %d: expected %q, got %q", i, expected[i], res)
		}
	}
}

func TestCollect(t *testing.T) {
	stream := gloo.StreamOf("x", "y", "z")
	results, err := gloo.Collect(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"x", "y", "z"}
	if len(results) != len(want) {
		t.Fatalf("got %v, want %v", results, want)
	}
	for i, w := range want {
		if results[i] != w {
			t.Errorf("results[%d] = %q, want %q", i, results[i], w)
		}
	}
}

func TestWriteTo(t *testing.T) {
	var buf bytes.Buffer
	sink := gloo.WriteTo(&buf)
	stream := gloo.StreamOf("hello", "world")

	count, err := sink.Consume(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
	if buf.String() != "hello\nworld\n" {
		t.Errorf("expected %q, got %q", "hello\nworld\n", buf.String())
	}
}

// TestWriteToFlushesOnError verifies that lines written before a stream error
// are flushed to the underlying writer rather than discarded in the buffer.
// Regression test: Consume previously returned on error without flushing.
func TestWriteToFlushesOnError(t *testing.T) {
	var buf bytes.Buffer
	sink := gloo.WriteTo(&buf)
	stream := gloo.Generate(context.Background(), func(_ context.Context, send func(string) bool, sendErr func(error)) {
		if !send("a") {
			return
		}
		if !send("b") {
			return
		}
		sendErr(errors.New("boom"))
	})

	count, err := sink.Consume(context.Background(), stream)
	if err == nil {
		t.Fatal("expected error from stream, got nil")
	}
	if count != 2 {
		t.Errorf("expected count 2 lines written before error, got %d", count)
	}
	if buf.String() != "a\nb\n" {
		t.Errorf("expected buffered lines flushed on error, got %q", buf.String())
	}
}

func TestFromWithChain(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "data.txt", []byte("apple red\nbanana yellow\ncherry red"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := gloo.FileSource(fs, []gloo.File{"data.txt"})
	cmd := gloo.Compose(grepCmd("red")).To(mapCmd(strings.ToUpper))

	results, err := gloo.From(context.Background(), src, cmd).Collect()
	if err != nil {
		t.Fatal(err)
	}

	expected := []string{"APPLE RED", "CHERRY RED"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d, got %d", len(expected), len(results))
	}
	for i, res := range results {
		if res != expected[i] {
			t.Errorf("result %d: expected %q, got %q", i, expected[i], res)
		}
	}
}
