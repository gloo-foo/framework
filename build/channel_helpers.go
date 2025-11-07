package build

import (
	"context"

	gloo "github.com/gloo-foo/framework"
)

// ============================================================================
// Channel-Based Helper Patterns
//
// These functions provide common patterns for building channel-based commands.
// They mirror the io-based helpers but use channels for row-oriented processing.
// ============================================================================

// ChannelLineTransformFunc is a simple row-by-row transformation for channels.
// Return (output, true) to emit the row, or (_, false) to skip it.
type ChannelLineTransformFunc[T any] func(data T) (output T, emit bool)

// ChannelStatefulLineTransformFunc is a row transformation with row number tracking.
// Return (output, true) to emit the row, or (_, false) to skip it.
type ChannelStatefulLineTransformFunc[T any] func(rowNum int64, data T) (output T, emit bool)

// ChannelAccumulateAndProcessFunc collects all rows, processes them, and returns the result.
type ChannelAccumulateAndProcessFunc[T any] func(rows []T) []T

// ChannelAccumulateAndOutputFunc collects all rows, processes them, and outputs directly to the channel.
type ChannelAccumulateAndOutputFunc[T any] func(rows []T, out chan<- gloo.Row[T]) error

// ============================================================================
// Channel Helper Functions
// ============================================================================

// ChannelLineTransform creates a ChannelCommand that transforms rows one at a time.
// Best for: grep, cut, tr, sed - stateless row transformations
//
// Example with strings (simulating grep):
//
//	func Grep(pattern string) ChannelCommand[string] {
//	    return ChannelLineTransform(func(line string) (string, bool) {
//	        if strings.Contains(line, pattern) {
//	            return line, true
//	        }
//	        return "", false
//	    })
//	}
//
// Example with custom types:
//
//	type Person struct {
//	    Name string
//	    Age  int
//	}
//
//	func FilterAdults() ChannelCommand[Person] {
//	    return ChannelLineTransform(func(p Person) (Person, bool) {
//	        return p, p.Age >= 18
//	    })
//	}
func ChannelLineTransform[T any](fn ChannelLineTransformFunc[T]) gloo.ChannelCommand[T] {
	return &channelLineTransformCommand[T]{fn: fn}
}

type channelLineTransformCommand[T any] struct {
	fn ChannelLineTransformFunc[T]
}

func (c *channelLineTransformCommand[T]) ChannelExecutor() gloo.ChannelExecutor[T] {
	return func(ctx context.Context, in <-chan gloo.Row[T], out chan<- gloo.Row[T]) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case row, ok := <-in:
				if !ok {
					return nil
				}
				if row.Err != nil {
					out <- row
					return row.Err
				}
				output, emit := c.fn(row.Data)
				if emit {
					out <- gloo.Row[T]{Data: output}
				}
			}
		}
	}
}

// ChannelStatefulLineTransform creates a ChannelCommand that transforms rows with state tracking.
// Best for: uniq, nl, head - need row numbers or previous row tracking
//
// Example with strings (simulating head):
//
//	func Head(n int) ChannelCommand[string] {
//	    return ChannelStatefulLineTransform(func(rowNum int64, line string) (string, bool) {
//	        return line, rowNum <= int64(n)
//	    })
//	}
//
// Example with custom types:
//
//	type Event struct {
//	    ID        int
//	    Timestamp time.Time
//	}
//
//	func AddSequence() ChannelCommand[Event] {
//	    return ChannelStatefulLineTransform(func(rowNum int64, e Event) (Event, bool) {
//	        e.ID = int(rowNum)
//	        return e, true
//	    })
//	}
func ChannelStatefulLineTransform[T any](fn ChannelStatefulLineTransformFunc[T]) gloo.ChannelCommand[T] {
	return &channelStatefulLineCommand[T]{fn: fn}
}

type channelStatefulLineCommand[T any] struct {
	fn ChannelStatefulLineTransformFunc[T]
}

func (c *channelStatefulLineCommand[T]) ChannelExecutor() gloo.ChannelExecutor[T] {
	return func(ctx context.Context, in <-chan gloo.Row[T], out chan<- gloo.Row[T]) error {
		rowNum := int64(0)
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case row, ok := <-in:
				if !ok {
					return nil
				}
				if row.Err != nil {
					out <- row
					return row.Err
				}
				rowNum++
				output, emit := c.fn(rowNum, row.Data)
				if emit {
					out <- gloo.Row[T]{Data: output}
				}
			}
		}
	}
}

// ChannelAccumulateAndProcess creates a ChannelCommand that collects all rows, processes them, and outputs.
// Best for: sort, tac, shuf - need to see all input before producing output
//
// Example with strings (simulating sort):
//
//	func Sort() ChannelCommand[string] {
//	    return ChannelAccumulateAndProcess(func(lines []string) []string {
//	        sort.Strings(lines)
//	        return lines
//	    })
//	}
//
// Example with custom types:
//
//	type Transaction struct {
//	    Amount float64
//	    Date   time.Time
//	}
//
//	func SortByAmount() ChannelCommand[Transaction] {
//	    return ChannelAccumulateAndProcess(func(txns []Transaction) []Transaction {
//	        sort.Slice(txns, func(i, j int) bool {
//	            return txns[i].Amount < txns[j].Amount
//	        })
//	        return txns
//	    })
//	}
func ChannelAccumulateAndProcess[T any](fn ChannelAccumulateAndProcessFunc[T]) gloo.ChannelCommand[T] {
	return &channelAccumulatorCommand[T]{fn: fn}
}

type channelAccumulatorCommand[T any] struct {
	fn ChannelAccumulateAndProcessFunc[T]
}

func (c *channelAccumulatorCommand[T]) ChannelExecutor() gloo.ChannelExecutor[T] {
	return func(ctx context.Context, in <-chan gloo.Row[T], out chan<- gloo.Row[T]) error {
		// Collect all rows
		var rows []T
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case row, ok := <-in:
				if !ok {
					// Process all rows and output
					result := c.fn(rows)
					for _, data := range result {
						select {
						case <-ctx.Done():
							return ctx.Err()
						case out <- gloo.Row[T]{Data: data}:
						}
					}
					return nil
				}
				if row.Err != nil {
					return row.Err
				}
				rows = append(rows, row.Data)
			}
		}
	}
}

// ChannelAccumulateAndOutput creates a ChannelCommand that collects all rows and outputs with full control.
// Best for: wc, stats - need to accumulate and produce custom output
//
// Example with strings (simulating wc):
//
//	func WordCount() ChannelCommand[string] {
//	    return ChannelAccumulateAndOutput(func(lines []string, out chan<- gloo.Row[string]) error {
//	        wordCount := 0
//	        for _, line := range lines {
//	            wordCount += len(strings.fields(line))
//	        }
//	        out <- Row[string]{Data: fmt.Sprintf("%d words", wordCount)}
//	        return nil
//	    })
//	}
//
// Example with custom types:
//
//	type Sale struct {
//	    Product string
//	    Amount  float64
//	}
//
//	type Summary struct {
//	    TotalSales float64
//	    count      int
//	}
//
//	func Summarize() ChannelCommand[Sale] {
//	    // Note: Input is Sale, but we want to output Summary
//	    // This requires a different pattern - see ChannelTransform below
//	}
func ChannelAccumulateAndOutput[T any](fn ChannelAccumulateAndOutputFunc[T]) gloo.ChannelCommand[T] {
	return &channelAccumulateOutputCommand[T]{fn: fn}
}

type channelAccumulateOutputCommand[T any] struct {
	fn ChannelAccumulateAndOutputFunc[T]
}

func (c *channelAccumulateOutputCommand[T]) ChannelExecutor() gloo.ChannelExecutor[T] {
	return func(ctx context.Context, in <-chan gloo.Row[T], out chan<- gloo.Row[T]) error {
		// Collect all rows
		var rows []T
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case row, ok := <-in:
				if !ok {
					// Process and output with custom logic
					return c.fn(rows, out)
				}
				if row.Err != nil {
					return row.Err
				}
				rows = append(rows, row.Data)
			}
		}
	}
}

// ============================================================================
// Advanced Channel Patterns
// ============================================================================

// ChannelTransform allows transforming from one type to another in a pipeline.
// This is useful when you need to change the data type between pipeline stages.
//
// Example: Parse log lines into structured data
//
//	type LogEntry struct {
//	    Level     string
//	    Timestamp time.Time
//	    Message   string
//	}
//
//	func ParseLogs() ChannelExecutor[string] {
//	    return ChannelTransform[string, LogEntry](
//	        func(line string) (LogEntry, bool, error) {
//	            // Parse line into LogEntry
//	            parts := strings.SplitN(line, " ", 3)
//	            if len(parts) != 3 {
//	                return LogEntry{}, false, nil
//	            }
//	            return LogEntry{
//	                Level:   parts[0],
//	                Message: parts[2],
//	            }, true, nil
//	        },
//	    )
//	}
func ChannelTransform[TIn, TOut any](fn func(TIn) (TOut, bool, error)) func(context.Context, <-chan gloo.Row[TIn], chan<- gloo.Row[TOut]) error {
	return func(ctx context.Context, in <-chan gloo.Row[TIn], out chan<- gloo.Row[TOut]) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case row, ok := <-in:
				if !ok {
					return nil
				}
				if row.Err != nil {
					out <- gloo.Row[TOut]{Err: row.Err}
					return row.Err
				}
				output, emit, err := fn(row.Data)
				if err != nil {
					out <- gloo.Row[TOut]{Err: err}
					return err
				}
				if emit {
					out <- gloo.Row[TOut]{Data: output}
				}
			}
		}
	}
}

// ChannelFanOut duplicates each input row to multiple output channels.
// Useful for broadcasting data to multiple consumers.
//
// ⚠️  THREAD SAFETY WARNING:
// This function sends the SAME Row[T] reference to ALL output channels.
// If T contains mutable data (pointers, slices, maps), all consumers
// will share that mutable state, which can cause DATA RACES.
//
// SAFE to use with:
//   - Immutable types (string, int, bool, etc.)
//   - Read-only structs with no pointers
//
// UNSAFE to use with:
//   - Structs containing slices, maps, or pointers
//   - Any mutable data that consumers might modify
//
// For mutable types, implement a custom fan-out that copies data.
// See THREADING.md for examples.
//
// Example (safe with strings):
//
//	func Tee(outputs ...chan<- gloo.Row[string]) ChannelExecutor[string] {
//	    return ChannelFanOut(outputs...)  // OK: strings are immutable
//	}
func ChannelFanOut[T any](outputs ...chan<- gloo.Row[T]) gloo.ChannelExecutor[T] {
	return func(ctx context.Context, in <-chan gloo.Row[T], out chan<- gloo.Row[T]) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case row, ok := <-in:
				if !ok {
					return nil
				}
				// Send to primary output
				out <- row
				// Send to additional outputs
				for _, output := range outputs {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case output <- row:
					}
				}
			}
		}
	}
}

// ChannelMerge combines multiple input channels into one output channel.
// Useful for combining data from multiple sources.
//
// Example:
//
//	func MergeFiles(readers ...io.Reader) ChannelExecutor[string] {
//	    return func(ctx context.Context, _ <-chan gloo.Row[string], out chan<- gloo.Row[string]) error {
//	        inputs := make([]<-chan gloo.Row[string], len(readers))
//	        for i, r := range readers {
//	            ch := make(chan Row[string], 100)
//	            inputs[i] = ch
//	            go ReaderToChannel(ctx, r, ch)
//	        }
//	        return ChannelMerge(inputs...)(ctx, nil, out)
//	    }
//	}
func ChannelMerge[T any](inputs ...<-chan gloo.Row[T]) gloo.ChannelExecutor[T] {
	return func(ctx context.Context, _ <-chan gloo.Row[T], out chan<- gloo.Row[T]) error {
		// Use a single goroutine per input to forward to output
		done := make(chan struct{})
		for _, input := range inputs {
			go func(in <-chan gloo.Row[T]) {
				for {
					select {
					case <-ctx.Done():
						return
					case <-done:
						return
					case row, ok := <-in:
						if !ok {
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-done:
							return
						case out <- row:
						}
					}
				}
			}(input)
		}
		<-ctx.Done()
		close(done)
		return ctx.Err()
	}
}

// ChannelBuffer creates a buffered channel pipeline stage.
// Useful for decoupling slow producers from slow consumers.
//
// Example:
//
//	func WithBuffer(size int, exec ChannelExecutor[string]) ChannelExecutor[string] {
//	    return func(ctx context.Context, in <-chan gloo.Row[string], out chan<- gloo.Row[string]) error {
//	        buffered := make(chan Row[string], size)
//	        go func() {
//	            exec(ctx, in, buffered)
//	            close(buffered)
//	        }()
//	        for row := range buffered {
//	            out <- row
//	        }
//	        return nil
//	    }
//	}
func ChannelBuffer[T any](size int) gloo.ChannelExecutor[T] {
	return func(ctx context.Context, in <-chan gloo.Row[T], out chan<- gloo.Row[T]) error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case row, ok := <-in:
				if !ok {
					return nil
				}
				out <- row
			}
		}
	}
}
