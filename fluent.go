package gloo

import (
	"context"
	"reflect"
	"slices"
	"strings"
)

// Validation errors surfaced by the fluent builder. The reflection-based Chain
// API cannot enforce stage types at compile time (Go has no generic methods
// yet), so a mismatch is reported here as a value you can match with errors.Is —
// never a panic. The first error a builder hits is sticky: later .To() calls are
// no-ops and the terminal (.Sink/.Collect/.ForEach) returns it.
const (
	ErrNotSource         Error = "gloo: argument must implement Source (Stream(context.Context) Stream[Out])"
	ErrNotCommand        Error = "gloo: stage must implement Command (Execute(context.Context, Stream[In]) Stream[Out])"
	ErrNotSink           Error = "gloo: sink must implement Sink (Consume(context.Context, Stream[In]) (Res, error))"
	ErrStageTypeMismatch Error = "gloo: pipeline stage type mismatch"
	ErrSinkTypeMismatch  Error = "gloo: sink type mismatch"
	ErrNotForEachFunc    Error = "gloo: ForEach argument must be func(T) error"
	ErrPipelineConsumed  Error = "gloo: pipeline already consumed by a terminal operation"
)

// Chain starts a fluent pipeline from a source using context.Background().
// Use .To() to chain commands and .Sink()/.Collect()/.ForEach() to consume.
//
//	gloo.Chain(source).To(grepCmd).To(sortCmd).Sink(sink)
//
// For compile-time-checked composition (no reflection, no runtime type errors),
// prefer Pipe and Compose; Chain trades that safety for a fluent, type-changing
// syntax until Go ships generic methods.
func Chain(source any) FluentPipeline {
	return ChainContext(context.Background(), source)
}

// ChainContext starts a fluent pipeline from a source with an explicit context.
// The context is inherited by .To() stages. Use .ToContext() to override the
// context for individual stages. A nil context is treated as
// context.Background().
//
// Wiring is LAZY: the source and commands are validated for type compatibility
// at build time (by inspecting method signatures, without invoking them) but
// the stream is not created until a terminal operation runs. A chain that is
// built and never consumed therefore starts no goroutines and leaks nothing. A
// validation failure is recorded and returned by the terminal, never panicked.
func ChainContext(ctx context.Context, source any) FluentPipeline {
	if ctx == nil {
		ctx = context.Background()
	}
	p := FluentPipeline{ctx: ctx, source: source, state: &chainState{}}
	out, err := sourceOutType(source)
	if err != nil {
		p.err = err
		return p
	}
	p.curType = out
	return p
}

// Run executes a pipeline in one call: source → commands → sink.
// Uses context.Background().
//
//	gloo.Run(source, sink, cmd1, cmd2, cmd3)
func Run(source, sink any, cmds ...any) (any, error) {
	return RunContext(context.Background(), source, sink, cmds...)
}

// RunContext executes a pipeline in one call with an explicit context.
//
//	gloo.RunContext(ctx, source, sink, cmd1, cmd2, cmd3)
func RunContext(ctx context.Context, source, sink any, cmds ...any) (any, error) {
	p := ChainContext(ctx, source)
	for _, cmd := range cmds {
		p = p.To(cmd)
	}
	return p.Sink(sink)
}

// stage is one recorded command in a lazily-wired chain.
type stage struct {
	ctx context.Context
	cmd any
}

// FluentPipeline is a pipeline builder created by Chain or ChainContext. Each
// .To() records a command and validates its type against the running element
// type via reflection — without wiring any stream. Terminal operations
// (.Sink(), .Collect(), .ForEach()) build the stream inside their own scope and
// consume it.
//
// It is an immutable value: each .To() returns an UPDATED pipeline, so chain
// the calls (or reassign). All values derived from one Chain share a single
// consumed marker: once a terminal operation runs on any of them the pipeline
// is consumed and cannot be reused. Single-owner — do not share across
// goroutines. Any validation error is sticky and surfaced by the terminal.
type FluentPipeline struct {
	ctx     context.Context
	source  any
	curType reflect.Type
	err     error
	state   *chainState
	stages  []stage
}

// chainState is the consumed marker shared by every FluentPipeline value
// derived from one Chain, so a terminal on any derived value retires them all.
type chainState struct {
	isConsumed bool
}

// To records a Command into the chain, using the chain's context. The command
// must implement Command[In, Out] where In matches the current element type.
func (p FluentPipeline) To(cmd any) FluentPipeline {
	return p.ToContext(p.ctx, cmd)
}

// ToContext records a Command into the chain with an explicit context,
// overriding the chain's context for this stage only (nil means the chain's
// context). A type or shape mismatch is recorded as the pipeline's sticky
// error; it is not raised until a terminal.
func (p FluentPipeline) ToContext(ctx context.Context, cmd any) FluentPipeline {
	if ctx == nil {
		ctx = p.ctx
	}
	if p.err != nil {
		return p
	}
	if p.state.isConsumed {
		p.err = ErrPipelineConsumed
		return p
	}
	out, err := commandOutType(cmd, p.curType)
	if err != nil {
		p.err = err
		return p
	}
	p.stages = append(slices.Clip(p.stages), stage{ctx: ctx, cmd: cmd})
	p.curType = out
	return p
}

// consume marks the pipeline consumed, returning any sticky build error or the
// already-consumed error. Every terminal calls it first.
func (p FluentPipeline) consume() error {
	if p.err != nil {
		return p.err
	}
	if p.state.isConsumed {
		return ErrPipelineConsumed
	}
	p.state.isConsumed = true
	return nil
}

// build wires the source and recorded stages into a concrete stream. It is
// called once, by a terminal, only after validation has passed.
func (p FluentPipeline) build() reflect.Value {
	sv := reflect.ValueOf(p.source).
		MethodByName("Stream").
		Call([]reflect.Value{reflect.ValueOf(p.ctx)})[0]
	for _, st := range p.stages {
		sv = reflect.ValueOf(st.cmd).
			MethodByName("Execute").
			Call([]reflect.Value{reflect.ValueOf(st.ctx), sv})[0]
	}
	return sv
}

// Sink is a terminal operation that consumes the stream via a Sink, using the
// chain's context. Returns (Res, error) as (any, error).
func (p FluentPipeline) Sink(sink any) (any, error) {
	return p.SinkContext(p.ctx, sink)
}

// SinkContext is a terminal operation that consumes the stream via a Sink with
// an explicit context (nil means the chain's context).
func (p FluentPipeline) SinkContext(ctx context.Context, sink any) (any, error) {
	if ctx == nil {
		ctx = p.ctx
	}
	if err := p.consume(); err != nil {
		return nil, err
	}
	method, inType, err := sinkMethod(sink)
	if err != nil {
		return nil, err
	}
	if !p.curType.AssignableTo(inType) {
		return nil, mismatch(ErrSinkTypeMismatch, inType, p.curType)
	}

	streamVal := p.build()
	defer discardStream(streamVal) // tear down upstream if the sink returns early

	results := method.Call([]reflect.Value{reflect.ValueOf(ctx), streamVal})
	res := results[0].Interface()
	if errVal := results[1].Interface(); errVal != nil {
		return res, errVal.(error)
	}
	return res, nil
}

// Collect is a terminal operation that collects all stream items into a slice.
// Returns ([]T, error) as (any, error) where T is the final element type.
func (p FluentPipeline) Collect() (any, error) {
	if err := p.consume(); err != nil {
		return nil, err
	}
	streamVal := p.build()
	chVal := streamChan(streamVal)
	result := reflect.MakeSlice(reflect.SliceOf(tryValueType(chVal)), 0, 0)
	collect := func(value reflect.Value) error {
		result = reflect.Append(result, value)
		return nil
	}
	if err := drainReflected(streamVal, chVal, collect); err != nil {
		return nil, err
	}
	return result.Interface(), nil
}

// ForEach is a terminal operation that calls fn for each item in the stream.
// fn must be func(T) error where T matches the final element type.
func (p FluentPipeline) ForEach(fn any) error {
	if err := p.consume(); err != nil {
		return err
	}
	fnVal := reflect.ValueOf(fn)
	if !fnVal.IsValid() {
		return ErrNotForEachFunc
	}
	if err := requireForEachFunc(fnVal.Type(), elementType(p.curType)); err != nil {
		return err
	}
	streamVal := p.build()
	return drainReflected(streamVal, streamChan(streamVal), callReflected(fnVal))
}

// reflectVisitor consumes one reflected stream value (a Try's Value field).
type reflectVisitor func(value reflect.Value) error

// drainReflected receives each item from a reflected stream channel, passing
// successful values to visit. On the first error (stream or visit) it discards
// the upstream (stop + drain) so the producer goroutine can finish, then returns.
func drainReflected(streamVal, chVal reflect.Value, visit reflectVisitor) error {
	for {
		val, ok := chVal.Recv()
		if !ok {
			return nil
		}
		if err := visitReflected(val, visit); err != nil {
			discardStream(streamVal)
			return err
		}
	}
}

// visitReflected returns the item's stream error, or the result of visit.
func visitReflected(val reflect.Value, visit reflectVisitor) error {
	if errField := val.FieldByName("Error"); !errField.IsNil() {
		return errField.Interface().(error)
	}
	return visit(val.FieldByName("Value"))
}

// callReflected adapts a reflected func(T) error into a reflectVisitor.
func callReflected(fnVal reflect.Value) reflectVisitor {
	return func(value reflect.Value) error {
		return errorFromReflect(fnVal.Call([]reflect.Value{value})[0])
	}
}

// errorFromReflect converts a reflected error-interface value to error, or nil.
func errorFromReflect(v reflect.Value) error {
	if v.IsNil() {
		return nil
	}
	return v.Interface().(error)
}

// methodName names a reflected method the fluent builder looks up on a stage.
type methodName string

// methodOf returns v's method of the given name, reporting false when v cannot
// receive a method call at all: a nil interface, or a nil pointer (whose value
// methods would panic when the reflected call unwraps the receiver).
func methodOf(v any, name methodName) (reflect.Value, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() == reflect.Pointer && rv.IsNil()) {
		return reflect.Value{}, false
	}
	m := rv.MethodByName(string(name))
	return m, m.IsValid()
}

// isStreamType reports whether t is an instantiation of Stream[T]. The fluent
// builder requires it of every reflected Stream/Execute return so a terminal
// never calls Chan/Discard on something that is not a Stream — that would be a
// panic, which the builder's contract forbids.
func isStreamType(t reflect.Type) bool {
	return t.PkgPath() == streamPkgPath && strings.HasPrefix(t.Name(), "Stream[")
}

// sourceOutType validates that source implements Source and returns the element
// type its Stream method produces.
func sourceOutType(source any) (reflect.Type, error) {
	m, ok := methodOf(source, "Stream")
	if !ok {
		return nil, ErrNotSource
	}
	mt := m.Type()
	if mt.NumIn() != 1 || mt.NumOut() != 1 || mt.In(0) != contextType || !isStreamType(mt.Out(0)) {
		return nil, ErrNotSource
	}
	return mt.Out(0), nil
}

// commandOutType validates that cmd implements Command accepting in, and returns
// the element type it produces.
func commandOutType(cmd any, in reflect.Type) (reflect.Type, error) {
	m, ok := methodOf(cmd, "Execute")
	if !ok {
		return nil, ErrNotCommand
	}
	mt := m.Type()
	if mt.NumIn() != 2 || mt.NumOut() != 1 || mt.In(0) != contextType || !isStreamType(mt.Out(0)) {
		return nil, ErrNotCommand
	}
	if !in.AssignableTo(mt.In(1)) {
		return nil, mismatch(ErrStageTypeMismatch, mt.In(1), in)
	}
	return mt.Out(0), nil
}

// sinkMethod validates that sink implements Sink and returns its bound Consume
// method and the element type it accepts.
func sinkMethod(sink any) (reflect.Value, reflect.Type, error) {
	m, ok := methodOf(sink, "Consume")
	if !ok {
		return reflect.Value{}, nil, ErrNotSink
	}
	mt := m.Type()
	if mt.NumIn() != 2 || mt.NumOut() != 2 || mt.Out(1) != errorType || mt.In(0) != contextType {
		return reflect.Value{}, nil, ErrNotSink
	}
	return m, mt.In(1), nil
}

// requireForEachFunc returns nil iff fnType is func(T) error accepting elem.
func requireForEachFunc(fnType, elem reflect.Type) error {
	if !isForEachFunc(fnType) {
		return ErrNotForEachFunc
	}
	if !elem.AssignableTo(fnType.In(0)) {
		return mismatch(ErrStageTypeMismatch, fnType.In(0), elem)
	}
	return nil
}

func isForEachFunc(t reflect.Type) bool {
	return t.Kind() == reflect.Func && t.NumIn() == 1 && t.NumOut() == 1 && t.Out(0) == errorType
}

// mismatch wraps a type-mismatch sentinel with the expected and actual types.
func mismatch(sentinel Error, want, got reflect.Type) error {
	return sentinel.With(nil, "stage expects", want.String(), "but upstream produces", got.String())
}

// errorType is the reflect.Type of the error interface; contextType is the
// reflect.Type of context.Context; streamPkgPath locates Stream[T]
// instantiations. All are used to validate that reflected command/sink/source
// signatures have the expected shapes.
var (
	errorType     = reflect.TypeFor[error]()
	contextType   = reflect.TypeFor[context.Context]()
	streamPkgPath = reflect.TypeFor[Stream[int]]().PkgPath()
)

// streamChan returns the underlying channel of a Stream[T] reflect value by
// calling its Chan method. The caller has already validated, via the build
// pipeline, that streamVal is a Stream — so no shape check is needed here.
func streamChan(streamVal reflect.Value) reflect.Value {
	return streamVal.MethodByName("Chan").Call(nil)[0]
}

// discardStream calls Discard (stop + drain) on a Stream[T] reflect value, the
// safe way to abandon a stream when a terminal returns early.
func discardStream(streamVal reflect.Value) {
	if m := streamVal.MethodByName("Discard"); m.IsValid() {
		m.Call(nil)
	}
}

// elementType extracts the element type T from a Stream[T] reflect.Type by
// inspecting the return type of its Chan method (<-chan rill.Try[T]). streamType
// is a validated Stream type, so the introspection always succeeds.
func elementType(streamType reflect.Type) reflect.Type {
	m, _ := streamType.MethodByName("Chan")
	field, _ := m.Type.Out(0).Elem().FieldByName("Value") // rill.Try[T].Value
	return field.Type
}

// tryValueType returns the element type T of a Stream[T]'s channel (a channel of
// rill.Try[T]) given its reflect.Value.
func tryValueType(chVal reflect.Value) reflect.Type {
	field, _ := chVal.Type().Elem().FieldByName("Value")
	return field.Type
}
