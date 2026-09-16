package gloo

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/destel/rill"
)

// The fluent builder reports every validation failure as an error you can match
// with errors.Is — never a panic. These tests pin each failure mode to its
// sentinel.

func TestChainErrorsOnNonSource(t *testing.T) {
	_, err := Chain(struct{}{}).Collect()
	if !errors.Is(err, ErrNotSource) {
		t.Errorf("got %v, want ErrNotSource", err)
	}
}

func TestToErrorsOnNonCommand(t *testing.T) {
	src := SliceSource([]string{"x"})
	_, err := Chain(src).To(struct{}{}).Collect()
	if !errors.Is(err, ErrNotCommand) {
		t.Errorf("got %v, want ErrNotCommand", err)
	}
}

type badArityCommand struct{}

func (badArityCommand) Execute() {}

func TestToErrorsOnWrongExecuteSignature(t *testing.T) {
	src := SliceSource([]string{"x"})
	_, err := Chain(src).To(badArityCommand{}).Collect()
	if !errors.Is(err, ErrNotCommand) {
		t.Errorf("got %v, want ErrNotCommand", err)
	}
}

func TestToErrorsOnStageTypeMismatch(t *testing.T) {
	src := SliceSource([]string{"x"})
	wantsInts := mapCmd(func(n int) (int, error) { return n, nil })
	_, err := Chain(src).To(wantsInts).Collect()
	if !errors.Is(err, ErrStageTypeMismatch) {
		t.Errorf("got %v, want ErrStageTypeMismatch", err)
	}
}

// A mismatch error must name both types so the message is actionable.
func TestStageTypeMismatchNamesTypes(t *testing.T) {
	src := SliceSource([]string{"x"})
	wantsInts := mapCmd(func(n int) (int, error) { return n, nil })
	_, err := Chain(src).To(wantsInts).Collect()
	msg := err.Error()
	// The ordered phrasing must be correct: the int-command EXPECTS int, while
	// the source PRODUCES string. A want/got swap in mismatch() would still
	// contain both type names, so assert the full ordered fragments.
	if !contains(msg, "stage expects gloo.Stream[int]") {
		t.Errorf("message %q must say the stage expects the int stream", msg)
	}
	if !contains(msg, "upstream produces gloo.Stream[string]") {
		t.Errorf("message %q must say the upstream produces the string stream", msg)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// The first error is sticky: a later valid stage does not clear it, and the
// terminal still reports the original failure.
func TestFirstErrorIsSticky(t *testing.T) {
	src := SliceSource([]string{"x"})
	pass := mapCmd(func(s string) (string, error) { return s, nil })
	_, err := Chain(src).To(struct{}{}).To(pass).Collect()
	if !errors.Is(err, ErrNotCommand) {
		t.Errorf("got %v, want the first error ErrNotCommand to stick", err)
	}
}

func TestSinkErrorsOnNonSink(t *testing.T) {
	src := SliceSource([]string{"x"})
	_, err := Chain(src).Sink(struct{}{})
	if !errors.Is(err, ErrNotSink) {
		t.Errorf("got %v, want ErrNotSink", err)
	}
}

type badAritySink struct{}

func (badAritySink) Consume() {}

func TestSinkErrorsOnWrongConsumeSignature(t *testing.T) {
	src := SliceSource([]string{"x"})
	_, err := Chain(src).Sink(badAritySink{})
	if !errors.Is(err, ErrNotSink) {
		t.Errorf("got %v, want ErrNotSink", err)
	}
}

type intSink struct{}

func (intSink) Consume(_ context.Context, in Stream[int]) (int, error) {
	n := 0
	err := rill.ForEach(in.Chan(), 1, func(int) error { n++; return nil })
	return n, err
}

func TestSinkErrorsOnTypeMismatch(t *testing.T) {
	src := SliceSource([]string{"x"})
	_, err := Chain(src).Sink(intSink{})
	if !errors.Is(err, ErrSinkTypeMismatch) {
		t.Errorf("got %v, want ErrSinkTypeMismatch", err)
	}
}

func TestSinkReturnsResultAndNilError(t *testing.T) {
	src := SliceSource([]int{1, 2, 3})
	res, err := Chain(src).Sink(intSink{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.(int) != 3 {
		t.Errorf("res = %v, want 3", res)
	}
}

type failingSink struct{ err error }

func (s failingSink) Consume(_ context.Context, in Stream[string]) (int, error) {
	for {
		if _, ok := <-in.Chan(); !ok {
			break
		}
	}
	return 0, s.err
}

func TestSinkReturnsSinkError(t *testing.T) {
	wantErr := errors.New("sink exploded")
	src := SliceSource([]string{"x"})
	_, err := Chain(src).Sink(failingSink{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestRunWiresSourceCommandsSink(t *testing.T) {
	src := SliceSource([]string{"3", "4"})
	parse := mapCmd(strconv.Atoi)
	res, err := Run(src, intSink{}, parse)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.(int) != 2 {
		t.Errorf("res = %v, want 2", res)
	}
}

// Run surfaces a validation error from any stage as its error return, not a panic.
func TestRunReturnsValidationError(t *testing.T) {
	src := SliceSource([]string{"x"})
	_, err := Run(src, intSink{}, struct{}{})
	if !errors.Is(err, ErrNotCommand) {
		t.Errorf("got %v, want ErrNotCommand", err)
	}
}

func TestRunContextHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := &ctxCheckSource{}
	_, err := RunContext(ctx, src, intSink{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

// ctxCheckSource emits ctx.Err() if cancelled, else a value.
type ctxCheckSource struct{}

func (s *ctxCheckSource) Stream(ctx context.Context) Stream[int] {
	return Generate(ctx, func(ctx context.Context, send func(int) bool, sendErr func(error)) {
		if err := ctx.Err(); err != nil {
			sendErr(err)
			return
		}
		if !send(1) {
			return
		}
	})
}

func TestCollectEmptyStream(t *testing.T) {
	src := SliceSource([]string{})
	res, err := Chain(src).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := res.([]string); len(got) != 0 {
		t.Errorf("got %v, want empty slice", got)
	}
}

func TestForEachErrorsOnNonFunc(t *testing.T) {
	src := SliceSource([]string{"x"})
	err := Chain(src).ForEach("not a func")
	if !errors.Is(err, ErrNotForEachFunc) {
		t.Errorf("got %v, want ErrNotForEachFunc", err)
	}
}

func TestForEachErrorsOnTypeMismatch(t *testing.T) {
	src := SliceSource([]string{"x"})
	err := Chain(src).ForEach(func(int) error { return nil })
	if !errors.Is(err, ErrStageTypeMismatch) {
		t.Errorf("got %v, want ErrStageTypeMismatch", err)
	}
}

// ForEach surfaces an earlier build error too, like the other terminals.
func TestForEachReturnsBuildError(t *testing.T) {
	err := Chain(struct{}{}).ForEach(func(int) error { return nil })
	if !errors.Is(err, ErrNotSource) {
		t.Errorf("got %v, want ErrNotSource", err)
	}
}

func TestTerminalAfterTerminalErrors(t *testing.T) {
	src := SliceSource([]string{"x"})
	p := Chain(src)
	if _, err := p.Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := p.Collect(); !errors.Is(err, ErrPipelineConsumed) {
		t.Errorf("got %v, want ErrPipelineConsumed", err)
	}
}

// Recording a stage after the pipeline is consumed sets the consumed error,
// which the next terminal surfaces.
func TestToAfterTerminalErrors(t *testing.T) {
	src := SliceSource([]string{"x"})
	p := Chain(src)
	if _, err := p.Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pass := mapCmd(func(s string) (string, error) { return s, nil })
	if _, err := p.To(pass).Collect(); !errors.Is(err, ErrPipelineConsumed) {
		t.Errorf("got %v, want ErrPipelineConsumed", err)
	}
}

// tryValueType extracts T from a validated Stream[T] channel.
func TestTryValueTypeReturnsElementType(t *testing.T) {
	ch := make(chan rill.Try[string])
	if got := tryValueType(reflect.ValueOf(ch)); got != reflect.TypeFor[string]() {
		t.Errorf("got %v, want string", got)
	}
}

// --- Collect / ForEach value and error paths ---

func TestCollectReturnsValues(t *testing.T) {
	got, err := Chain(SliceSource([]int{1, 2, 3})).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v := got.([]int); len(v) != 3 || v[0] != 1 || v[2] != 3 {
		t.Errorf("got %v, want [1 2 3]", v)
	}
}

func TestCollectReturnsStreamError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := ChainContext(ctx, &ctxCheckSource{}).Collect()
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
	if got != nil {
		t.Errorf("result should be nil on error, got %v", got)
	}
}

func TestForEachAppliesFnToEachValue(t *testing.T) {
	sum := 0
	if err := Chain(SliceSource([]int{1, 2, 3})).ForEach(func(n int) error { sum += n; return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sum != 6 {
		t.Errorf("sum = %d, want 6", sum)
	}
}

func TestForEachReturnsFnError(t *testing.T) {
	wantErr := errors.New("boom")
	err := Chain(SliceSource([]string{"a", "b"})).ForEach(func(string) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestForEachReturnsStreamError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ChainContext(ctx, &ctxCheckSource{}).ForEach(func(int) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

// badStreamArity has a Stream method with the wrong arity, exercising
// ChainContext's signature validation.
type badStreamArity struct{}

func (badStreamArity) Stream(_ context.Context, _ int) Stream[int] { return StreamOf[int]() }

// badStreamCtx has the right arity but a non-context first parameter, which must
// be rejected as a non-Source rather than failing later in a reflect.Call.
type badStreamCtx struct{}

func (badStreamCtx) Stream(_ int) Stream[int] { return StreamOf[int]() }

// assertNotSource builds a chain from an invalid source and asserts the terminal
// reports ErrNotSource — a value, never a panic.
func assertNotSource(t *testing.T, source any) {
	t.Helper()
	if _, err := Chain(source).Collect(); !errors.Is(err, ErrNotSource) {
		t.Fatalf("got %v, want ErrNotSource", err)
	}
}

func TestChain_RejectsBadSourceSignature(t *testing.T) { assertNotSource(t, badStreamArity{}) }

func TestChain_RejectsNonContextStreamParam(t *testing.T) { assertNotSource(t, badStreamCtx{}) }

func TestChain_RejectsNonSource(t *testing.T) { assertNotSource(t, struct{}{}) }

// --- Nil and non-Stream misuse: errors, never panics ---

func TestChainErrorsOnNilSource(t *testing.T) {
	if _, err := Chain(nil).Collect(); !errors.Is(err, ErrNotSource) {
		t.Errorf("got %v, want ErrNotSource", err)
	}
}

// nilPtrSource exists to build a typed-nil source value: a method set on a
// pointer receiver whose receiver is nil at call time.
type nilPtrSource struct{}

func (*nilPtrSource) Stream(context.Context) Stream[int] { return StreamOf[int]() }

func TestChainErrorsOnTypedNilSource(t *testing.T) {
	var src *nilPtrSource
	if _, err := Chain(src).Collect(); !errors.Is(err, ErrNotSource) {
		t.Errorf("got %v, want ErrNotSource", err)
	}
}

func TestToErrorsOnNilCommand(t *testing.T) {
	src := SliceSource([]string{"x"})
	if _, err := Chain(src).To(nil).Collect(); !errors.Is(err, ErrNotCommand) {
		t.Errorf("got %v, want ErrNotCommand", err)
	}
}

func TestSinkErrorsOnNilSink(t *testing.T) {
	src := SliceSource([]string{"x"})
	if _, err := Chain(src).Sink(nil); !errors.Is(err, ErrNotSink) {
		t.Errorf("got %v, want ErrNotSink", err)
	}
}

func TestForEachErrorsOnNilFunc(t *testing.T) {
	src := SliceSource([]string{"x"})
	if err := Chain(src).ForEach(nil); !errors.Is(err, ErrNotForEachFunc) {
		t.Errorf("got %v, want ErrNotForEachFunc", err)
	}
}

// nonStreamSource has the right Stream arity but does not return a Stream[T];
// it must be rejected as a non-Source instead of panicking at a terminal.
type nonStreamSource struct{}

func (nonStreamSource) Stream(context.Context) int { return 42 }

func TestChainErrorsOnNonStreamSourceReturn(t *testing.T) {
	if _, err := Chain(nonStreamSource{}).Collect(); !errors.Is(err, ErrNotSource) {
		t.Errorf("Collect: got %v, want ErrNotSource", err)
	}
	if err := Chain(nonStreamSource{}).ForEach(func(int) error { return nil }); !errors.Is(err, ErrNotSource) {
		t.Errorf("ForEach: got %v, want ErrNotSource", err)
	}
}

// nonStreamCommand consumes a Stream but does not produce one; it must be
// rejected as a non-Command.
type nonStreamCommand struct{}

func (nonStreamCommand) Execute(_ context.Context, _ Stream[string]) int { return 0 }

func TestToErrorsOnNonStreamCommandReturn(t *testing.T) {
	src := SliceSource([]string{"x"})
	if _, err := Chain(src).To(nonStreamCommand{}).Collect(); !errors.Is(err, ErrNotCommand) {
		t.Errorf("got %v, want ErrNotCommand", err)
	}
}

// A nil context defaults to context.Background instead of panicking inside a
// reflected call, matching the tolerance callers expect from Run-style APIs.
func TestNilContextDefaultsToBackground(t *testing.T) {
	src := SliceSource([]string{"a", "b"})
	var nilCtx context.Context // deliberately nil — the tolerance under test
	got, err := ChainContext(nilCtx, src).Collect()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lines := got.([]string); len(lines) != 2 {
		t.Errorf("got %v, want 2 items", lines)
	}
}

// ctxRecordingCmd captures the context its Execute receives, so a test can
// observe which context a stage actually ran under.
type ctxRecordingCmd struct{ got *context.Context }

func (c ctxRecordingCmd) Execute(ctx context.Context, in Stream[string]) Stream[string] {
	*c.got = ctx
	return in
}

// ctxRecordingSink captures the context its Consume receives.
type ctxRecordingSink struct{ got *context.Context }

func (s ctxRecordingSink) Consume(ctx context.Context, in Stream[string]) (int, error) {
	*s.got = ctx
	items, err := in.Collect() // fully consume
	return len(items), err
}

type ctxKey struct{}

// ToContext must run its stage under the EXPLICIT context, not the chain's.
func TestToContextOverridesStageContext(t *testing.T) {
	chainCtx := context.WithValue(context.Background(), ctxKey{}, "chain")
	stageCtx := context.WithValue(context.Background(), ctxKey{}, "stage")
	var got context.Context
	src := SliceSource([]string{"x"})
	if _, err := ChainContext(chainCtx, src).ToContext(stageCtx, ctxRecordingCmd{got: &got}).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value(ctxKey{}) != "stage" {
		t.Errorf("stage ran under %v context, want the ToContext override", got.Value(ctxKey{}))
	}
}

// SinkContext must consume under the EXPLICIT context, not the chain's.
func TestSinkContextOverridesConsumeContext(t *testing.T) {
	chainCtx := context.WithValue(context.Background(), ctxKey{}, "chain")
	sinkCtx := context.WithValue(context.Background(), ctxKey{}, "sink")
	var got context.Context
	src := SliceSource([]string{"x"})
	if _, err := ChainContext(chainCtx, src).SinkContext(sinkCtx, ctxRecordingSink{got: &got}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value(ctxKey{}) != "sink" {
		t.Errorf("sink ran under %v context, want the SinkContext override", got.Value(ctxKey{}))
	}
}

// A nil context passed to ToContext falls back to the chain's context.
func TestToContextNilFallsBackToChainContext(t *testing.T) {
	chainCtx := context.WithValue(context.Background(), ctxKey{}, "chain")
	var got context.Context
	src := SliceSource([]string{"x"})
	var nilCtx context.Context // deliberately nil — the fallback under test
	if _, err := ChainContext(chainCtx, src).ToContext(nilCtx, ctxRecordingCmd{got: &got}).Collect(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value(ctxKey{}) != "chain" {
		t.Errorf("stage ran under %v context, want the chain's", got.Value(ctxKey{}))
	}
}

// A nil context passed to SinkContext falls back to the chain's context.
func TestSinkContextNilFallsBackToChainContext(t *testing.T) {
	chainCtx := context.WithValue(context.Background(), ctxKey{}, "chain")
	var got context.Context
	src := SliceSource([]string{"x"})
	var nilCtx context.Context // deliberately nil — the fallback under test
	if _, err := ChainContext(chainCtx, src).SinkContext(nilCtx, ctxRecordingSink{got: &got}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value(ctxKey{}) != "chain" {
		t.Errorf("sink ran under %v context, want the chain's", got.Value(ctxKey{}))
	}
}
