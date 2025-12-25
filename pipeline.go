package gloo

import (
	"context"
	"os"

	"github.com/destel/rill"
)

// ============================================================================
// Channel Pipeline Support
//
// These functions enable composing multiple channel-based commands into
// pipelines, similar to Unix shell pipes but with full type safety.
// ============================================================================

// Pipe connects multiple channel executors into a pipeline.
// The output of each executor becomes the input of the next.
//
// Example:
//
//	grep := ChannelLineTransform(func(line string) (string, bool) {
//	    return line, strings.Contains(line, "ERROR")
//	})
//
//	sort := ChannelAccumulateAndProcess(func(lines []string) []string {
//	    sort.Strings(lines)
//	    return lines
//	})
//
//	pipeline := Pipe(grep.ChannelExecutor(), sort.ChannelExecutor())
func Pipe[T any](first ChannelExecutor[T], rest ...ChannelExecutor[T]) ChannelExecutor[T] {
	executors := append([]ChannelExecutor[T]{first}, rest...)

	if len(executors) == 1 {
		return executors[0]
	}

	if len(executors) == 2 {
		// Optimized case for two executors
		return func(ctx context.Context, in <-chan rill.Try[T], out chan<- rill.Try[T]) error {
			// Create intermediate channel
			intermediate := make(chan rill.Try[T], 100)

			// Start first executor
			firstDone := make(chan error, 1)
			go func() {
				err := executors[0](ctx, in, intermediate)
				close(intermediate)
				firstDone <- err
			}()

			// Run second executor
			secondErr := executors[1](ctx, intermediate, out)

			// Wait for first to complete
			firstErr := <-firstDone

			// Return first error encountered
			if firstErr != nil {
				return firstErr
			}
			return secondErr
		}
	}

	// For more than 2, chain them recursively
	return Pipe(executors[0], Pipe(executors[1], executors[2:]...))
}

// ============================================================================
// Pipeline Command
// ============================================================================

// Pipeline creates a Command from a channel executor pipeline.
// This allows you to build complex pipelines using channels and run them with the standard Run function.
//
// Example:
//
//	pipeline := gloo.Pipeline(
//	    gloo.ChannelLineTransform(func(line string) (string, bool) {
//	        return strings.ToUpper(line), true
//	    }),
//	)
//	gloo.Run(pipeline)
func Pipeline[T any](executor ChannelExecutor[T]) Command {
	return &pipelineCommand[T]{executor: executor}
}

type pipelineCommand[T any] struct {
	executor ChannelExecutor[T]
}

func (p *pipelineCommand[T]) Executor() CommandExecutor {
	return channelToIOAdapter(p.executor)
}

// ============================================================================
// Run Variants for Channel Commands
// ============================================================================

// RunChannel executes a ChannelCommand with the standard os.Stdin, os.Stdout, and os.Stderr streams.
// This is the primary way to run channel-based commands.
//
// Example:
//
//	cmd := ChannelLineTransform(func(line string) (string, bool) {
//	    return strings.ToUpper(line), true
//	})
//	if err := gloo.RunChannel(cmd); err != nil {
//	    log.Fatal(err)
//	}
func RunChannel[T any](cmd ChannelCommand[T]) error {
	return RunChannelWithContext(context.Background(), cmd)
}

// RunChannelWithContext executes a ChannelCommand with a custom context.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	if err := gloo.RunChannelWithContext(ctx, cmd); err != nil {
//	    log.Fatal(err)
//	}
func RunChannelWithContext[T any](ctx context.Context, cmd ChannelCommand[T]) error {
	executor := cmd.ChannelExecutor()
	ioExecutor := channelToIOAdapter(executor)
	return ioExecutor(ctx, os.Stdin, os.Stdout, os.Stderr)
}

// MustRunChannel runs a channel command and panics if it returns an error.
func MustRunChannel[T any](cmd ChannelCommand[T]) {
	Must(RunChannel(cmd))
}

// ============================================================================
// Parallel Processing
// ============================================================================

// ParallelMap applies a function to each row in parallel using multiple goroutines.
// This is useful for CPU-intensive operations that can benefit from parallelism.
//
// ⚠️  THREAD SAFETY WARNING:
// Your function receives the input data directly. DO NOT mutate the input.
// Always return a NEW value instead of modifying the input.
//
// SAFE:
//
//	result := process(input)  // Read input, create new output
//	return result, true, nil
//
// UNSAFE:
//
//	input.Field = "modified"  // Mutating input - DON'T DO THIS!
//	return input, true, nil
//
// Treat the input parameter as READ-ONLY. Any mutation may cause
// undefined behavior or data races.
//
// Example:
//
//	func ExpensiveOperation(line string) string {
//	    // Some CPU-intensive work on line
//	    return result  // Return NEW string
//	}
//
//	parallelTransform := ParallelMap(4, func(line string) (string, bool, error) {
//	    return ExpensiveOperation(line), true, nil  // OK: strings immutable
//	})
func ParallelMap[T any](workers int, fn func(T) (T, bool, error)) ChannelExecutor[T] {
	return func(ctx context.Context, in <-chan rill.Try[T], out chan<- rill.Try[T]) error {
		// Create work queue
		work := make(chan rill.Try[T], workers*2)
		results := make(chan rill.Try[T], workers*2)

		// Start workers
		workerDone := make(chan struct{})
		for i := 0; i < workers; i++ {
			go func() {
				for row := range work {
					if row.Error != nil {
						results <- row
						continue
					}
					output, emit, err := fn(row.Value)
					if err != nil {
						results <- rill.Try[T]{Error: err}
						continue
					}
					if emit {
						results <- rill.Try[T]{Value: output}
					}
				}
				workerDone <- struct{}{}
			}()
		}

		// Start result collector
		collectorDone := make(chan error, 1)
		go func() {
			for row := range results {
				select {
				case <-ctx.Done():
					collectorDone <- ctx.Err()
					return
				case out <- row:
					if row.Error != nil {
						collectorDone <- row.Error
						return
					}
				}
			}
			collectorDone <- nil
		}()

		// Feed work to workers
		feedErr := func() error {
			defer close(work)
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case row, ok := <-in:
					if !ok {
						return nil
					}
					work <- row
				}
			}
		}()

		// Wait for workers to finish
		for i := 0; i < workers; i++ {
			<-workerDone
		}
		close(results)

		// Wait for collector to finish
		collectorErr := <-collectorDone

		// Return first error encountered
		if feedErr != nil {
			return feedErr
		}
		return collectorErr
	}
}

// ============================================================================
// Batch Processing
// ============================================================================

// Batch groups rows into batches of a specified size and processes them together.
// This is useful for operations that are more efficient when processing multiple items at once.
//
// Example:
//
//	batchInsert := Batch[string](100, func(batch []string) ([]string, error) {
//	    // Insert batch into database
//	    db.InsertMany(batch)
//	    return batch, nil
//	})
func Batch[T any](size int, fn func([]T) ([]T, error)) ChannelExecutor[T] {
	return func(ctx context.Context, in <-chan rill.Try[T], out chan<- rill.Try[T]) error {
		batch := make([]T, 0, size)

		flushBatch := func() error {
			if len(batch) == 0 {
				return nil
			}
			results, err := fn(batch)
			if err != nil {
				return err
			}
			for _, result := range results {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case out <- rill.Try[T]{Value: result}:
				}
			}
			batch = batch[:0]
			return nil
		}

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case row, ok := <-in:
				if !ok {
					return flushBatch()
				}
				if row.Error != nil {
					if err := flushBatch(); err != nil {
						return err
					}
					out <- row
					return row.Error
				}
				batch = append(batch, row.Value)
				if len(batch) >= size {
					if err := flushBatch(); err != nil {
						return err
					}
				}
			}
		}
	}
}

// ============================================================================
// Rill-Based Helpers
//
// These functions provide direct wrappers around rill's built-in functions
// for better performance and concurrency control.
// ============================================================================

// RillMap wraps rill.Map to provide a ChannelExecutor that uses rill's concurrent map implementation.
// This is more efficient than custom implementations for parallel transformations.
//
// Example:
//
//	transform := RillMap(4, func(line string) (string, error) {
//	   return strings.ToUpper(line), nil
//	})
//
// pipeline := gloo.Pipeline(transform)
// gloo.Run(pipeline)
func RillMap[A, B any](n int, f func(A) (B, error)) func(context.Context, <-chan rill.Try[A], chan<- rill.Try[B]) error {
	return func(ctx context.Context, in <-chan rill.Try[A], out chan<- rill.Try[B]) error {
		// Use rill.Map to transform the stream
		mapped := rill.Map(in, n, f)

		// Forward all items to output channel
		for item := range mapped {
			select {
			case <-ctx.Done():
				rill.Discard(mapped)
				return ctx.Err()
			case out <- item:
				if item.Error != nil {
					rill.Discard(mapped)
					return item.Error
				}
			}
		}
		return nil
	}
}

// RillFilter wraps rill.Filter to provide a ChannelExecutor that uses rill's concurrent filter implementation.
//
// Example:
//
//	filter := RillFilter(2, func(line string) (bool, error) {
//	   return strings.Contains(line, "ERROR"), nil
//	})
//
// pipeline := gloo.Pipeline(filter)
// gloo.Run(pipeline)
func RillFilter[T any](n int, f func(T) (bool, error)) ChannelExecutor[T] {
	return func(ctx context.Context, in <-chan rill.Try[T], out chan<- rill.Try[T]) error {
		// Use rill.Filter to filter the stream
		filtered := rill.Filter(in, n, f)

		// Forward all items to output channel
		for item := range filtered {
			select {
			case <-ctx.Done():
				rill.Discard(filtered)
				return ctx.Err()
			case out <- item:
				if item.Error != nil {
					rill.Discard(filtered)
					return item.Error
				}
			}
		}
		return nil
	}
}

// RillOrderedMap wraps rill.OrderedMap for ordered concurrent transformations.
// This preserves the order of items from input to output, which is useful for
// maintaining line order in text processing.
//
// Example:
//
//	transform := RillOrderedMap(4, func(line string) (string, error) {
//	   return processLine(line), nil
//	})
func RillOrderedMap[A, B any](n int, f func(A) (B, error)) func(context.Context, <-chan rill.Try[A], chan<- rill.Try[B]) error {
	return func(ctx context.Context, in <-chan rill.Try[A], out chan<- rill.Try[B]) error {
		// Use rill.OrderedMap to transform the stream while preserving order
		mapped := rill.OrderedMap(in, n, f)

		// Forward all items to output channel
		for item := range mapped {
			select {
			case <-ctx.Done():
				rill.Discard(mapped)
				return ctx.Err()
			case out <- item:
				if item.Error != nil {
					rill.Discard(mapped)
					return item.Error
				}
			}
		}
		return nil
	}
}
