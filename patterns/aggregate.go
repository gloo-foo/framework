package patterns

import (
	gloo "github.com/gloo-foo/framework"
)

// Aggregate creates a Command that reduces all input items to a single output value.
// The function receives the complete input as a slice and returns one result.
// The output stream contains exactly one item.
//
// This is the pattern for commands that consume everything and produce a summary.
// Unlike Accumulate, the output type can differ from the input type.
//
// Shell equivalents: wc -l, wc -w, sha256sum, md5sum, cksum
//
// Example — building a Wc command:
//
//	func Wc() gloo.Command[string, int] {
//	    return patterns.Aggregate(func(lines []string) (int, error) {
//	        return len(lines), nil
//	    })
//	}
//
// Example — building a Sha256sum command:
//
//	func Sha256sum() gloo.Command[[]byte, string] {
//	    return patterns.Aggregate(func(chunks [][]byte) (string, error) {
//	        h := sha256.New()
//	        for _, chunk := range chunks {
//	            h.Write(chunk)
//	        }
//	        return hex.EncodeToString(h.Sum(nil)), nil
//	    })
//	}
func Aggregate[In, Out any](fn func([]In) (Out, error)) gloo.Command[In, Out] {
	return produce(func(in gloo.Stream[In], send func(Out) bool, sendErr func(error)) {
		aggregate(in, fn, send, sendErr)
	})
}

// aggregate drains the input, applies fn, and emits the single result.
func aggregate[In, Out any](in gloo.Stream[In], fn func([]In) (Out, error), send func(Out) bool, sendErr func(error)) {
	items, ok := collectInput(in, sendErr)
	if !ok {
		return
	}
	result, err := fn(items)
	if err != nil {
		sendErr(err)
		return
	}
	send(result)
}
