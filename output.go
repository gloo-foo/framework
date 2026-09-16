package gloo

import (
	"io"
)

// Terminator is the byte appended after each written record — '\n' for line
// output, '\x00' for NUL-delimited output (the -0/-z convention).
type Terminator byte

// NewLine is the standard line terminator.
const NewLine Terminator = '\n'

// Line is the text of a single output record, written without its terminator.
type Line string

// Output writes data followed by a newline to stdout.
func Output(stdout io.Writer, data []byte) error {
	return OutputWithTerminator(stdout, NewLine, data)
}

// OutputWithTerminator writes data followed by a terminator byte to stdout.
func OutputWithTerminator(stdout io.Writer, terminator Terminator, data []byte) error {
	_, err := stdout.Write(data)
	if err != nil {
		return err
	}
	_, err = stdout.Write([]byte{byte(terminator)})
	return err
}

// OutputString writes a string line followed by a newline to stdout.
// Uses io.WriteString to avoid a string→[]byte copy when the writer
// implements io.StringWriter (e.g., os.File, bytes.Buffer).
func OutputString(stdout io.Writer, data Line) error {
	return OutputStringWithTerminator(stdout, NewLine, data)
}

// OutputStringWithTerminator writes a string followed by a terminator byte to stdout.
func OutputStringWithTerminator(stdout io.Writer, terminator Terminator, data Line) error {
	_, err := io.WriteString(stdout, string(data))
	if err != nil {
		return err
	}
	_, err = stdout.Write([]byte{byte(terminator)})
	return err
}
