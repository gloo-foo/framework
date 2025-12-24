# Gloo Framework

A Go framework for building composable Unix-style command-line tools, powered by [github.com/destel/rill](https://github.com/destel/rill) for concurrent stream processing.

## What is Gloo?

Gloo lets you build commands that work like traditional Unix tools - they read from stdin, write to stdout, and can be piped together. Commands are type-safe, composable, easy to test, and can leverage powerful concurrent processing capabilities.

### Key Features

- **Unix-style composition**: Pipe commands together like shell scripts
- **Type-safe pipelines**: Process strings, bytes, or custom types
- **Concurrent processing**: Built on rill for efficient parallel execution
- **Order preservation**: Maintain sequence when needed
- **Automatic error handling**: Errors propagate through pipelines
- **Zero external dependencies**: Besides rill, which itself has zero dependencies

## Quick Start

### Installing

```bash
go get github.com/gloo-foo/framework
```

### Using Commands

Commands are used just like Unix tools:

```go
import (
    "github.com/gloo-foo/framework"
    "github.com/yupsh/grep"
)

func main() {
    // Simple usage
    gloo.MustRun(grep.Grep("error", "logfile.txt"))

    // With flags
    gloo.MustRun(grep.Grep("ERROR", "log.txt", grep.IgnoreCase))

    // From stdin
    gloo.MustRun(grep.Grep("pattern"))
}
```

### Piping Commands

Commands can be piped together:

```go
// cat file.txt | grep "error" | sort
pipeline := gloo.Pipe(
    cat.Cat("file.txt"),
    grep.Grep("error"),
    sort.Sort(),
)
gloo.MustRun(pipeline)
```

## Building Commands

See [framework-examples](../framework-examples/) for complete examples of building your own commands.

### Basic Command Structure

```go
package mycommand

import gloo "github.com/gloo-foo/framework"

type command struct {
    inputs gloo.Inputs[gloo.File, Flags]
}

func MyCommand(params ...any) gloo.Command {
    inputs := gloo.Initialize[gloo.File, Flags](params...)
    return command{inputs: inputs}
}

func (c command) Executor() gloo.CommandExecutor {
    // Implementation details...
}
```

### Custom Types

Commands can work with structured data:

```go
type LogEntry struct {
    Level   string
    Message string
}

// Commands can process strongly-typed data
inputs := gloo.Initialize[LogEntry, Flags](params...)
```

## API Reference

See [API_REFERENCE.md](./API_REFERENCE.md) for complete API documentation.

### Core Types

- `Command` - Interface for all commands
- `CommandExecutor` - Function that executes a command
- `Inputs[T, O]` - Parsed parameters with positionals and flags
- `File` - Type for file path parameters

### Main Functions

- `gloo.Run(cmd)` - Execute a command
- `gloo.MustRun(cmd)` - Execute a command, panic on error
- `gloo.Pipe(cmd1, cmd2, ...)` - Chain commands together
- `gloo.Initialize[T, O](params...)` - Parse command parameters

## Examples

See [framework-examples](../framework-examples/) for complete working examples:

- Custom struct parameters with flags
- Strongly-typed positional arguments
- Building pipeable commands

## Testing

Commands are easy to test:

```go
func TestMyCommand(t *testing.T) {
    input := strings.NewReader("test input")
    output := &bytes.Buffer{}

    cmd := MyCommand("args")
    err := cmd.Executor()(context.Background(), input, output, os.Stderr)

    // Assert on output.String()
}
```

## License

See [LICENSE](./LICENSE) file.
