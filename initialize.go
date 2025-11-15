package gloo

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
)

// ============================================================================
// Parameter Parsing and Initialization for Command Developers
// ============================================================================

// Initialize parses parameters into Inputs and automatically handles file opening
// based on the positional type T:
//   - gloo.File: Automatically opens files for reading (or uses stdin if no files)
//   - io.Reader: Wraps readers for stdin
//   - Other types: Just parses (commands define their own types as needed)
//
// Example:
//
//	func Cat(parameters ...any) gloo.Command {
//	    inputs := gloo.Initialize[gloo.File, Flags](parameters...)
//	    return command(inputs)
//	}
//
// Custom types:
//
//	type DirPath string  // Command-defined type
//	inputs := gloo.Initialize[DirPath, Flags](parameters...)
func Initialize[T any, O any](parameters ...any) Inputs[T, O] {
	inputs := args[T, O](parameters...)

	// Check if T is File - if so, automatically open files
	var zeroT T
	switch any(zeroT).(type) {
	case File:
		// T is File, so we can call openAsFiles
		// We need to convert Inputs[T,O] to Inputs[File,O], open files, then convert back
		return openAsFilesGeneric(inputs)
	case io.Reader:
		// T is io.Reader, wrap the readers
		return wrapReadersGeneric(inputs)
	default:
		// For custom command-defined types - just return as-is
		return inputs
	}
}

// ============================================================================
// Internal Implementation
// ============================================================================

// args parses parameters into Inputs without any special handling
func args[T any, O any](parameters ...any) (result Inputs[T, O]) {
	var (
		options []Switch[O]
	)

	// Check if T is File - we'll accept strings and io.Reader as substitutes
	// Use reflection on pointer to get the actual type since zero values can be tricky
	var ptrT *T
	tType := reflect.TypeOf(ptrT).Elem()
	fileType := reflect.TypeOf(File(""))
	positionalIsFile := tType == fileType

	for _, arg := range parameters {
		switch v := arg.(type) {
		case io.Reader:
			// If T is File, accept io.Reader as a substitute
			if positionalIsFile {
				result.stdin = append(result.stdin, reader{
					position: len(result.stdin),
					reader:   v,
					closer:   nil, // Caller owns lifecycle
				})
			} else {
				slog.Warn("io.Reader not supported for this command type")
				result.Ambiguous = append(result.Ambiguous, v)
			}
		case T:
			result.Positional = append(result.Positional, v)
		case string:
			// If T is File, automatically convert string to File
			if positionalIsFile {
				result.Positional = append(result.Positional, any(File(v)).(T))
			} else {
				slog.Warn("string not supported for this command type (use custom type or File)")
				result.Ambiguous = append(result.Ambiguous, v)
			}
		case Switch[O]:
			options = append(options, v)
		default:
			slog.Warn("Unknown argument type", "arg", v, "type", fmt.Sprintf("%T/%T", arg, v))
			result.Ambiguous = append(result.Ambiguous, v)
		}
	}
	result.Flags = configure(options...)
	return result
}

// configure applies all switches to create a configured flags struct
func configure[T any](opts ...Switch[T]) T {
	def := new(T)
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.Configure(def)
	}
	return *def
}

// openAsFilesGeneric handles file opening for File type positionals.
// This is called automatically by Initialize when T is File.
func openAsFilesGeneric[T any, O any](inputs Inputs[T, O]) Inputs[T, O] {
	if len(inputs.Positional) == 0 {
		// No files provided - will read from stdin parameter
		return inputs
	}

	var (
		stdinSeen bool
		input     io.Reader
		closer    io.Closer
	)

	for position, positional := range inputs.Positional {
		// Convert positional to string (we know it's File which is string-based)
		filename := any(positional).(File)
		filenameStr := string(filename)

		if filenameStr == "-" {
			if stdinSeen {
				slog.Warn("multiple stdin sources")
				continue
			}
			stdinSeen = true
			input = os.Stdin
		} else {
			fd, err := os.Open(filenameStr)
			if err != nil {
				slog.Debug("Could not open file as input", "filename", filenameStr, "err", err)
				continue
			}
			input, closer = fd, fd
		}

		inputs.stdin = append(inputs.stdin, reader{
			name:     filenameStr,
			position: position,
			reader:   input,
			closer:   closer,
		})
	}

	return inputs
}

// wrapReadersGeneric handles io.Reader positional wrapping.
// This is called automatically by Initialize when T is io.Reader.
func wrapReadersGeneric[T any, O any](inputs Inputs[T, O]) Inputs[T, O] {
	for i, positional := range inputs.Positional {
		// Convert positional to io.Reader
		r := any(positional).(io.Reader)
		inputs.stdin = append(inputs.stdin, reader{
			position: i,
			reader:   r,
			closer:   nil, // Caller owns lifecycle
		})
	}
	return inputs
}
