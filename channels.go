package gloo

import (
	"bufio"
	"context"
	"encoding"
	"fmt"
	"io"

	"github.com/destel/rill"
)

// ============================================================================
// Channel-Based Data Flow Types
//
// These types enable commands to communicate via channels instead of io streams,
// supporting both line-oriented string processing (like Unix pipes) and
// complex user-defined types.
// ============================================================================

// Row represents a single unit of data flowing through a channel.
// It can contain strings (for line-oriented processing), []byte, or any user-defined type.
//
// This is an alias for rill.Try[T] to maintain API compatibility while using rill internally.
//
// typ Parameters:
//   - T: The type of data being passed (string, []byte, or custom types)
//
// ⚠️  THREAD SAFETY:
// Row[T] is passed by value through channels, but if T contains pointers, slices,
// or maps, multiple goroutines may share references to the same underlying data.
//
//   - In single-consumer pipelines: Safe to mutate Value (you own it)
//   - With fan-out or parallel processing: Treat Value as READ-ONLY or copy explicitly
//   - Commands should be immutable after construction
//
// See THREADING.md for detailed threading model and best practices.
//
// Example with strings (always safe - strings are immutable):
//
//	type command struct { pattern string }
//	func (c command) ChannelExecutor() ChannelExecutor[string] {
//	    return func(ctx context.Context, in <-chan rill.Try[string], out chan<- rill.Try[string]) error {
//	        for row := range in {
//	            if strings.Contains(row.Value, c.pattern) {
//	                out <- row  // Safe: strings immutable
//	            }
//	        }
//	        return nil
//	    }
//	}
//
// Example with custom types:
//
//	type LogEntry struct {
//	    Timestamp time.Time
//	    Level     string
//	    Message   string
//	}
//
//	func FilterByLevel(level string) ChannelExecutor[LogEntry] {
//	    return func(ctx context.Context, in <-chan rill.Try[LogEntry], out chan<- rill.Try[LogEntry]) error {
//	        for row := range in {
//	            if row.Value.Level == level {
//	                out <- row  // Safe in linear pipeline
//	            }
//	        }
//	        return nil
//	    }
//	}
type Row[T any] = rill.Try[T]

// ChannelExecutor is the function signature for channel-based command execution.
// It receives context, input channel, and output channel.
//
// typ Parameters:
//   - T: The type of data flowing through the channels
//
// The executor should:
//   - Read from the input channel until it's closed
//   - Process each Row and send results to the output channel
//   - Return an error if processing fails
//   - NOT close the output channel (the framework handles that)
//
// Note: This uses rill.Try[T] (aliased as Row[T]) for error handling.
type ChannelExecutor[T any] func(ctx context.Context, in <-chan rill.Try[T], out chan<- rill.Try[T]) error

// ChannelCommand represents a channel-based executable command.
// This is the channel equivalent of the Command interface.
//
// typ Parameters:
//   - T: The type of data flowing through the channels
type ChannelCommand[T any] interface {
	ChannelExecutor() ChannelExecutor[T]
}

// ============================================================================
// Adapters: Bridging io.Reader/Writer and Channels
// ============================================================================

// RowParser is an interface for types that can parse themselves from a text line.
// Custom types can implement this to enable automatic conversion from io.Reader.
//
// Example:
//
//	type LogEntry struct {
//	    Level   string
//	    Message string
//	}
//
//	func (e *LogEntry) ParseRow(line string) error {
//	    parts := strings.SplitN(line, " ", 2)
//	    if len(parts) != 2 {
//	        return fmt.Errorf("invalid format")
//	    }
//	    e.Level = parts[0]
//	    e.Message = parts[1]
//	    return nil
//	}
//
// Now LogEntry can be used with ReaderToChannelParsed:
//
//	ch := make(chan Row[LogEntry], 100)
//	go ReaderToChannelParsed(ctx, reader, ch)
type RowParser interface {
	ParseRow(line string) error
}

// ReaderToChannelParsed converts an io.Reader to a channel of Row[T] for custom types
// that implement RowParser or encoding.TextUnmarshaler.
//
// This enables automatic parsing of custom types from text input.
//
// Example:
//
//	type LogEntry struct {
//	    Level   string
//	    Message string
//	}
//
//	func (e *LogEntry) ParseRow(line string) error {
//	    parts := strings.SplitN(line, " ", 2)
//	    if len(parts) != 2 {
//	        return fmt.Errorf("invalid log format")
//	    }
//	    e.Level = parts[0]
//	    e.Message = parts[1]
//	    return nil
//	}
//
//	ch := make(chan Row[LogEntry], 100)
//	go ReaderToChannelParsed(ctx, reader, ch)
//	for row := range ch {
//	    if row.Error != nil {
//	        log.Printf("Parse error: %v", row.Error)
//	        continue
//	    }
//	    fmt.Printf("Level: %s, Message: %s\n", row.Value.Level, row.Value.Message)
//	}
func ReaderToChannelParsed[T any](ctx context.Context, r io.Reader, out chan<- rill.Try[T]) error {
	defer close(out)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()

		// Create a new instance of T
		var data T
		var err error

		// Try RowParser interface first
		if parser, ok := any(&data).(RowParser); ok {
			err = parser.ParseRow(line)
		} else if unmarshaler, ok := any(&data).(encoding.TextUnmarshaler); ok {
			// Fall back to encoding.TextUnmarshaler
			err = unmarshaler.UnmarshalText([]byte(line))
		} else {
			// No parser available
			err = fmt.Errorf("type %T does not implement RowParser or encoding.TextUnmarshaler", data)
		}

		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case out <- rill.Try[T]{Error: fmt.Errorf("parse error: %w", err)}:
				// Continue processing even on parse errors
			}
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- rill.Try[T]{Value: data}:
		}
	}

	if err := scanner.Err(); err != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- rill.Try[T]{Error: err}:
		}
		return err
	}
	return nil
}

// ReaderToChannelWithParser converts an io.Reader to a channel of Row[T] using a custom parser function.
// This is useful when you want to provide a parsing function without implementing an interface.
//
// Example:
//
//	type LogEntry struct {
//	    Level   string
//	    Message string
//	}
//
//	parser := func(line string) (LogEntry, error) {
//	    parts := strings.SplitN(line, " ", 2)
//	    if len(parts) != 2 {
//	        return LogEntry{}, fmt.Errorf("invalid format")
//	    }
//	    return LogEntry{Level: parts[0], Message: parts[1]}, nil
//	}
//
//	ch := make(chan Row[LogEntry], 100)
//	go ReaderToChannelWithParser(ctx, reader, ch, parser)
func ReaderToChannelWithParser[T any](ctx context.Context, r io.Reader, out chan<- rill.Try[T], parse func(string) (T, error)) error {
	defer close(out)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		data, err := parse(line)

		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case out <- rill.Try[T]{Error: fmt.Errorf("parse error: %w", err)}:
				// Continue processing even on parse errors
			}
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- rill.Try[T]{Value: data}:
		}
	}

	if err := scanner.Err(); err != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- rill.Try[T]{Error: err}:
		}
		return err
	}
	return nil
}

// readerToChannel converts an io.Reader to a channel of Row[string] (line-oriented).
// Each line from the reader becomes a Row[string] in the channel.
// The channel is closed when the reader is exhausted or an error occurs.
func readerToChannel(ctx context.Context, r io.Reader, out chan<- rill.Try[string]) error {
	defer close(out)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- rill.Try[string]{Value: scanner.Text()}:
		}
	}
	if err := scanner.Err(); err != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- rill.Try[string]{Error: err}:
		}
		return err
	}
	return nil
}

// byteReaderToChannel converts an io.Reader to a channel of Row[[]byte] (line-oriented).
// Each line from the reader becomes a Row[[]byte] in the channel.
// The channel is closed when the reader is exhausted or an error occurs.
func byteReaderToChannel(ctx context.Context, r io.Reader, out chan<- rill.Try[[]byte]) error {
	defer close(out)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		// Make a copy of the bytes since scanner reuses the buffer
		data := make([]byte, len(scanner.Bytes()))
		copy(data, scanner.Bytes())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- rill.Try[[]byte]{Value: data}:
		}
	}
	if err := scanner.Err(); err != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- rill.Try[[]byte]{Error: err}:
		}
		return err
	}
	return nil
}

// channelToWriter converts a channel of Row[string] to an io.Writer (line-oriented).
// Each Row[string] is written as a line to the writer.
func channelToWriter(ctx context.Context, in <-chan rill.Try[string], w io.Writer) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case row, ok := <-in:
			if !ok {
				return nil
			}
			if row.Error != nil {
				return row.Error
			}
			if _, err := fmt.Fprintln(w, row.Value); err != nil {
				return err
			}
		}
	}
}

// byteChannelToWriter converts a channel of Row[[]byte] to an io.Writer (line-oriented).
// Each Row[[]byte] is written as a line to the writer.
func byteChannelToWriter(ctx context.Context, in <-chan rill.Try[[]byte], w io.Writer) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case row, ok := <-in:
			if !ok {
				return nil
			}
			if row.Error != nil {
				return row.Error
			}
			if _, err := w.Write(row.Value); err != nil {
				return err
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				return err
			}
		}
	}
}

// ============================================================================
// Channel Bridge: Connecting Channel Commands to io-based Commands
// ============================================================================

// channelToIOAdapter wraps a ChannelExecutor to work with the standard CommandExecutor interface.
// This allows channel-based commands to be used with the existing io-based framework.
// Used internally by WrapChannelString and WrapChannelBytes.
func channelToIOAdapter[T any](chExec ChannelExecutor[T]) CommandExecutor {
	return func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
		// Create channels
		in := make(chan rill.Try[T], 100)
		out := make(chan rill.Try[T], 100)

		// Start reader goroutine
		readerDone := make(chan error, 1)
		go func() {
			defer close(in)
			var readerErr error
			// Use type-specific reader based on T
			var zeroT T
			switch any(zeroT).(type) {
			case string:
				// For string type, convert stdin to channel
				scanner := bufio.NewScanner(stdin)
				for scanner.Scan() {
					select {
					case <-ctx.Done():
						readerDone <- ctx.Err()
						return
					case in <- any(rill.Try[string]{Value: scanner.Text()}).(rill.Try[T]):
					}
				}
				readerErr = scanner.Err()
			case []byte:
				// For []byte type, convert stdin to channel
				scanner := bufio.NewScanner(stdin)
				for scanner.Scan() {
					data := make([]byte, len(scanner.Bytes()))
					copy(data, scanner.Bytes())
					select {
					case <-ctx.Done():
						readerDone <- ctx.Err()
						return
					case in <- any(rill.Try[[]byte]{Value: data}).(rill.Try[T]):
					}
				}
				readerErr = scanner.Err()
			default:
				// For custom types, try RowParser or encoding.TextUnmarshaler
				scanner := bufio.NewScanner(stdin)
				for scanner.Scan() {
					line := scanner.Text()
					var data T
					var parseErr error

					// Try RowParser interface
					if parser, ok := any(&data).(RowParser); ok {
						parseErr = parser.ParseRow(line)
					} else if unmarshaler, ok := any(&data).(encoding.TextUnmarshaler); ok {
						// Try encoding.TextUnmarshaler
						parseErr = unmarshaler.UnmarshalText([]byte(line))
					} else {
						// No parser available - close channel
						readerErr = fmt.Errorf("type %T does not implement RowParser or encoding.TextUnmarshaler", data)
						break
					}

					if parseErr != nil {
						select {
						case <-ctx.Done():
							readerDone <- ctx.Err()
							return
						case in <- rill.Try[T]{Error: parseErr}:
						}
						continue
					}

					select {
					case <-ctx.Done():
						readerDone <- ctx.Err()
						return
					case in <- rill.Try[T]{Value: data}:
					}
				}
				if readerErr == nil {
					readerErr = scanner.Err()
				}
			}
			if readerErr != nil {
				_, _ = fmt.Fprintf(stderr, "Error reading input: %v\n", readerErr)
			}
			readerDone <- readerErr
		}()

		// Start writer goroutine
		writerDone := make(chan error, 1)
		go func() {
			var writerErr error
			var zeroT T
			switch any(zeroT).(type) {
			case string:
				// For string type, write channel to stdout
				for row := range out {
					if row.Error != nil {
						writerErr = row.Error
						break
					}
					strRow := any(row).(rill.Try[string])
					if _, err := fmt.Fprintln(stdout, strRow.Value); err != nil {
						writerErr = err
						break
					}
				}
			case []byte:
				// For []byte type, write channel to stdout
				for row := range out {
					if row.Error != nil {
						writerErr = row.Error
						break
					}
					byteRow := any(row).(rill.Try[[]byte])
					if _, err := stdout.Write(byteRow.Value); err != nil {
						writerErr = err
						break
					}
					if _, err := stdout.Write([]byte("\n")); err != nil {
						writerErr = err
						break
					}
				}
			default:
				// For custom types, just drain the channel
				for range out {
				}
			}
			writerDone <- writerErr
		}()

		// Execute the channel command
		execErr := chExec(ctx, in, out)
		close(out)

		// Wait for goroutines to finish
		<-readerDone
		writerErr := <-writerDone

		// Return first error encountered
		if execErr != nil {
			return execErr
		}
		return writerErr
	}
}

// ioToChannelAdapter wraps a CommandExecutor to work with channel-based commands.
// This allows existing io-based commands to be used in channel pipelines.
// Used internally when bridging IO and channel-based commands.
func ioToChannelAdapter[T any](exec CommandExecutor) ChannelExecutor[T] {
	return func(ctx context.Context, in <-chan rill.Try[T], out chan<- rill.Try[T]) error {
		// Create pipes
		pr, pw := io.Pipe()
		defer pr.Close()

		// Start goroutine to convert channel to pipe
		go func() {
			defer pw.Close()
			var zeroT T
			switch any(zeroT).(type) {
			case string:
				for row := range in {
					if row.Error != nil {
						pw.CloseWithError(row.Error)
						return
					}
					strRow := any(row).(rill.Try[string])
					if _, err := fmt.Fprintln(pw, strRow.Value); err != nil {
						pw.CloseWithError(err)
						return
					}
				}
			case []byte:
				for row := range in {
					if row.Error != nil {
						pw.CloseWithError(row.Error)
						return
					}
					byteRow := any(row).(rill.Try[[]byte])
					if _, err := pw.Write(byteRow.Value); err != nil {
						pw.CloseWithError(err)
						return
					}
					if _, err := pw.Write([]byte("\n")); err != nil {
						pw.CloseWithError(err)
						return
					}
				}
			default:
				// For custom types, close immediately
				pw.Close()
			}
		}()

		// Create pipe for output
		outPr, outPw := io.Pipe()
		defer outPw.Close()

		// Start goroutine to convert pipe to channel
		go func() {
			defer close(out)
			var zeroT T
			switch any(zeroT).(type) {
			case string:
				scanner := bufio.NewScanner(outPr)
				for scanner.Scan() {
					select {
					case <-ctx.Done():
						return
					case out <- any(rill.Try[string]{Value: scanner.Text()}).(rill.Try[T]):
					}
				}
				if err := scanner.Err(); err != nil {
					select {
					case <-ctx.Done():
					case out <- rill.Try[T]{Error: err}:
					}
				}
			case []byte:
				scanner := bufio.NewScanner(outPr)
				for scanner.Scan() {
					data := make([]byte, len(scanner.Bytes()))
					copy(data, scanner.Bytes())
					select {
					case <-ctx.Done():
						return
					case out <- any(rill.Try[[]byte]{Value: data}).(rill.Try[T]):
					}
				}
				if err := scanner.Err(); err != nil {
					select {
					case <-ctx.Done():
					case out <- rill.Try[T]{Error: err}:
					}
				}
			}
		}()

		// Execute the io-based command
		return exec(ctx, pr, outPw, io.Discard)
	}
}
