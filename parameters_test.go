package gloo

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

type noFlags struct{}

type markerFlags struct{ Set bool }

type markerSwitch struct{}

func (markerSwitch) Configure(f *markerFlags) { f.Set = true }

// silenceWarnings discards slog output for tests that exercise the
// ambiguous-argument paths, which log warnings by design.
func silenceWarnings(t *testing.T) {
	t.Helper()
	old := slog.Default()
	slog.SetDefault(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { slog.SetDefault(old) })
}

func TestNewParametersTypedFileArgs(t *testing.T) {
	p := NewParameters[File, noFlags](File("a.txt"), File("b.txt"))
	if len(p.Typed) != 2 || p.Typed[0] != "a.txt" || p.Typed[1] != "b.txt" {
		t.Errorf("Typed = %v", p.Typed)
	}
	if len(p.Positional) != 2 {
		t.Errorf("Positional = %v", p.Positional)
	}
	if len(p.Ambiguous) != 0 {
		t.Errorf("Ambiguous should be empty, got %v", p.Ambiguous)
	}
}

func TestNewParametersConvertsStringToFile(t *testing.T) {
	p := NewParameters[File, noFlags]("plain.txt")
	if len(p.Typed) != 1 || p.Typed[0] != File("plain.txt") {
		t.Errorf("string should convert to File, got %v", p.Typed)
	}
}

func TestNewParametersCollectsSwitches(t *testing.T) {
	p := NewParameters[File, markerFlags](markerSwitch{}, File("a.txt"))
	if !p.Flags.Set {
		t.Error("switch should configure flags")
	}
	if len(p.Typed) != 1 {
		t.Errorf("Typed = %v", p.Typed)
	}
}

func TestNewParametersAcceptsReaderForFileCommands(t *testing.T) {
	r := strings.NewReader("data")
	p := NewParameters[File, noFlags](r)
	if len(p.Positional) != 1 {
		t.Fatalf("Positional = %v", p.Positional)
	}
	if p.Positional[0] != io.Reader(r) {
		t.Error("reader should be kept as a positional argument")
	}
	if len(p.Typed) != 0 {
		t.Errorf("readers are positional but not Typed, got %v", p.Typed)
	}
	if len(p.Ambiguous) != 0 {
		t.Errorf("a reader for a File command is unambiguous, got %v", p.Ambiguous)
	}
}

func TestNewParametersRejectsReaderForNonFileCommands(t *testing.T) {
	silenceWarnings(t)
	p := NewParameters[int, noFlags](strings.NewReader("data"))
	if len(p.Ambiguous) != 1 {
		t.Errorf("reader should be ambiguous when P is not File, got %v", p.Ambiguous)
	}
	if len(p.Positional) != 0 {
		t.Errorf("Positional should be empty, got %v", p.Positional)
	}
}

func TestNewParametersRejectsInconvertibleString(t *testing.T) {
	silenceWarnings(t)
	p := NewParameters[int, noFlags]("not-an-int")
	if len(p.Ambiguous) != 1 {
		t.Errorf("string should be ambiguous when P is int, got %v", p.Ambiguous)
	}
}

func TestNewParametersRejectsUnknownType(t *testing.T) {
	silenceWarnings(t)
	p := NewParameters[File, noFlags](3.14)
	if len(p.Ambiguous) != 1 {
		t.Errorf("float should be ambiguous, got %v", p.Ambiguous)
	}
}

// --- ReadersFrom / ReaderFrom ---

func memFsWithFiles(t *testing.T, files map[string]string) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	for name, content := range files {
		if err := afero.WriteFile(fs, name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fs
}

func TestReadersFromMixedPositionalKinds(t *testing.T) {
	fs := memFsWithFiles(t, map[string]string{"f.txt": "file"})
	rc := io.NopCloser(strings.NewReader("closer"))
	p := NewParameters[File, noFlags](rc, File("f.txt"))

	readers, err := p.ReadersFrom(fs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(readers) != 2 {
		t.Fatalf("got %d readers, want 2", len(readers))
	}
	// Order and content must be preserved: the io.ReadCloser positional ("closer")
	// first, then the File ("file").
	want := []string{"closer", "file"}
	for i, r := range readers {
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("ReadAll[%d]: %v", i, err)
		}
		if string(data) != want[i] {
			t.Errorf("readers[%d] = %q, want %q", i, data, want[i])
		}
		_ = r.Close()
	}
}

func TestReadersFromWrapsPlainReader(t *testing.T) {
	p := NewParameters[File, noFlags](strings.NewReader("plain"))
	readers, err := p.ReadersFrom(afero.NewMemMapFs())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(readers) != 1 {
		t.Fatalf("got %d readers, want 1", len(readers))
	}
	if err := readers[0].Close(); err != nil {
		t.Errorf("NopCloser wrap should close cleanly: %v", err)
	}
}

func TestReadersFromMissingFile(t *testing.T) {
	p := NewParameters[File, noFlags](File("missing.txt"))
	_, err := p.ReadersFrom(afero.NewMemMapFs())
	if !errors.Is(err, ErrFileNotFound) {
		t.Errorf("got %v, want ErrFileNotFound", err)
	}
}

func TestReadersFromSkipsUnreadablePositional(t *testing.T) {
	// A positional that is neither a reader nor a File is skipped, not opened.
	p := Parameters[File, noFlags]{Positional: []any{42}}
	readers, err := p.ReadersFrom(afero.NewMemMapFs())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(readers) != 0 {
		t.Errorf("unreadable positional should be skipped, got %d readers", len(readers))
	}
}

func TestReadersFromClosesAlreadyOpenedOnError(t *testing.T) {
	// When a later File fails to open, readers opened earlier must be closed.
	opened := &trackingCloser{Reader: strings.NewReader("early")}
	p := NewParameters[File, noFlags](opened, File("missing.txt"))
	_, err := p.ReadersFrom(afero.NewMemMapFs())
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("got %v, want ErrFileNotFound", err)
	}
	if !opened.closed {
		t.Error("reader opened before the failure should be closed")
	}
}

func TestReaderFromConcatenatesFiles(t *testing.T) {
	fs := memFsWithFiles(t, map[string]string{"a.txt": "aaa", "b.txt": "bbb"})
	p := NewParameters[File, noFlags](File("a.txt"), File("b.txt"))

	r, err := p.ReaderFrom(fs, strings.NewReader("stdin-unused"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = r.Close() }()

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "aaabbb" {
		t.Errorf("got %q, want %q (files concatenated in order)", data, "aaabbb")
	}
}

func TestReaderFromFallsBackToStdin(t *testing.T) {
	p := NewParameters[File, noFlags]()
	r, err := p.ReaderFrom(afero.NewMemMapFs(), strings.NewReader("from-stdin"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(r)
	if string(data) != "from-stdin" {
		t.Errorf("got %q, want stdin content", data)
	}
	if err := r.Close(); err != nil {
		t.Errorf("stdin fallback Close should be a no-op: %v", err)
	}
}

func TestReaderFromMissingFileError(t *testing.T) {
	p := NewParameters[File, noFlags](File("missing.txt"))
	_, err := p.ReaderFrom(afero.NewMemMapFs(), strings.NewReader(""))
	if !errors.Is(err, ErrFileNotFound) {
		t.Errorf("got %v, want ErrFileNotFound", err)
	}
}

// --- multiReadCloser ---

type trackingCloser struct {
	io.Reader
	err    error
	closed bool
}

func (c *trackingCloser) Close() error {
	c.closed = true
	return c.err
}

func TestMultiReadCloserClosesAll(t *testing.T) {
	a := &trackingCloser{Reader: strings.NewReader("a")}
	b := &trackingCloser{Reader: strings.NewReader("b")}
	m := &multiReadCloser{
		Reader:  io.MultiReader(a, b),
		closers: []io.ReadCloser{a, b},
	}
	if err := m.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !a.closed || !b.closed {
		t.Error("all underlying readers should be closed")
	}
}

func TestMultiReadCloserJoinsErrors(t *testing.T) {
	errA := errors.New("close a failed")
	errB := errors.New("close b failed")
	a := &trackingCloser{Reader: strings.NewReader("a"), err: errA}
	ok := &trackingCloser{Reader: strings.NewReader("ok")}
	b := &trackingCloser{Reader: strings.NewReader("b"), err: errB}
	m := &multiReadCloser{closers: []io.ReadCloser{a, ok, b}}

	err := m.Close()
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Errorf("Close should join both errors, got %v", err)
	}
	if !ok.closed {
		t.Error("a failing Close must not stop later closers")
	}
}

func TestReaderFallsBackToStdinWithoutTouchingFilesystem(t *testing.T) {
	// Reader uses the OS filesystem only to open File args; with no positional
	// args it returns a no-op wrapper around stdin and never touches disk.
	p := NewParameters[File, noFlags]()
	r, err := p.Reader(strings.NewReader("from-stdin"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := io.ReadAll(r)
	if string(data) != "from-stdin" {
		t.Errorf("got %q, want stdin content", data)
	}
	if err := r.Close(); err != nil {
		t.Errorf("stdin fallback Close should be a no-op: %v", err)
	}
}

// A command whose positional type IS string must classify plain strings as
// typed positionals — the case where the string classification arm and the
// typed arm coincide.
func TestNewParametersStringPositionals(t *testing.T) {
	p := NewParameters[string, struct{}]("a", "b")
	if len(p.Typed) != 2 || p.Typed[0] != "a" || p.Typed[1] != "b" {
		t.Errorf("Typed = %v, want [a b]", p.Typed)
	}
	if len(p.Positional) != 2 {
		t.Errorf("Positional = %v, want 2 entries", p.Positional)
	}
	if len(p.Ambiguous) != 0 {
		t.Errorf("Ambiguous = %v, want none", p.Ambiguous)
	}
}
