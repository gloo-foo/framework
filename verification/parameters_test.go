package verification

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/afero"

	gloo "github.com/gloo-foo/framework"
)

// T031: NewParameters with File positional args — verify Typed and Positional fields.
func TestNewParametersFileArgs(t *testing.T) {
	p := gloo.NewParameters[gloo.File, struct{}](gloo.File("a.txt"), gloo.File("b.txt"))

	if len(p.Typed) != 2 {
		t.Fatalf("expected 2 Typed, got %d", len(p.Typed))
	}
	if p.Typed[0] != "a.txt" || p.Typed[1] != "b.txt" {
		t.Errorf("Typed: expected [a.txt, b.txt], got %v", p.Typed)
	}
	if len(p.Positional) != 2 {
		t.Fatalf("expected 2 Positional, got %d", len(p.Positional))
	}
}

// T032: NewParameters with string args convertible to P — verify string conversion.
func TestNewParametersStringConversion(t *testing.T) {
	p := gloo.NewParameters[gloo.File, struct{}]("a.txt", "b.txt")

	if len(p.Typed) != 2 {
		t.Fatalf("expected 2 Typed, got %d", len(p.Typed))
	}
	if p.Typed[0] != "a.txt" || p.Typed[1] != "b.txt" {
		t.Errorf("Typed: expected [a.txt, b.txt], got %v", p.Typed)
	}
}

// T033: NewParameters with Switch flags — verify Flags field configured.
type testFlags struct {
	Verbose bool
}

type verboseSwitch struct{}

func (v verboseSwitch) Configure(f *testFlags) {
	f.Verbose = true
}

func TestNewParametersSwitchFlags(t *testing.T) {
	p := gloo.NewParameters[gloo.File, testFlags](verboseSwitch{})

	if !p.Flags.Verbose {
		t.Error("expected Verbose flag to be true")
	}
}

// T034: NewParameters with unknown arg types — verify Ambiguous field populated.
func TestNewParametersAmbiguous(t *testing.T) {
	p := gloo.NewParameters[gloo.File, struct{}](42, true)

	if len(p.Ambiguous) != 2 {
		t.Fatalf("expected 2 Ambiguous, got %d", len(p.Ambiguous))
	}
	// The exact unclassifiable values land in Ambiguous, in order...
	if p.Ambiguous[0] != 42 || p.Ambiguous[1] != true {
		t.Errorf("Ambiguous = %v, want [42 true]", p.Ambiguous)
	}
	// ...and nothing leaks into Typed.
	if len(p.Typed) != 0 {
		t.Errorf("Typed = %v, want empty (unclassifiable args are not typed)", p.Typed)
	}
}

// T035: ReadersFrom(afero.NewMemMapFs()) — open file handles from File args.
func TestReadersFromMemMapFs(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "test.txt", []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := gloo.NewParameters[gloo.File, struct{}](gloo.File("test.txt"))

	readers, err := p.ReadersFrom(fs)
	if err != nil {
		t.Fatalf("ReadersFrom failed: %v", err)
	}
	if len(readers) != 1 {
		t.Fatalf("expected 1 reader, got %d", len(readers))
	}

	data, err := io.ReadAll(readers[0])
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(data) != "content" {
		t.Errorf("expected %q, got %q", "content", string(data))
	}
	_ = readers[0].Close()
}

// T036: Reader(stdin) returns stdin when no positional args.
func TestReaderFallbackToStdin(t *testing.T) {
	p := gloo.NewParameters[gloo.File, struct{}]()

	stdin := strings.NewReader("from stdin")
	reader, err := p.Reader(stdin)
	if err != nil {
		t.Fatalf("Reader failed: %v", err)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(data) != "from stdin" {
		t.Errorf("expected %q, got %q", "from stdin", string(data))
	}
}

// Reader returns an io.ReadCloser so callers can release the file handles it
// opens. Regression test: it previously returned a bare io.Reader backed by an
// unclosable io.MultiReader, leaking every opened file descriptor. Here we
// exercise the stdin fallback (the file-opening path uses the OS filesystem,
// which tests must not touch) and confirm the result is safely closable.
func TestReaderIsClosable(t *testing.T) {
	p := gloo.NewParameters[gloo.File, struct{}]()

	var rc io.ReadCloser // compile-time assertion of the closable return type
	rc, err := p.Reader(strings.NewReader("data"))
	if err != nil {
		t.Fatalf("Reader failed: %v", err)
	}
	if rc == nil {
		t.Fatal("Reader returned nil ReadCloser")
	}
	if err := rc.Close(); err != nil {
		t.Errorf("Close on stdin-backed reader should be a no-op, got: %v", err)
	}
}
