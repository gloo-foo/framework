package gloo

import (
	"errors"
	"io"
	"log/slog"
	"reflect"

	"github.com/spf13/afero"
)

const ErrFileNotFound Error = "file not found"

// File is a positional argument naming a filesystem path.
type File string

// Warning messages emitted when an argument cannot be classified as a
// positional of the command's expected type.
const (
	warnReaderUnsupported = "io.Reader not supported for this command type"
	warnStringUnsupported = "string not supported for this command type (use a custom type or File)"
	warnUnknownArgument   = "unknown argument type"
)

var (
	stringType = reflect.TypeFor[string]()
	fileType   = reflect.TypeFor[File]()
)

// NewParameters classifies a heterogeneous argument list into typed positional
// values, Switch[F] flags, and an ambiguous bin. Files named by File
// positionals are opened lazily by Reader/ReadersFrom, not here.
func NewParameters[P, F any](parameters ...any) Parameters[P, F] {
	var b paramBuilder[P, F]
	for _, arg := range parameters {
		b = b.add(arg)
	}
	return b.build()
}

// Parameters holds parsed command parameters. It is an immutable value.
type Parameters[P any, F any] struct {
	Typed      []P
	Positional []any
	Flags      F
	Ambiguous  []any
}

// paramBuilder accumulates classified arguments. It is an immutable value:
// each classification step returns an updated builder.
type paramBuilder[P any, F any] struct {
	typed      []P
	positional []any
	ambiguous  []any
	options    []Switch[F]
}

// add classifies a single argument into the appropriate bin.
func (b paramBuilder[P, F]) add(arg any) paramBuilder[P, F] {
	switch v := arg.(type) {
	case Switch[F]:
		b.options = append(b.options, v)
		return b
	case io.Reader:
		return b.addReader(v)
	case string:
		return b.addString(v)
	case P:
		return b.addTyped(v)
	default:
		return b.reject(warnUnknownArgument, v)
	}
}

// addReader accepts an io.Reader positional only for File-typed commands.
func (b paramBuilder[P, F]) addReader(r io.Reader) paramBuilder[P, F] {
	if reflect.TypeFor[P]() != fileType {
		return b.reject(warnReaderUnsupported, r)
	}
	b.positional = append(b.positional, r)
	return b
}

// addString converts a string to the positional type P when convertible.
func (b paramBuilder[P, F]) addString(s string) paramBuilder[P, F] {
	v, ok := convertTo[P](argumentText(s))
	if !ok {
		return b.reject(warnStringUnsupported, s)
	}
	return b.addTyped(v)
}

// addTyped records a value already of the positional type P.
func (b paramBuilder[P, F]) addTyped(v P) paramBuilder[P, F] {
	b.positional = append(b.positional, v)
	b.typed = append(b.typed, v)
	return b
}

// reject logs and bins an argument that does not fit the command's shape.
func (b paramBuilder[P, F]) reject(reason string, v any) paramBuilder[P, F] {
	slog.Warn(reason, "argument", v)
	b.ambiguous = append(b.ambiguous, v)
	return b
}

// build assembles the immutable Parameters result.
func (b paramBuilder[P, F]) build() Parameters[P, F] {
	return Parameters[P, F]{
		Typed:      b.typed,
		Positional: b.positional,
		Flags:      configure(b.options...),
		Ambiguous:  b.ambiguous,
	}
}

// argumentText is the raw text of a positional argument awaiting conversion to
// the command's positional type.
type argumentText string

// convertTo converts an argument's text to P via reflection when string is
// convertible to P (e.g. File or any named string type). Returns false otherwise.
func convertTo[P any](s argumentText) (P, bool) {
	tP := reflect.TypeFor[P]()
	if !stringType.ConvertibleTo(tP) {
		var zero P
		return zero, false
	}
	return reflect.ValueOf(string(s)).Convert(tP).Interface().(P), true
}

// ReadersFrom opens file handles from positional args using the given
// filesystem. On error, all previously opened handles are closed.
func (p Parameters[P, F]) ReadersFrom(fs afero.Fs) ([]io.ReadCloser, error) {
	var r []io.ReadCloser
	for _, arg := range p.Positional {
		next, err := appendOpened(fs, r, arg)
		if err != nil {
			closeAll(r)
			return nil, err
		}
		r = next
	}
	return r, nil
}

// appendOpened opens one positional argument and appends it to r, skipping
// argument kinds that are not readable.
func appendOpened(fs afero.Fs, r []io.ReadCloser, arg any) ([]io.ReadCloser, error) {
	rc, err := openPositional(fs, arg)
	if err != nil {
		return nil, err
	}
	if rc == nil {
		return r, nil
	}
	return append(r, rc), nil
}

// openPositional opens a single positional argument as an io.ReadCloser, or
// returns (nil, nil) for argument kinds that are not readable.
func openPositional(fs afero.Fs, arg any) (io.ReadCloser, error) {
	switch v := arg.(type) {
	case io.ReadCloser:
		return v, nil
	case io.Reader:
		return io.NopCloser(v), nil
	case File:
		return openFile(fs, v)
	}
	return nil, nil
}

// openFile opens a File positional, wrapping open errors as ErrFileNotFound.
func openFile(fs afero.Fs, name File) (io.ReadCloser, error) {
	fd, err := fs.Open(string(name))
	if err != nil {
		return nil, ErrFileNotFound.With(err, "file", string(name))
	}
	return fd, nil
}

// closeAll closes every reader, ignoring individual errors — best-effort
// cleanup on an error path.
func closeAll(closers []io.ReadCloser) {
	for _, c := range closers {
		_ = c.Close()
	}
}

// Reader returns a combined reader over all positional arguments, falling back
// to stdin when there are none, using the OS filesystem for File positionals.
//
// The returned io.ReadCloser owns the file handles it opened: callers MUST
// Close it to release them. Closing also closes any positional io.ReadCloser
// arguments; the fallback stdin reader is wrapped so Close is a no-op.
func (p Parameters[P, F]) Reader(stdin io.Reader) (io.ReadCloser, error) {
	return p.ReaderFrom(afero.NewOsFs(), stdin)
}

// ReaderFrom is Reader with an explicit filesystem for opening File positionals.
func (p Parameters[P, F]) ReaderFrom(fs afero.Fs, stdin io.Reader) (io.ReadCloser, error) {
	readers, err := p.ReadersFrom(fs)
	if err != nil {
		return nil, err
	}
	return combine(readers, stdin), nil
}

// combine joins opened readers into one ReadCloser, or wraps stdin when none
// were opened.
func combine(readers []io.ReadCloser, stdin io.Reader) io.ReadCloser {
	if len(readers) == 0 {
		return io.NopCloser(stdin)
	}
	plain := make([]io.Reader, len(readers))
	for i, rc := range readers {
		plain[i] = rc
	}
	return multiReadCloser{Reader: io.MultiReader(plain...), closers: readers}
}

// multiReadCloser combines a MultiReader with the Closers of its underlying
// readers, so closing it releases every file handle opened.
type multiReadCloser struct {
	io.Reader
	closers []io.ReadCloser
}

// Close closes all underlying readers, joining any errors. Value receiver: it
// only reads the closers slice.
func (m multiReadCloser) Close() error {
	var errs []error
	for _, c := range m.closers {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
