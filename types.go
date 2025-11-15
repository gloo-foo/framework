package gloo

import (
	"context"
	"io"
)

// ============================================================================
// Types for Command Developers
// ============================================================================

// File represents a file path that should be opened for reading.
// When used as positional type with Initialize, the framework automatically
// opens the files. If no files are provided, stdin is used.
//
// Example:
//
//	inputs := gloo.Initialize[gloo.File, flags](params...)
//	// Framework automatically opens files and handles stdin
type File string

// Inputs holds parsed command parameters: positional arguments, flags, and I/O streams.
// This is the primary type command developers work with.
//
// Type Parameters:
//   - T: Type of positional arguments (e.g., gloo.File, string, custom types)
//   - O: Type of flags struct
//
// Example:
//
//	type command gloo.Inputs[gloo.File, myFlags]
//
//	func (c command) Executor() gloo.CommandExecutor {
//	    inputs := gloo.Inputs[gloo.File, myFlags](c)
//	    return inputs.Wrap(...)
//	}
type Inputs[T any, O any] struct {
	Positional []T      // Parsed positional arguments
	Flags      O        // Parsed flags
	Ambiguous  []any    // Arguments that couldn't be parsed
	stdin      []reader // Internal: opened input streams
	stdout     writer   // Internal: output stream configuration
	stderr     writer   // Internal: error stream configuration
}

// Readers returns the opened readers for commands that need direct access to io.Reader.
// This allows commands to work with readers without caring about file paths.
//
// Example:
//
//	for _, r := range inputs.Readers() {
//	    scanner := bufio.NewScanner(r)
//	    // ... process each file separately
//	}
func (inputs Inputs[T, O]) Readers() []io.Reader {
	readers := make([]io.Reader, len(inputs.stdin))
	for i, r := range inputs.stdin {
		readers[i] = r.reader
	}
	return readers
}

// Reader returns a single io.Reader that combines all stdin sources.
// If multiple files were opened, they're concatenated. If none, returns the stdin parameter.
// This is useful for commands that want to treat multiple files as one stream.
//
// Example:
//
//	func (c command) Executor() gloo.CommandExecutor {
//	    return func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
//	        input := gloo.Inputs[gloo.File, flags](c).Reader(stdin)
//	        scanner := bufio.NewScanner(input)
//	        // ... process combined stream
//	    }
//	}
func (inputs Inputs[T, O]) Reader(stdin io.Reader) io.Reader {
	if len(inputs.stdin) == 0 {
		return stdin
	}

	readers := make([]io.Reader, len(inputs.stdin))
	for i, r := range inputs.stdin {
		readers[i] = r.reader
	}

	if len(readers) == 1 {
		return readers[0]
	}

	return io.MultiReader(readers...)
}

// Wrap wraps a CommandExecutor to automatically use the correct input source.
// This allows commands to use helper functions without worrying about stdin vs files.
// The framework automatically routes input based on how the command was initialized.
//
// Example:
//
//	func (c command) Executor() gloo.CommandExecutor {
//	    inputs := gloo.Inputs[gloo.File, flags](c)
//	    return inputs.Wrap(
//	        gloo.AccumulateAndProcess(func(lines []string) []string {
//	            return c.process(lines)
//	        }).Executor(),
//	    )
//	}
func (inputs Inputs[T, O]) Wrap(executor CommandExecutor) CommandExecutor {
	return func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
		// Automatically select the right input source:
		// - If files were opened (via Initialize), use those
		// - If readers were provided, use those
		// - Otherwise, use the stdin parameter
		input := inputs.Reader(stdin)
		return executor(ctx, input, stdout, stderr)
	}
}

// ============================================================================
// Channel-Based Methods
// ============================================================================

// WrapChannelString wraps a ChannelExecutor[string] to automatically convert from io streams.
// This allows channel-based commands to work with the standard io-based framework.
//
// Example:
//
//	func (c command) Executor() gloo.CommandExecutor {
//	    inputs := gloo.Inputs[gloo.File, flags](c)
//	    return inputs.WrapChannelString(
//	        gloo.ChannelLineTransform(func(line string) (string, bool) {
//	            return c.process(line), true
//	        }).ChannelExecutor(),
//	    )
//	}
func (inputs Inputs[T, O]) WrapChannelString(executor ChannelExecutor[string]) CommandExecutor {
	return inputs.Wrap(channelToIOAdapter(executor))
}

// WrapChannelBytes wraps a ChannelExecutor[[]byte] to automatically convert from io streams.
func (inputs Inputs[T, O]) WrapChannelBytes(executor ChannelExecutor[[]byte]) CommandExecutor {
	return inputs.Wrap(channelToIOAdapter(executor))
}

// ToChannelString converts the input readers to a channel of Row[string].
// This is useful for commands that want to use channels internally.
//
// Example:
//
//	func (c command) Executor() gloo.CommandExecutor {
//	    inputs := gloo.Inputs[gloo.File, flags](c)
//	    return func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
//	        ch := make(chan Row[string], 100)
//	        go inputs.ToChannelString(ctx, stdin, ch)
//	        for row := range ch {
//	            fmt.Fprintln(stdout, row.Data)
//	        }
//	        return nil
//	    }
//	}
func (inputs Inputs[T, O]) ToChannelString(ctx context.Context, stdin io.Reader, out chan<- Row[string]) error {
	input := inputs.Reader(stdin)
	return readerToChannel(ctx, input, out)
}

// ToChannelBytes converts the input readers to a channel of Row[[]byte].
func (inputs Inputs[T, O]) ToChannelBytes(ctx context.Context, stdin io.Reader, out chan<- Row[[]byte]) error {
	input := inputs.Reader(stdin)
	return byteReaderToChannel(ctx, input, out)
}

// Close closes all opened file handles. Call this when done with the inputs.
//
// Example:
//
//	inputs := gloo.Initialize[gloo.File, flags](params...)
//	defer inputs.Close()
func (inputs Inputs[T, O]) Close() error {
	for _, r := range inputs.stdin {
		if r.closer != nil {
			if err := r.closer.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Switch is the interface for flag types.
// All flag types should implement this interface to configure the flags struct.
//
// Example:
//
//	type IgnoreCase bool
//	const (
//	    CaseSensitive   IgnoreCase = false
//	    CaseInsensitive IgnoreCase = true
//	)
//
//	func (f IgnoreCase) Configure(flags *Flags) {
//	    flags.IgnoreCase = f
//	}
type Switch[T any] interface {
	Configure(*T)
}

// ============================================================================
// Internal Types
// ============================================================================

// writer holds output stream information (internal use only)
type writer struct {
	name   string
	writer io.Writer
	closer io.Closer
}

// reader holds input stream information (internal use only)
type reader struct {
	name     string
	position int
	reader   io.Reader
	closer   io.Closer
}
