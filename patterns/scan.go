package patterns

import (
	gloo "github.com/gloo-foo/framework"
)

// Scan creates a Command with per-Execute state that may emit zero or more
// outputs per input item, plus a final batch when the input ends. The factory
// returns the pair (step, end): step handles one item; end flushes whatever
// state remains once the stream closes.
//
// This is the pattern for commands whose output depends on stream BOUNDARIES —
// state that must be emitted when the input ends, which Map/Filter cannot
// express and Accumulate can only express by buffering everything.
//
// Shell equivalents: uniq -c (flush the last group), awk END blocks,
// grep -A/-B context windows, paste -s
//
// Example — building uniq -c:
//
//	func UniqC() gloo.Command[string, string] {
//	    return patterns.Scan(func() (func(string) ([]string, error), func() ([]string, error)) {
//	        var cur string
//	        n := 0
//	        step := func(line string) ([]string, error) {
//	            if n > 0 && line == cur {
//	                n++
//	                return nil, nil
//	            }
//	            out := group(n, cur) // previous group, if any
//	            cur, n = line, 1
//	            return out, nil
//	        }
//	        end := func() ([]string, error) { return group(n, cur), nil }
//	        return step, end
//	    })
//	}
func Scan[In, Out any](
	factory func() (step func(In) ([]Out, error), end func() ([]Out, error)),
) gloo.Command[In, Out] {
	return produce(func(in gloo.Stream[In], send func(Out) bool, sendErr func(error)) {
		step, end := factory()
		if !scanAll(in, step, send, sendErr) {
			return
		}
		flush(end, send, sendErr)
	})
}

// scanAll drives every input item through step, reporting whether the
// downstream is still reading at the end of the input.
func scanAll[In, Out any](
	in gloo.Stream[In],
	step func(In) ([]Out, error),
	send func(Out) bool,
	sendErr func(error),
) bool {
	reading := true
	eachValue(in, sendErr, func(v In) bool {
		outs, err := step(v)
		if err != nil {
			sendErr(err)
			return true // the stream continues past a failed item
		}
		reading = sendAll(outs, send)
		return reading
	})
	return reading
}

// flush emits the end-of-stream batch.
func flush[Out any](end func() ([]Out, error), send func(Out) bool, sendErr func(error)) {
	outs, err := end()
	if err != nil {
		sendErr(err)
		return
	}
	sendAll(outs, send)
}
