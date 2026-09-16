package patterns

import (
	gloo "github.com/gloo-foo/framework"
)

// Accumulate creates a Command that collects all input, processes it, then emits output.
// The function receives the complete input as a slice and returns the complete output as a slice.
//
// This is the pattern for commands that must see all input before producing any output.
// The input/output types are the same — items go in, (possibly reordered/trimmed) items come out.
//
// Shell equivalents: sort, tac, tail -n, shuf
//
// Example — building a Sort command:
//
//	func Sort() gloo.Command[string, string] {
//	    return patterns.Accumulate(func(lines []string) ([]string, error) {
//	        sort.Strings(lines)
//	        return lines, nil
//	    })
//	}
//
// Example — building a Tail command:
//
//	func Tail(n int) gloo.Command[string, string] {
//	    return patterns.Accumulate(func(lines []string) ([]string, error) {
//	        if len(lines) <= n {
//	            return lines, nil
//	        }
//	        return lines[len(lines)-n:], nil
//	    })
//	}
func Accumulate[T any](fn func([]T) ([]T, error)) gloo.Command[T, T] {
	return produce(func(in gloo.Stream[T], send func(T) bool, sendErr func(error)) {
		accumulate(in, fn, send, sendErr)
	})
}

// accumulate drains the input, applies fn, and emits each resulting item.
func accumulate[T any](in gloo.Stream[T], fn func([]T) ([]T, error), send func(T) bool, sendErr func(error)) {
	items, ok := collectInput(in, sendErr)
	if !ok {
		return
	}
	result, err := fn(items)
	if err != nil {
		sendErr(err)
		return
	}
	sendEach(result, send)
}
