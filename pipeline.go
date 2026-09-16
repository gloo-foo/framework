package gloo

import (
	"context"
)

// Pipe composes two commands into a new command.
// It connects the output of cmd1 to the input of cmd2.
// The result is immutable and safe to reuse across pipelines.
func Pipe[A, B, C any](cmd1 Command[A, B], cmd2 Command[B, C]) Command[A, C] {
	return pipelined[A, B, C]{first: cmd1, second: cmd2}
}

// pipelined is the immutable result of Pipe. Value receiver — no mutation.
type pipelined[A, B, C any] struct {
	first  Command[A, B]
	second Command[B, C]
}

func (p pipelined[A, B, C]) Execute(ctx context.Context, input Stream[A]) Stream[C] {
	return p.second.Execute(ctx, p.first.Execute(ctx, input))
}

// FuncCommand adapts a function to the Command interface.
// Value type — no pointer receiver, safe to copy and reuse.
type FuncCommand[In, Out any] func(context.Context, Stream[In]) Stream[Out]

func (f FuncCommand[In, Out]) Execute(ctx context.Context, input Stream[In]) Stream[Out] {
	return f(ctx, input)
}

// Compose starts a same-type command chain from an initial Command[T, T].
// Each .To() returns a NEW Pipeline (immutable). The resulting Pipeline
// implements Command[T, T] and is safe to reuse across multiple pipelines.
//
//	pipeline := gloo.Compose(grepCmd).To(sortCmd).To(uniqCmd)
//	// pipeline implements Command[T, T]
func Compose[T any](cmd Command[T, T]) Pipeline[T] {
	return Pipeline[T]{cmds: []Command[T, T]{cmd}}
}

// Pipeline is an immutable same-type command chain. Implements Command[T, T].
// Value receiver on all methods — safe to copy, assign, and reuse.
type Pipeline[T any] struct {
	cmds []Command[T, T]
}

// To returns a NEW Pipeline with the command appended. Does not mutate
// the original.
func (p Pipeline[T]) To(cmd Command[T, T]) Pipeline[T] {
	next := make([]Command[T, T], len(p.cmds)+1)
	copy(next, p.cmds)
	next[len(p.cmds)] = cmd
	return Pipeline[T]{cmds: next}
}

// Execute runs all chained commands in sequence.
func (p Pipeline[T]) Execute(ctx context.Context, input Stream[T]) Stream[T] {
	stream := input
	for _, cmd := range p.cmds {
		stream = cmd.Execute(ctx, stream)
	}
	return stream
}

// Compile-time check that Pipeline satisfies Command.
var _ Command[string, string] = Pipeline[string]{}
