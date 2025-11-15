package gloo_test

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/build"
)

// ============================================================================
// Test 1: Basic String Processing (Unix-style)
// ============================================================================

func TestChannelLineTransform_Grep(t *testing.T) {
	// Create a grep-like filter
	grep := build.ChannelLineTransform(func(line string) (string, bool) {
		return line, strings.Contains(line, "ERROR")
	})

	// Test with sample input
	input := strings.NewReader("INFO: starting\nERROR: failed\nINFO: continuing\nERROR: crashed\n")
	output := &bytes.Buffer{}

	ctx := context.Background()
	cmd := gloo.Pipeline(grep.ChannelExecutor())
	err := cmd.Executor()(ctx, input, output, nil)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := "ERROR: failed\nERROR: crashed\n"
	if output.String() != expected {
		t.Errorf("Expected %q, got %q", expected, output.String())
	}
}

func TestChannelStatefulLineTransform_Head(t *testing.T) {
	// Create a head-like command that takes first 3 lines
	head := build.ChannelStatefulLineTransform(func(lineNum int64, line string) (string, bool) {
		return line, lineNum <= 3
	})

	input := strings.NewReader("line1\nline2\nline3\nline4\nline5\n")
	output := &bytes.Buffer{}

	ctx := context.Background()
	cmd := gloo.Pipeline(head.ChannelExecutor())
	err := cmd.Executor()(ctx, input, output, nil)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := "line1\nline2\nline3\n"
	if output.String() != expected {
		t.Errorf("Expected %q, got %q", expected, output.String())
	}
}

func TestChannelAccumulateAndProcess_Sort(t *testing.T) {
	// Create a sort command
	sortCmd := build.ChannelAccumulateAndProcess(func(lines []string) []string {
		sort.Strings(lines)
		return lines
	})

	input := strings.NewReader("zebra\napple\nmango\nbanana\n")
	output := &bytes.Buffer{}

	ctx := context.Background()
	cmd := gloo.Pipeline(sortCmd.ChannelExecutor())
	err := cmd.Executor()(ctx, input, output, nil)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := "apple\nbanana\nmango\nzebra\n"
	if output.String() != expected {
		t.Errorf("Expected %q, got %q", expected, output.String())
	}
}

func TestChannelAccumulateAndOutput_WordCount(t *testing.T) {
	// Create a word count command
	wc := build.ChannelAccumulateAndOutput(func(lines []string, out chan<- gloo.Row[string]) error {
		wordCount := 0
		for _, line := range lines {
			wordCount += len(strings.Fields(line))
		}
		out <- gloo.Row[string]{Data: fmt.Sprintf("%d words", wordCount)}
		return nil
	})

	input := strings.NewReader("hello world\nfoo bar baz\ntest\n")
	output := &bytes.Buffer{}

	ctx := context.Background()
	cmd := gloo.Pipeline(wc.ChannelExecutor())
	err := cmd.Executor()(ctx, input, output, nil)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := "6 words\n"
	if output.String() != expected {
		t.Errorf("Expected %q, got %q", expected, output.String())
	}
}

// ============================================================================
// Test 2: Pipeline Composition
// ============================================================================

func TestPipe_GrepAndSort(t *testing.T) {
	// Create pipeline: grep ERROR | sort
	grep := build.ChannelLineTransform(func(line string) (string, bool) {
		return line, strings.Contains(line, "ERROR")
	}).ChannelExecutor()

	sortCmd := build.ChannelAccumulateAndProcess(func(lines []string) []string {
		sort.Strings(lines)
		return lines
	}).ChannelExecutor()

	pipeline := gloo.Pipe(grep, sortCmd)

	input := strings.NewReader("ERROR: zebra\nINFO: apple\nERROR: banana\nERROR: apple\n")
	output := &bytes.Buffer{}

	ctx := context.Background()
	cmd := gloo.Pipeline(pipeline)
	err := cmd.Executor()(ctx, input, output, nil)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := "ERROR: apple\nERROR: banana\nERROR: zebra\n"
	if output.String() != expected {
		t.Errorf("Expected %q, got %q", expected, output.String())
	}
}

func TestPipe_ComplexPipeline(t *testing.T) {
	// Create pipeline: grep ERROR | sort | head -2
	grep := build.ChannelLineTransform(func(line string) (string, bool) {
		return line, strings.Contains(line, "ERROR")
	}).ChannelExecutor()

	sortCmd := build.ChannelAccumulateAndProcess(func(lines []string) []string {
		sort.Strings(lines)
		return lines
	}).ChannelExecutor()

	head := build.ChannelStatefulLineTransform(func(lineNum int64, line string) (string, bool) {
		return line, lineNum <= 2
	}).ChannelExecutor()

	pipeline := gloo.Pipe(grep, sortCmd, head)

	input := strings.NewReader("ERROR: c\nINFO: x\nERROR: a\nERROR: b\nERROR: d\n")
	output := &bytes.Buffer{}

	ctx := context.Background()
	cmd := gloo.Pipeline(pipeline)
	err := cmd.Executor()(ctx, input, output, nil)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := "ERROR: a\nERROR: b\n"
	if output.String() != expected {
		t.Errorf("Expected %q, got %q", expected, output.String())
	}
}

// ============================================================================
// Test 3: Custom Types
// ============================================================================

type Person struct {
	Name string
	Age  int
}

func TestChannelLineTransform_CustomType(t *testing.T) {
	// Filter adults (age >= 18)
	filterAdults := build.ChannelLineTransform(func(p Person) (Person, bool) {
		return p, p.Age >= 18
	})

	// Create test data
	input := make(chan gloo.Row[Person], 10)
	output := make(chan gloo.Row[Person], 10)

	input <- gloo.Row[Person]{Data: Person{"Alice", 25}}
	input <- gloo.Row[Person]{Data: Person{"Bob", 15}}
	input <- gloo.Row[Person]{Data: Person{"Charlie", 30}}
	input <- gloo.Row[Person]{Data: Person{"David", 10}}
	close(input)

	ctx := context.Background()
	executor := filterAdults.ChannelExecutor()
	err := executor(ctx, input, output)
	close(output)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Collect results
	var results []Person
	for row := range output {
		if row.Err != nil {
			t.Fatalf("Unexpected error in row: %v", row.Err)
		}
		results = append(results, row.Data)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}

	if results[0].Name != "Alice" || results[1].Name != "Charlie" {
		t.Errorf("Unexpected results: %+v", results)
	}
}

func TestChannelTransform_TypeConversion(t *testing.T) {
	// Convert string to Person
	parser := build.ChannelTransform[string, Person](func(line string) (Person, bool, error) {
		parts := strings.Split(line, ",")
		if len(parts) != 2 {
			return Person{}, false, nil
		}
		var age int
		fmt.Sscanf(parts[1], "%d", &age)
		return Person{Name: parts[0], Age: age}, true, nil
	})

	// Create test data
	input := make(chan gloo.Row[string], 10)
	output := make(chan gloo.Row[Person], 10)

	input <- gloo.Row[string]{Data: "Alice,25"}
	input <- gloo.Row[string]{Data: "Bob,30"}
	input <- gloo.Row[string]{Data: "Invalid"}
	close(input)

	ctx := context.Background()
	err := parser(ctx, input, output)
	close(output)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Collect results
	var results []Person
	for row := range output {
		if row.Err != nil {
			t.Fatalf("Unexpected error in row: %v", row.Err)
		}
		results = append(results, row.Data)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}

	if results[0].Name != "Alice" || results[0].Age != 25 {
		t.Errorf("Unexpected first result: %+v", results[0])
	}
}

// ============================================================================
// Test 4: Context Cancellation
// ============================================================================

func TestChannelExecutor_ContextCancellation(t *testing.T) {
	// Create a slow processor
	slow := build.ChannelLineTransform(func(line string) (string, bool) {
		time.Sleep(100 * time.Millisecond)
		return line, true
	})

	// Create test data with many items
	input := make(chan gloo.Row[string], 100)
	for i := 0; i < 100; i++ {
		input <- gloo.Row[string]{Data: fmt.Sprintf("line %d", i)}
	}
	close(input)

	output := make(chan gloo.Row[string], 100)

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	executor := slow.ChannelExecutor()
	err := executor(ctx, input, output)

	if err != context.DeadlineExceeded {
		t.Errorf("Expected DeadlineExceeded, got %v", err)
	}
}

// ============================================================================
// Test 5: Error Handling
// ============================================================================

func TestChannelExecutor_ErrorPropagation(t *testing.T) {
	// Create a transform that generates an error
	errorGen := build.ChannelLineTransform(func(line string) (string, bool) {
		if line == "ERROR" {
			// We can't return an error directly from LineTransform,
			// so we'll test this with a custom executor
			return line, true
		}
		return line, true
	})

	// Create test data with error row
	input := make(chan gloo.Row[string], 10)
	input <- gloo.Row[string]{Data: "line1"}
	input <- gloo.Row[string]{Err: fmt.Errorf("test error")}
	input <- gloo.Row[string]{Data: "line3"}
	close(input)

	output := make(chan gloo.Row[string], 10)

	ctx := context.Background()
	executor := errorGen.ChannelExecutor()
	err := executor(ctx, input, output)

	// Should propagate the error
	if err == nil || err.Error() != "test error" {
		t.Errorf("Expected 'test error', got %v", err)
	}
}

// ============================================================================
// Test 6: Parallel Processing
// ============================================================================

func TestParallelMap_Concurrency(t *testing.T) {
	processed := make(chan int, 100)

	// Create a transform that records which worker processed it
	transform := gloo.ParallelMap(4, func(line string) (string, bool, error) {
		// Simulate work
		time.Sleep(10 * time.Millisecond)
		processed <- 1
		return strings.ToUpper(line), true, nil
	})

	input := strings.NewReader("a\nb\nc\nd\ne\nf\ng\nh\n")
	output := &bytes.Buffer{}

	ctx := context.Background()
	cmd := gloo.Pipeline(transform)

	start := time.Now()
	err := cmd.Executor()(ctx, input, output, nil)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// With 8 items and 4 workers, should take ~20ms (2 batches)
	// Without parallelism, would take ~80ms (8 sequential)
	if duration > 50*time.Millisecond {
		t.Logf("Warning: Processing took %v, may not be parallel", duration)
	}

	// Note: Output order may vary with parallel processing
	// So we just check all lines are present
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 8 {
		t.Errorf("Expected 8 lines, got %d", len(lines))
	}
}

// ============================================================================
// Test 7: Batch Processing
// ============================================================================

func TestBatch_GroupsCorrectly(t *testing.T) {
	batchSizes := []int{}

	// Create a batch processor that records batch sizes
	batcher := gloo.Batch[string](3, func(batch []string) ([]string, error) {
		batchSizes = append(batchSizes, len(batch))
		return batch, nil
	})

	input := strings.NewReader("1\n2\n3\n4\n5\n6\n7\n")
	output := &bytes.Buffer{}

	ctx := context.Background()
	cmd := gloo.Pipeline(batcher)
	err := cmd.Executor()(ctx, input, output, nil)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	// Should have 3 batches: [3, 3, 1]
	if len(batchSizes) != 3 {
		t.Errorf("Expected 3 batches, got %d: %v", len(batchSizes), batchSizes)
	}

	if batchSizes[0] != 3 || batchSizes[1] != 3 || batchSizes[2] != 1 {
		t.Errorf("Expected batch sizes [3, 3, 1], got %v", batchSizes)
	}
}

// ============================================================================
// Test 8: Byte Processing
// ============================================================================

func TestByteProcessing(t *testing.T) {
	// Test byte channel processing with a simple pass-through
	passthrough := build.ChannelLineTransform(func(data []byte) ([]byte, bool) {
		return data, true
	})

	input := strings.NewReader("line1\nline2\nline3\n")
	output := &bytes.Buffer{}

	ctx := context.Background()
	cmd := gloo.Pipeline(passthrough.ChannelExecutor())
	err := cmd.Executor()(ctx, input, output, nil)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	expected := "line1\nline2\nline3\n"
	if output.String() != expected {
		t.Errorf("Expected %q, got %q", expected, output.String())
	}
}

// ============================================================================
// Test 9: Complete Command Integration
// ============================================================================

type testCommand struct {
	pattern string
	inputs  gloo.Inputs[gloo.File, struct{}]
}

func (c testCommand) Executor() gloo.CommandExecutor {
	return c.inputs.WrapChannelString(
		build.ChannelLineTransform(func(line string) (string, bool) {
			return line, strings.Contains(line, c.pattern)
		}).ChannelExecutor(),
	)
}

func TestCommand_ChannelIntegration(t *testing.T) {
	// Create a command that uses channels internally
	cmd := testCommand{
		pattern: "keep",
		inputs:  gloo.Inputs[gloo.File, struct{}]{},
	}

	input := strings.NewReader("keep this\nremove this\nkeep that\n")
	output := &bytes.Buffer{}

	ctx := context.Background()
	executor := cmd.Executor()
	err := executor(ctx, input, output, nil)

	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	expected := "keep this\nkeep that\n"
	if output.String() != expected {
		t.Errorf("Expected %q, got %q", expected, output.String())
	}
}
