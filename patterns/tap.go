package patterns

import (
	gloo "github.com/gloo-foo/framework"
)

// Tap creates a Command that passes each item through unchanged while calling
// a side-effect function. An error from the side-effect is emitted as an error
// item in place of that item; the stream itself CONTINUES (later items are
// still observed), exactly like the other per-item patterns — it is the
// consuming terminal that stops a pipeline at its first error.
//
// This is the pattern for commands that observe the stream without modifying it
// — writing to files, logging, metrics collection.
//
// Shell equivalents: tee (write to file while passing through)
//
// Example — building a Tee command (open once, append each line):
//
//	func Tee(w io.Writer) gloo.Command[[]byte, []byte] {
//	    return patterns.Tap(func(line []byte) error {
//	        return gloo.Output(w, line)
//	    })
//	}
func Tap[T any](fn func(T) error) gloo.Command[T, T] {
	return Map(func(item T) (T, error) {
		return item, fn(item)
	})
}
