package patterns

import (
	"context"

	gloo "github.com/gloo-foo/framework"
)

// produce builds a Command whose output is produced by a cancellation-aware
// body over the input stream, hiding the FuncCommand + Generate plumbing. Stop
// propagates to the input via in.Stop, so a downstream stop tears the whole
// chain down (the SIGPIPE analogue).
func produce[In, Out any](
	body func(in gloo.Stream[In], send func(Out) bool, sendErr func(error)),
) gloo.Command[In, Out] {
	return gloo.FuncCommand[In, Out](func(ctx context.Context, in gloo.Stream[In]) gloo.Stream[Out] {
		return gloo.GenerateFrom(ctx, in, func(_ context.Context, send func(Out) bool, sendErr func(error)) {
			body(in, send, sendErr)
		})
	})
}

// collectInput drains the whole input into a slice, reporting any stream error.
// The bool reports whether collection succeeded. It collects via Stream.Collect,
// which on the first error DISCARDS the upstream (stop + drain) — the SIGPIPE
// analogue — so an erroring upstream that would keep producing is stopped, not
// merely abandoned.
func collectInput[In any](in gloo.Stream[In], sendErr func(error)) ([]In, bool) {
	items, err := in.Collect()
	if err != nil {
		sendErr(err)
		return nil, false
	}
	return items, true
}

// sendEach emits each item in order, stopping early if the consumer stopped.
func sendEach[T any](items []T, send func(T) bool) {
	for _, item := range items {
		if !send(item) {
			return
		}
	}
}

// eachValue drives every input item through visit, forwarding upstream error
// items unchanged and CONTINUING — a shell filter keeps processing after
// writing an error line. visit returns false once the downstream has stopped;
// the loop then returns and produce's teardown discards the rest.
func eachValue[In any](in gloo.Stream[In], sendErr func(error), visit func(In) bool) {
	for item := range in.Chan() {
		if item.Error != nil {
			sendErr(item.Error)
			continue
		}
		if !visit(item.Value) {
			return
		}
	}
}
