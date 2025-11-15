# Gloo Framework - API Reference

This reference documents the public API for **using** and **building** commands with the Gloo framework.

## Using Commands

### Running Commands

```go
// Run a command, return error
func Run(cmd Command) error

// Run a command, panic on error
func MustRun(cmd Command)

// Run with custom context
func RunWithContext(ctx context.Context, cmd Command) error
```

**Example:**

```go
gloo.MustRun(grep.Grep("error", "logfile.txt"))
```

### Piping Commands

```go
// Chain commands together
func Pipe(executors ...ChannelExecutor[string]) ChannelExecutor[string]
```

**Example:**

```go
pipeline := gloo.Pipe(
    cat.Cat("file.txt").ChannelExecutor(),
    grep.Grep("error").ChannelExecutor(),
    sort.Sort().ChannelExecutor(),
)
gloo.MustRun(gloo.Pipeline(pipeline))
```

## Building Commands

### Core Interfaces

Commands implement one of these interfaces:

```go
// Standard command interface
type Command interface {
    Executor() CommandExecutor
}

type CommandExecutor func(
    ctx context.Context,
    stdin io.Reader,
    stdout, stderr io.Writer,
) error
```

### Command Constructor Pattern

```go
func MyCommand(params ...any) gloo.Command {
    inputs := gloo.Initialize[PositionalType, FlagsType](params...)
    return myCommand{inputs: inputs}
}
```

### Parameter Parsing

```go
// Parse parameters into positionals and flags
func Initialize[T any, O any](parameters ...any) Inputs[T, O]
```

**Type Parameters:**
- `T` - Type of positional arguments (e.g., `gloo.File`, custom types)
- `O` - Type of flags struct

**Example:**

```go
// Accept files and flags
inputs := gloo.Initialize[gloo.File, MyFlags](params...)

// Access parsed values
for _, file := range inputs.Positional { /* ... */ }
if inputs.Flags.Verbose { /* ... */ }
```

### Built-in Types

#### File

```go
type File string
```

Use `gloo.File` as the positional type to accept file paths:

```go
inputs := gloo.Initialize[gloo.File, Flags](params...)
// Framework automatically opens files
```

#### Inputs

```go
type Inputs[T any, O any] struct {
    Positional []T      // Parsed positional arguments
    Flags      O        // Parsed flags
    Ambiguous  []any    // Arguments that couldn't be parsed
}
```

**Methods:**

```go
// Get combined reader from all files (or stdin if no files)
func (inputs Inputs[T, O]) Reader(stdin io.Reader) io.Reader

// Wrap an executor to use parsed inputs
func (inputs Inputs[T, O]) Wrap(executor CommandExecutor) CommandExecutor

// Close opened files
func (inputs Inputs[T, O]) Close() error
```

### Flags

Flags use Go's type system for compile-time safety:

```go
// Define flag type
type IgnoreCase bool

const (
    CaseSensitive   IgnoreCase = false
    CaseInsensitive IgnoreCase = true
)

// Implement Switch interface
func (i IgnoreCase) Configure(flags *MyFlags) {
    flags.IgnoreCase = i
}

// Define flags struct
type MyFlags struct {
    IgnoreCase bool
}
```

**Usage:**

```go
// User passes typed flags
cmd := MyCommand("pattern", "file.txt", CaseInsensitive)

// Command accesses flags
if inputs.Flags.IgnoreCase { /* ... */ }
```

### Flag Interface

```go
type Switch[T any] interface {
    Configure(*T)
}
```

All flag types must implement `Switch` to configure the flags struct.

## Custom Positional Types

Use custom types for strongly-typed parameters:

```go
type URL string

func FetchURLs(urls ...any) gloo.Command {
    inputs := gloo.Initialize[URL, struct{}](urls...)
    // inputs.Positional is []URL
    return urlCommand{inputs: inputs}
}

// Usage:
cmd := FetchURLs(
    URL("https://example.com"),
    URL("https://github.com"),
)
```

## Testing Commands

Commands are easy to test with standard Go testing:

```go
func TestMyCommand(t *testing.T) {
    input := strings.NewReader("test input\n")
    output := &bytes.Buffer{}

    cmd := MyCommand("args", MyFlag)
    err := cmd.Executor()(context.Background(), input, output, os.Stderr)

    if err != nil {
        t.Fatalf("Unexpected error: %v", err)
    }

    if output.String() != "expected output\n" {
        t.Errorf("Got: %s", output.String())
    }
}
```

## Advanced: Custom Type Parsing

Commands can automatically parse custom types from stdin by implementing:

```go
type RowParser interface {
    ParseRow(line string) error
}
```

**Example:**

```go
type LogEntry struct {
    Level   string
    Message string
}

func (e *LogEntry) ParseRow(line string) error {
    parts := strings.SplitN(line, ":", 2)
    if len(parts) != 2 {
        return fmt.Errorf("invalid format")
    }
    e.Level = parts[0]
    e.Message = parts[1]
    return nil
}
```

Now `LogEntry` can be used in channel-based commands with automatic parsing.

## Complete Example

```go
package mycommand

import (
    "context"
    "io"
    "strings"

    gloo "github.com/gloo-foo/framework"
)

// Flags
type CaseSensitivity bool
const (
    CaseSensitive   CaseSensitivity = false
    CaseInsensitive CaseSensitivity = true
)

func (c CaseSensitivity) Configure(f *Flags) { f.IgnoreCase = bool(c) }

type Flags struct {
    IgnoreCase bool
}

// Command
type command struct {
    pattern string
    inputs  gloo.Inputs[gloo.File, Flags]
}

func Grep(pattern string, params ...any) gloo.Command {
    inputs := gloo.Initialize[gloo.File, Flags](params...)
    return command{pattern: pattern, inputs: inputs}
}

func (c command) Executor() gloo.CommandExecutor {
    return c.inputs.Wrap(func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer) error {
        // Read from stdin line by line
        // Check if line contains pattern
        // Write matching lines to stdout
        return nil
    })
}
```

## See Also

- [README.md](./README.md) - Quick start guide
- [framework-examples](../framework-examples/) - Complete working examples
