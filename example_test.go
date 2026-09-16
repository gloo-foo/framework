package gloo_test

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/afero"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

func ExampleRun() {
	src := gloo.SliceSource([]string{"hello", "world"})

	upper := patterns.Map(func(line string) (string, error) {
		return strings.ToUpper(line), nil
	})

	_, _ = gloo.Run(src, gloo.WriteTo(os.Stdout), upper)
	// Output:
	// HELLO
	// WORLD
}

// ExamplePump wires a composed command to stdin/stdout as a Unix filter —
// the bridge from a gloo pipeline to a func main. Nothing is shelled out.
func ExamplePump() {
	// grep error | head -2, reading "stdin" and writing "stdout".
	cmd := gloo.Compose(
		patterns.Filter(func(line string) (bool, error) {
			return strings.Contains(line, "error"), nil
		}),
	).To(patterns.Head[string](2))

	stdin := strings.NewReader("error: a\ninfo: b\nerror: c\nerror: d\n")
	_, _ = gloo.Pump(context.Background(), cmd, stdin, os.Stdout)
	// Output:
	// error: a
	// error: c
}

func ExampleChain() {
	src := gloo.SliceSource([]string{"apple red", "banana yellow", "cherry red"})

	grep := patterns.Filter(func(line string) (bool, error) {
		return strings.Contains(line, "red"), nil
	})

	upper := patterns.Map(func(line string) (string, error) {
		return strings.ToUpper(line), nil
	})

	result, _ := gloo.Chain(src).
		To(grep).
		To(upper).
		Collect()

	for _, item := range result.([]string) {
		fmt.Println(item)
	}
	// Output:
	// APPLE RED
	// CHERRY RED
}

func ExampleChain_forEach() {
	src := gloo.SliceSource([]string{"one", "two", "three"})

	_ = gloo.Chain(src).ForEach(func(line string) error {
		fmt.Println(line)
		return nil
	})
	// Output:
	// one
	// two
	// three
}

func ExampleChain_sink() {
	src := gloo.SliceSource([]string{"hello", "world"})

	upper := patterns.Map(func(line string) (string, error) {
		return strings.ToUpper(line), nil
	})

	count, _ := gloo.Chain(src).To(upper).Sink(gloo.WriteTo(os.Stdout))
	fmt.Printf("%d lines written\n", count)
	// Output:
	// HELLO
	// WORLD
	// 2 lines written
}

func ExampleCompose() {
	src := gloo.SliceSource([]string{"hello", "world"})

	addBang := patterns.Map(func(line string) (string, error) {
		return line + "!", nil
	})

	upper := patterns.Map(func(line string) (string, error) {
		return strings.ToUpper(line), nil
	})

	composed := gloo.Compose(addBang).To(upper)

	result, _ := gloo.Chain(src).To(composed).Collect()
	for _, item := range result.([]string) {
		fmt.Println(item)
	}
	// Output:
	// HELLO!
	// WORLD!
}

func ExamplePipe() {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"apple red", "banana yellow", "cherry red"})

	grep := patterns.Filter(func(line string) (bool, error) {
		return strings.Contains(line, "red"), nil
	})

	upper := patterns.Map(func(line string) (string, error) {
		return strings.ToUpper(line), nil
	})

	composed := gloo.Pipe(grep, upper)
	stream := gloo.From(ctx, src, composed)
	results, _ := stream.Collect()
	for _, r := range results {
		fmt.Println(r)
	}
	// Output:
	// APPLE RED
	// CHERRY RED
}

func ExampleFileSource() {
	fs := afero.NewMemMapFs()
	_ = afero.WriteFile(fs, "test.txt", []byte("line one\nline two\nline three"), 0o644)

	ctx := context.Background()
	src := gloo.FileSource(fs, []gloo.File{"test.txt"})
	results, _ := src.Stream(ctx).Collect()
	for _, r := range results {
		fmt.Println(r)
	}
	// Output:
	// line one
	// line two
	// line three
}

func ExampleSliceSource() {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"alpha", "bravo", "charlie"})

	results, _ := src.Stream(ctx).Collect()
	for _, r := range results {
		fmt.Println(r)
	}
	// Output:
	// alpha
	// bravo
	// charlie
}
