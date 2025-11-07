package build

import (
	"bufio"
	"context"
	"fmt"
	"io"

	gloo "github.com/gloo-foo/framework"
)

// ============================================================================
// Helper Patterns for Command Developers
//
// These functions provide common patterns for building commands:
//   - LineTransform: Process lines one at a time (grep, tr)
//   - StatefulLineTransform: Process lines with state (nl, uniq)
//   - AccumulateAndProcess: Collect all lines, then process (sort, shuf)
//   - AccumulateAndOutput: Collect all lines, custom output (wc)
//   - RawCommand: Full control over I/O (diff, find, ls)
// ============================================================================

// LineTransformFunc is a simple line-by-line transformation.
// Return (output, true) to emit the line, or (_, false) to skip it.
type LineTransformFunc func(line string) (output string, emit bool)

// StatefulLineTransformFunc is a line transformation with line number tracking.
// Return (output, true) to emit the line, or (_, false) to skip it.
type StatefulLineTransformFunc func(lineNum int64, line string) (output string, emit bool)

// AccumulateAndProcessFunc collects all lines, processes them, and returns the result.
type AccumulateAndProcessFunc func(lines []string) []string

// AccumulateAndOutputFunc collects all lines, processes them, and outputs directly.
type AccumulateAndOutputFunc func(lines []string, stdout io.Writer) error

// ============================================================================
// Helper Functions
// ============================================================================

// LineTransform creates a gloo.Command that transforms lines one at a time.
// Best for: grep, cut, tr, sed - stateless line transformations
//
// Example:
//
//	func (c command) Executor() gloo.CommandExecutor {
//	    return gloo.LineTransform(func(line string) (string, bool) {
//	        if strings.Contains(line, c.pattern) {
//	            return line, true
//	        }
//	        return "", false
//	    }).Executor()
//	}
func LineTransform(fn LineTransformFunc) gloo.Command {
	return &lineTransformCommand{fn: fn}
}

type lineTransformCommand struct {
	fn LineTransformFunc
}

func (c *lineTransformCommand) Executor() gloo.CommandExecutor {
	return func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
		scanner := bufio.NewScanner(stdin)
		for scanner.Scan() {
			line := scanner.Text()
			output, emit := c.fn(line)
			if emit {
				if _, err := fmt.Fprintln(stdout, output); err != nil {
					return err
				}
			}
		}
		return scanner.Err()
	}
}

// StatefulLineTransform creates a Command that transforms lines with state tracking.
// Best for: uniq, nl, head - need line numbers or previous line tracking
//
// Example:
//
//	func (c command) Executor() gloo.CommandExecutor {
//	    return gloo.StatefulLineTransform(func(lineNum int64, line string) (string, bool) {
//	        if lineNum <= c.maxLines {
//	            return line, true
//	        }
//	        return "", false
//	    }).Executor()
//	}
func StatefulLineTransform(fn StatefulLineTransformFunc) gloo.Command {
	return &statefulLineCommand{fn: fn}
}

type statefulLineCommand struct {
	fn StatefulLineTransformFunc
}

func (c *statefulLineCommand) Executor() gloo.CommandExecutor {
	return func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
		scanner := bufio.NewScanner(stdin)
		lineNum := int64(0)
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			output, emit := c.fn(lineNum, line)
			if emit {
				if _, err := fmt.Fprintln(stdout, output); err != nil {
					return err
				}
			}
		}
		return scanner.Err()
	}
}

// AccumulateAndProcess creates a Command that collects all lines, processes them, and outputs.
// Best for: sort, tac, shuf - need to see all input before producing output
//
// Example:
//
//	func (c command) Executor() gloo.CommandExecutor {
//	    return gloo.AccumulateAndProcess(func(lines []string) []string {
//	        sort.Strings(lines)
//	        return lines
//	    }).Executor()
//	}
func AccumulateAndProcess(fn AccumulateAndProcessFunc) gloo.Command {
	return &accumulatorCommand{fn: fn}
}

type accumulatorCommand struct {
	fn AccumulateAndProcessFunc
}

func (c *accumulatorCommand) Executor() gloo.CommandExecutor {
	return func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
		// Collect all lines
		var lines []string
		scanner := bufio.NewScanner(stdin)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return err
		}

		// Process lines
		result := c.fn(lines)

		// Output result
		for _, line := range result {
			if _, err := fmt.Fprintln(stdout, line); err != nil {
				return err
			}
		}
		return nil
	}
}

// AccumulateAndOutput creates a Command that collects all lines and outputs with full control.
// Best for: wc - need to accumulate and produce custom output format
//
// Example:
//
//	func (c command) Executor() gloo.CommandExecutor {
//	    return gloo.AccumulateAndOutput(func(lines []string, stdout io.Writer) error {
//	        fmt.Fprintf(stdout, "%d lines\n", len(lines))
//	        return nil
//	    }).Executor()
//	}
func AccumulateAndOutput(fn AccumulateAndOutputFunc) gloo.Command {
	return &accumulateOutputCommand{fn: fn}
}

type accumulateOutputCommand struct {
	fn AccumulateAndOutputFunc
}

func (c *accumulateOutputCommand) Executor() gloo.CommandExecutor {
	return func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
		// Collect all lines
		var lines []string
		scanner := bufio.NewScanner(stdin)
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return err
		}

		// Process and output with custom logic
		return c.fn(lines, stdout)
	}
}

// RawCommand wraps a raw CommandExecutor function.
// Best for: diff, paste, comm, find, ls - need full control over I/O
//
// Example:
//
//	func (c command) Executor() gloo.CommandExecutor {
//	    return gloo.RawCommand(func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
//	        // Full control over I/O
//	        return nil
//	    }).Executor()
//	}
func RawCommand(fn gloo.CommandExecutor) gloo.Command {
	return &rawCommand{fn: fn}
}

type rawCommand struct {
	fn gloo.CommandExecutor
}

func (c *rawCommand) Executor() gloo.CommandExecutor {
	return c.fn
}
