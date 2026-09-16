package verification

// Coverage of the early-stop branches in concrete sources and collectors:
// when a downstream Take stops reading, SliceSource/ReaderSource stop emitting
// and Accumulate stops mid-emission rather than running to completion.

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/spf13/afero"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func TestEarlyStop_SliceSourcePartial(t *testing.T) {
	big := make([]int, 1000)
	for i := range big {
		big[i] = i
	}
	src := gloo.SliceSource(big)
	got, err := patterns.Take[int](2).
		Execute(context.Background(), src.Stream(context.Background())).
		Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("got %v, want [0 1]", got)
	}
}

// largeBytes builds input far larger than the scanner's initial read buffer so
// an early-stopping scan provably leaves bytes unread (a single Read cannot
// slurp it all).
func largeBytes(line string) string {
	return strings.Repeat(line, 100_000)
}

// countingReader records how many bytes it has served downstream.
type countingReader struct {
	r      io.Reader
	served int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.served += n
	return n, err
}

func TestEarlyStop_ReaderSourcePartial(t *testing.T) {
	full := largeBytes("line\n")
	cr := &countingReader{r: strings.NewReader(full)}
	src := gloo.ReaderSource([]io.Reader{cr})
	got, err := patterns.Take[string](1).
		Execute(context.Background(), src.Stream(context.Background())).
		Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "line" {
		t.Errorf("got %v, want [line]", got)
	}
	if cr.served >= len(full) {
		t.Errorf("reader served all %d bytes; the source did not stop early", len(full))
	}
}

// countingFs counts bytes read from the files it opens.
type countingFs struct {
	afero.Fs
	served *int
}

func (c countingFs) Open(name string) (afero.File, error) {
	f, err := c.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return countingFile{File: f, served: c.served}, nil
}

type countingFile struct {
	afero.File
	served *int
}

func (c countingFile) Read(p []byte) (int, error) {
	n, err := c.File.Read(p)
	*c.served += n
	return n, err
}

func TestEarlyStop_FileSourcePartial(t *testing.T) {
	mem := afero.NewMemMapFs()
	full := []byte(largeBytes("row\n"))
	if err := afero.WriteFile(mem, "big.txt", full, 0o644); err != nil {
		t.Fatal(err)
	}
	served := 0
	fs := countingFs{Fs: mem, served: &served}
	src := gloo.FileSource(fs, []gloo.File{"big.txt"})

	got, err := patterns.Take[string](1).
		Execute(context.Background(), src.Stream(context.Background())).
		Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "row" {
		t.Errorf("got %v, want [row]", got)
	}
	if served >= len(full) {
		t.Errorf("file served all %d bytes; the source did not stop early", len(full))
	}
}

// Accumulate emits its whole result, but a downstream Take can stop it
// mid-emission; the un-emitted tail is simply dropped (no error, no hang).
// The input must exceed the stage buffer so emission actually blocks and the
// stop is observed mid-send.
func TestEarlyStop_AccumulateMidEmit(t *testing.T) {
	n := 500
	xs := make([]int, n)
	for i := range xs {
		xs[i] = n - i // descending, so sort must reorder
	}
	src := gloo.SliceSource(xs)
	sortThenTake := gloo.Compose[int](sortInts()).To(patterns.Take[int](2))

	got, err := sortThenTake.
		Execute(context.Background(), src.Stream(context.Background())).
		Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("got %v, want [1 2]", got)
	}
}
