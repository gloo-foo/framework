package verification

import (
	"bytes"
	"context"
	"errors"
	"io"
	iofs "io/fs"
	"testing"

	"github.com/spf13/afero"

	gloo "github.com/gloo-foo/framework"
)

func TestByteFileSourceMultipleFiles(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "a.txt", []byte("line1\nline2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "b.txt", []byte("line3\nline4"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := gloo.ByteFileSource(fs, []gloo.File{"a.txt", "b.txt"})
	results, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("ByteFileSource failed: %v", err)
	}

	expected := []string{"line1", "line2", "line3", "line4"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d lines, got %d", len(expected), len(results))
	}
	for i, res := range results {
		if string(res) != expected[i] {
			t.Errorf("line %d: expected %q, got %q", i, expected[i], string(res))
		}
	}
}

func TestByteFileSourceIndependentSlices(t *testing.T) {
	// Each returned []byte must be an independent copy, not sharing the scan buffer.
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "test.txt", []byte("aaa\nbbb\nccc"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := gloo.ByteFileSource(fs, []gloo.File{"test.txt"})
	results, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatal(err)
	}

	// Modify the first result and check it doesn't affect the second.
	results[0][0] = 'z'
	if string(results[1]) != "bbb" {
		t.Errorf("modifying result[0] affected result[1]: got %q", string(results[1]))
	}
}

func TestByteReaderSource(t *testing.T) {
	src := gloo.ByteReaderSource([]io.Reader{
		bytes.NewReader([]byte("hello\nworld")),
		bytes.NewReader([]byte("foo\nbar")),
	})
	results, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("ByteReaderSource failed: %v", err)
	}

	expected := []string{"hello", "world", "foo", "bar"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d lines, got %d", len(expected), len(results))
	}
	for i, res := range results {
		if string(res) != expected[i] {
			t.Errorf("line %d: expected %q, got %q", i, expected[i], string(res))
		}
	}
}

func TestByteFileSourceNonExistent(t *testing.T) {
	fs := afero.NewMemMapFs()
	src := gloo.ByteFileSource(fs, []gloo.File{"missing.txt"})
	_, err := src.Stream(context.Background()).Collect()
	if !errors.Is(err, iofs.ErrNotExist) {
		t.Fatalf("got %v, want fs.ErrNotExist", err)
	}
}

func TestByteFileSourceEmpty(t *testing.T) {
	fs := afero.NewMemMapFs()
	src := gloo.ByteFileSource(fs, nil)
	results, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty, got %d items", len(results))
	}
}
