package patterns

import (
	gloo "github.com/gloo-foo/framework"
)

// Fail creates a Command that emits exactly one error and nothing else,
// stopping its upstream. It is the canonical way to surface a construction
// failure (an invalid pattern, an unparsable flag) from a command factory that
// cannot return an error itself: the pipeline still composes, and the failure
// arrives where every other failure does — on the stream.
//
// Shell equivalent: a tool that prints a diagnostic and exits non-zero before
// reading input.
//
// Example — a grep whose pattern does not compile:
//
//	func Grep(pattern string) gloo.Command[string, string] {
//	    re, err := regexp.Compile(pattern)
//	    if err != nil {
//	        return patterns.Fail[string](err)
//	    }
//	    return patterns.Filter(func(line string) (bool, error) { return re.MatchString(line), nil })
//	}
func Fail[T any](err error) gloo.Command[T, T] {
	return produce(func(_ gloo.Stream[T], _ func(T) bool, sendErr func(error)) {
		sendErr(err)
	})
}
