package patterns

import (
	"context"

	"github.com/destel/rill"

	gloo "github.com/gloo-foo/framework"
)

// Less orders two values; it reports whether a sorts before b.
type Less[T any] func(a, b T) bool

// MergeSorted merges two already-sorted streams into one sorted stream — the
// two-input primitive behind `sort -m` and the streaming half of comm/join.
// Both inputs are owned by the merge: a downstream stop or an error on either
// input stops and drains BOTH, so one Discard still collapses the whole graph.
//
// On the first error from either input the error is forwarded and the merge
// stops — a broken input makes the merged order meaningless.
func MergeSorted[T any](ctx context.Context, less Less[T], a, b gloo.Stream[T]) gloo.Stream[T] {
	return gloo.Generate(ctx, func(_ context.Context, send func(T) bool, sendErr func(error)) {
		defer a.Discard()
		defer b.Discard()
		merge(less, newCursor(a), newCursor(b), send, sendErr)
	})
}

// cursor is a pull-style reader over one stream: cur holds the current value
// while isLoaded reports whether one is held; err holds the first stream
// error. It is an immutable value: next returns the ADVANCED cursor.
type cursor[T any] struct {
	ch       <-chan rill.Try[T]
	err      error
	cur      T
	isLoaded bool
}

func newCursor[T any](s gloo.Stream[T]) cursor[T] {
	return cursor[T]{ch: s.Chan()}.next()
}

// next returns the cursor advanced to the following value, unloaded on error
// or end of stream (recording the error).
func (c cursor[T]) next() cursor[T] {
	item, open := <-c.ch
	if !open || item.Error != nil {
		c.isLoaded = false
		c.err = item.Error
		return c
	}
	c.cur, c.isLoaded = item.Value, true
	return c
}

// fail forwards a recorded input error, reporting whether one ended the merge.
func (c cursor[T]) fail(sendErr func(error)) bool {
	if c.err != nil {
		sendErr(c.err)
		return true
	}
	return false
}

// merge interleaves the two cursors in order, then drains whichever side
// remains; the first error on either side ends the merge.
func merge[T any](less Less[T], a, b cursor[T], send func(T) bool, sendErr func(error)) {
	a, b, stopped := interleave(less, a, b, send)
	if stopped || a.fail(sendErr) || b.fail(sendErr) {
		return
	}
	rest, reading := drainAll(a, send)
	if !reading || rest.fail(sendErr) {
		return
	}
	rest, reading = drainAll(b, send)
	if reading {
		rest.fail(sendErr)
	}
}

// interleave emits the smaller head while both sides hold values, preferring
// the first input on ties. It reports the final cursors and whether the
// downstream stopped reading.
func interleave[T any](less Less[T], a, b cursor[T], send func(T) bool) (cursor[T], cursor[T], bool) {
	reading := true
	for reading && a.isLoaded && b.isLoaded {
		if less(b.cur, a.cur) {
			b, reading = emitHead(b, send)
		} else {
			a, reading = emitHead(a, send)
		}
	}
	return a, b, !reading
}

// emitHead sends the cursor's loaded value, reporting the advanced cursor and
// whether the downstream is still reading.
func emitHead[T any](c cursor[T], send func(T) bool) (cursor[T], bool) {
	if !send(c.cur) {
		return c, false
	}
	return c.next(), true
}

// drainAll emits the cursor's remainder in order, reporting the final cursor
// (whose err a caller must surface) and whether the downstream kept reading.
func drainAll[T any](c cursor[T], send func(T) bool) (cursor[T], bool) {
	reading := true
	for reading && c.isLoaded {
		c, reading = emitHead(c, send)
	}
	return c, reading
}
