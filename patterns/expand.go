package patterns

import (
	gloo "github.com/gloo-foo/framework"
)

// Expand creates a Command that maps each input item to zero or more output items.
// The function receives one item and returns a slice of results.
// Output order follows input order — all outputs from item 1 appear before item 2's outputs.
//
// This is the pattern for commands that split, unfold, or fan out each input line.
//
// Shell equivalents: fold (wrap long lines), split-like operations, xargs -n1
//
// Example — building a Split command (split on delimiter):
//
//	func Split(delim string) gloo.Command[string, string] {
//	    return patterns.Expand(func(line string) ([]string, error) {
//	        return strings.Split(line, delim), nil
//	    })
//	}
//
// Example — building a Words command (emit each word as a separate line):
//
//	func Words() gloo.Command[string, string] {
//	    return patterns.Expand(func(line string) ([]string, error) {
//	        return strings.Fields(line), nil
//	    })
//	}
func Expand[In, Out any](fn func(In) ([]Out, error)) gloo.Command[In, Out] {
	return produce(func(in gloo.Stream[In], send func(Out) bool, sendErr func(error)) {
		eachValue(in, sendErr, func(v In) bool {
			results, err := fn(v)
			if err != nil {
				sendErr(err)
				return true // the stream continues past a failed item
			}
			return sendAll(results, send)
		})
	})
}

// sendAll emits every expansion of one item, reporting whether the downstream
// is still reading.
func sendAll[T any](items []T, send func(T) bool) bool {
	for _, item := range items {
		if !send(item) {
			return false
		}
	}
	return true
}
