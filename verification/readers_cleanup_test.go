package verification

import (
	"testing"

	"github.com/spf13/afero"

	gloo "github.com/gloo-foo/framework"
)

// closeCountingFs wraps an afero.Fs so every successfully opened handle records
// its Close calls. MemMapFs alone cannot observe Close, so this wrapper makes
// the cleanup on ReadersFrom's error path observable.
type closeCountingFs struct {
	afero.Fs
	closes map[string]int
}

func (c closeCountingFs) Open(name string) (afero.File, error) {
	f, err := c.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &closeCountingFile{File: f, name: name, closes: c.closes}, nil
}

type closeCountingFile struct {
	afero.File
	closes map[string]int
	name   string
}

func (f *closeCountingFile) Close() error {
	f.closes[f.name]++
	return f.File.Close()
}

func TestReadersFromLeak(t *testing.T) {
	// When ReadersFrom hits an error mid-loop, previously opened handles must be
	// closed exactly once.
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "exists.txt", []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	// "missing.txt" does not exist — opening it fails AFTER "exists.txt" is open,
	// so the loop must close the "exists.txt" handle on the way out.
	closes := map[string]int{}
	fs := closeCountingFs{Fs: mem, closes: closes}

	p := gloo.NewParameters[gloo.File, struct{}](gloo.File("exists.txt"), gloo.File("missing.txt"))
	_, err := p.ReadersFrom(fs)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if closes["exists.txt"] != 1 {
		t.Errorf("exists.txt closed %d times, want exactly 1 (no leak, no double close)", closes["exists.txt"])
	}
}
