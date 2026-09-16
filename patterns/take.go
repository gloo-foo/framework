package patterns

import (
	"github.com/destel/rill"

	gloo "github.com/gloo-foo/framework"
)

// Take emits the first n items of its input and then stops the upstream — the
// framework equivalent of `head -n` exiting and killing its feeders via
// SIGPIPE.
//
// This is the early-termination primitive that StatefulFilter cannot express:
// a filter may drop items but can never declare "I need nothing more", so a
// filter-based head over an infinite or huge source reads everything. Take
// actively tears the upstream down, so `Take(3)` over an infinite source
// terminates instantly and leaks no goroutines, and `FileSource | Take(3)`
// reads only a small prefix of the file.
//
// Stages DOWNSTREAM of Take run to completion normally: `Take(3)` composed
// before a sort still yields three sorted lines, exactly as
// `seq inf | head -3 | sort` does. Take is the building block for head,
// grep -m, sed q, and any user-defined early exit.
//
// Take stops at the FIRST upstream error: the error is forwarded and nothing
// after it is emitted — unlike the per-item transforms (Map, Filter, Expand,
// Tap), which continue past error items. An early exit that kept reading a
// broken input would defeat its purpose.
//
// n <= 0 emits nothing and stops the upstream immediately.
func Take[T any](n Count) gloo.Command[T, T] {
	return produce(func(in gloo.Stream[T], send func(T) bool, sendErr func(error)) {
		emitN(n, in, send, sendErr)
		// SIGPIPE analogue: tell the upstream we are done, then drain so a
		// buffered producer can unblock and exit.
		in.Discard()
	})
}

// Count is the number of items a prefix pattern (Take, Head, Drop) emits or
// skips — the N of `head -n N`.
type Count int

// emitN forwards up to n items from in. It returns as soon as n are sent, the
// input errors, or the consumer downstream of Take stops reading.
func emitN[T any](n Count, in gloo.Stream[T], send func(T) bool, sendErr func(error)) {
	if int(n) <= 0 {
		return
	}
	count := 0
	for item := range in.Chan() {
		if !forward(item, send, sendErr) {
			return
		}
		count++
		if count >= int(n) {
			return
		}
	}
}

// forward emits one item's value, surfacing its error instead. It returns
// whether emission should continue.
func forward[T any](item rill.Try[T], send func(T) bool, sendErr func(error)) bool {
	if item.Error != nil {
		sendErr(item.Error)
		return false
	}
	return send(item.Value)
}
