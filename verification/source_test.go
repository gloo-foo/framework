package verification

import (
	"context"
	"errors"
	"io"
	iofs "io/fs"
	"strings"
	"testing"

	"github.com/spf13/afero"

	gloo "github.com/gloo-foo/framework"
)

// T012: FileSource with afero.NewMemMapFs and multiple in-memory files.
func TestFileSourceMultipleFiles(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "a.txt", []byte("line1\nline2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "b.txt", []byte("line3\nline4"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := gloo.FileSource(fs, []gloo.File{"a.txt", "b.txt"})
	results, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("FileSource failed: %v", err)
	}

	expected := []string{"line1", "line2", "line3", "line4"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d lines, got %d: %v", len(expected), len(results), results)
	}
	for i, res := range results {
		if res != expected[i] {
			t.Errorf("line %d: expected %q, got %q", i, expected[i], res)
		}
	}
}

// T013: ReaderSource with strings.NewReader inputs.
func TestReaderSource(t *testing.T) {
	src := gloo.ReaderSource([]io.Reader{
		strings.NewReader("hello\nworld"),
		strings.NewReader("foo\nbar"),
	})
	results, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("ReaderSource failed: %v", err)
	}

	expected := []string{"hello", "world", "foo", "bar"}
	if len(results) != len(expected) {
		t.Fatalf("expected %d lines, got %d: %v", len(expected), len(results), results)
	}
	for i, res := range results {
		if res != expected[i] {
			t.Errorf("line %d: expected %q, got %q", i, expected[i], res)
		}
	}
}

// T014: FileSource with zero files — empty stream.
func TestFileSourceZeroFiles(t *testing.T) {
	fs := afero.NewMemMapFs()
	src := gloo.FileSource(fs, nil)
	results, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("FileSource with zero files failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty stream, got %d items", len(results))
	}
}

// T015: FileSource with non-existent file — error sent via stream, other files still processed.
func TestFileSourceNonExistentFile(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "exists.txt", []byte("good"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Collect stops at the first error, so range the channel directly to observe
	// BOTH the missing-file error AND that the good file is still processed.
	src := gloo.FileSource(fs, []gloo.File{"missing.txt", "exists.txt"})
	var got []string
	var gotErr error
	for item := range src.Stream(context.Background()).Chan() {
		if item.Error != nil {
			gotErr = item.Error
			continue
		}
		got = append(got, item.Value)
	}

	if !errors.Is(gotErr, iofs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist for missing file, got %v", gotErr)
	}
	if len(got) != 1 || got[0] != "good" {
		t.Errorf("good file content = %v, want [good] (other files still processed)", got)
	}
}

// Long lines: shell tools (cat, grep, sed) handle lines of arbitrary length.
// bufio.Scanner's default 64KB token limit must not abort the pipeline.
func TestFileSourceLongLine(t *testing.T) {
	fs := afero.NewMemMapFs()
	long := strings.Repeat("x", 256*1024) // 256KB single line
	if err := afero.WriteFile(fs, "big.txt", []byte("before\n"+long+"\nafter"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := gloo.FileSource(fs, []gloo.File{"big.txt"})
	results, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("FileSource failed on long line: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(results))
	}
	if results[1] != long {
		t.Errorf("long line corrupted: got %d bytes, want %d", len(results[1]), len(long))
	}
}

// Long lines via ReaderSource (and []byte variant) — same guarantee.
func TestByteReaderSourceLongLine(t *testing.T) {
	long := strings.Repeat("y", 256*1024)
	src := gloo.ByteReaderSource([]io.Reader{strings.NewReader(long)})
	results, err := src.Stream(context.Background()).Collect()
	if err != nil {
		t.Fatalf("ByteReaderSource failed on long line: %v", err)
	}
	if len(results) != 1 || len(results[0]) != len(long) {
		t.Fatalf("expected 1 line of %d bytes, got %v lines", len(long), len(results))
	}
}
