// Package gloo provides a framework for building Unix-style command-line tools in Go.
//
// # For Command Users
//
// If you're using gloo.foo commands, you'll primarily interact with:
//   - Command interface: Represents any executable command
//   - Run: Execute a command with os.Stdin/Stdout/Stderr
//   - MustRun: Execute a command and panic on error (useful for examples/tests)
//
// Example:
//
//	cmd := cat.Cat("file.txt")
//	gloo.Run(cmd)
//
// # For Command Developers
//
// If you're building gloo.foo commands, see:
//   - types.go: Core types (File, Inputs, Switch)
//   - initialize.go: Parameter parsing with Initialize()
//   - helpers.go: Helper patterns for common command implementations
package gloo

import (
	"context"
	"io"
	"os"
)

// CommandExecutor is the function signature for executing a command.
// It receives context, input/output streams, and returns an error.
type CommandExecutor func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error

// Command represents an executable command.
// all gloo.foo commands implement this interface.
type Command interface {
	Executor() CommandExecutor
}

// Run executes a command with the standard os.Stdin, os.Stdout, and os.Stderr streams.
// This is the primary way to run commands in production code.
//
// Example:
//
//	cmd := cat.Cat("file.txt")
//	if err := gloo.Run(cmd); err != nil {
//	    log.Fatal(err)
//	}
func Run(cmd Command) error {
	return RunWithContext(context.Background(), cmd)
}

// RunWithContext executes a command with a custom context and the standard os.Stdin, os.Stdout, and os.Stderr streams.
// Use this when you need to pass cancellation signals, deadlines, or context values to the command.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	cmd := grep.Grep("pattern", "largefile.txt")
//	if err := gloo.RunWithContext(ctx, cmd); err != nil {
//	    log.Fatal(err)
//	}
func RunWithContext(ctx context.Context, cmd Command) error {
	executor := cmd.Executor()
	return executor(ctx, os.Stdin, os.Stdout, os.Stderr)
}

// Must panics if the provided error is non-nil.
// This is useful for examples and tests where you want to fail fast on errors.
//
// Example:
//
//	result, err := someOperation()
//	gloo.Must(err)
//	// proceed with result
func Must(err error) {
	if err != nil {
		panic(err)
	}
}

// MustRun runs a command and panics if it returns an error.
// This is useful for examples and tests where you want to fail fast.
//
// Example:
//
//	func ExampleCat() {
//	    gloo.MustRun(cat.Cat("file.txt"))
//	}
func MustRun(cmd Command) { Must(Run(cmd)) }
