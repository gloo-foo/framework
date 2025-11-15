package gloo_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/build"
)

// ============================================================================
// Test: RowParser Interface
// ============================================================================

type ParsedLog struct {
	Level   string
	Message string
}

// ParseRow implements gloo.RowParser
func (e *ParsedLog) ParseRow(line string) error {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid log format: %q", line)
	}
	e.Level = strings.TrimSpace(parts[0])
	e.Message = strings.TrimSpace(parts[1])
	return nil
}

func TestRowParser_Interface(t *testing.T) {
	input := strings.NewReader("ERROR: Something went wrong\nINFO: All good\nWARN: Be careful\n")
	ch := make(chan gloo.Row[ParsedLog], 100)

	ctx := context.Background()
	go gloo.ReaderToChannelParsed(ctx, input, ch)

	var results []ParsedLog
	for row := range ch {
		if row.Err != nil {
			t.Fatalf("Unexpected error: %v", row.Err)
		}
		results = append(results, row.Data)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	if results[0].Level != "ERROR" || results[0].Message != "Something went wrong" {
		t.Errorf("Unexpected first result: %+v", results[0])
	}
}

func TestRowParser_WithPipeline(t *testing.T) {
	// Create a pipeline that reads ParsedLog, filters by level, and formats
	filterErrors := build.ChannelLineTransform(func(entry ParsedLog) (ParsedLog, bool) {
		return entry, entry.Level == "ERROR"
	})

	// Manually create input channel with parsed data
	input := strings.NewReader("ERROR: Bad thing\nINFO: Good thing\nERROR: Another bad\n")
	inputCh := make(chan gloo.Row[ParsedLog], 100)
	outputCh := make(chan gloo.Row[ParsedLog], 100)

	ctx := context.Background()

	// Start parser
	go gloo.ReaderToChannelParsed(ctx, input, inputCh)

	// Start filter (it will close outputCh when done)
	go func() {
		filterErrors.ChannelExecutor()(ctx, inputCh, outputCh)
		close(outputCh)
	}()

	// Collect results
	var results []ParsedLog
	for row := range outputCh {
		if row.Err != nil {
			t.Fatalf("Unexpected error: %v", row.Err)
		}
		results = append(results, row.Data)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 ERROR entries, got %d", len(results))
	}
}

// ============================================================================
// Test: ReaderToChannelWithParser
// ============================================================================

type CSVRecord struct {
	Name  string
	Value int
}

func TestReaderToChannelWithParser(t *testing.T) {
	input := strings.NewReader("Alice,100\nBob,200\nCharlie,300\n")
	ch := make(chan gloo.Row[CSVRecord], 100)

	parser := func(line string) (CSVRecord, error) {
		parts := strings.Split(line, ",")
		if len(parts) != 2 {
			return CSVRecord{}, fmt.Errorf("invalid CSV: %q", line)
		}
		var value int
		fmt.Sscanf(parts[1], "%d", &value)
		return CSVRecord{Name: parts[0], Value: value}, nil
	}

	ctx := context.Background()
	go gloo.ReaderToChannelWithParser(ctx, input, ch, parser)

	var results []CSVRecord
	for row := range ch {
		if row.Err != nil {
			t.Fatalf("Unexpected error: %v", row.Err)
		}
		results = append(results, row.Data)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	if results[0].Name != "Alice" || results[0].Value != 100 {
		t.Errorf("Unexpected first result: %+v", results[0])
	}
}

// ============================================================================
// Test: encoding.TextUnmarshaler
// ============================================================================

type Temperature float64

// UnmarshalText implements encoding.TextUnmarshaler
func (t *Temperature) UnmarshalText(text []byte) error {
	var f float64
	_, err := fmt.Sscanf(string(text), "%f", &f)
	if err != nil {
		return err
	}
	*t = Temperature(f)
	return nil
}

func TestTextUnmarshaler_Interface(t *testing.T) {
	input := strings.NewReader("98.6\n100.4\n99.1\n")
	ch := make(chan gloo.Row[Temperature], 100)

	ctx := context.Background()
	go gloo.ReaderToChannelParsed(ctx, input, ch)

	var results []Temperature
	for row := range ch {
		if row.Err != nil {
			t.Fatalf("Unexpected error: %v", row.Err)
		}
		results = append(results, row.Data)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	if results[0] != Temperature(98.6) {
		t.Errorf("Expected 98.6, got %v", results[0])
	}
}

// ============================================================================
// Test: Automatic parsing with Pipeline
// ============================================================================

func TestAutomatic_Parsing_WithCommand(t *testing.T) {
	// Create a command that uses ParsedLog with automatic parsing
	type logFilterCommand struct {
		level  string
		inputs gloo.Inputs[gloo.File, struct{}]
	}

	cmd := logFilterCommand{
		level:  "ERROR",
		inputs: gloo.Inputs[gloo.File, struct{}]{},
	}

	// For this to work automatically, we'd need to enhance WrapChannel to support parsers
	// For now, this demonstrates the concept

	input := strings.NewReader("ERROR: Bad\nINFO: Good\nERROR: Worse\n")
	output := &bytes.Buffer{}

	// Manual pipeline with parsing
	inputCh := make(chan gloo.Row[ParsedLog], 100)
	outputCh := make(chan gloo.Row[ParsedLog], 100)
	doneCh := make(chan struct{})

	ctx := context.Background()

	// Parse input
	go gloo.ReaderToChannelParsed(ctx, input, inputCh)

	// Filter
	filter := build.ChannelLineTransform(func(entry ParsedLog) (ParsedLog, bool) {
		return entry, entry.Level == cmd.level
	})
	go func() {
		filter.ChannelExecutor()(ctx, inputCh, outputCh)
		close(outputCh)
		close(doneCh)
	}()

	// Collect output
	var count int
	for row := range outputCh {
		if row.Err != nil {
			t.Fatalf("Unexpected error: %v", row.Err)
		}
		count++
		fmt.Fprintf(output, "%s: %s\n", row.Data.Level, row.Data.Message)
	}
	<-doneCh

	if count != 2 {
		t.Errorf("Expected 2 ERROR entries, got %d", count)
	}

	expected := "ERROR: Bad\nERROR: Worse\n"
	if output.String() != expected {
		t.Errorf("Expected %q, got %q", expected, output.String())
	}
}

// ============================================================================
// Test: Parse errors are propagated
// ============================================================================

func TestRowParser_ParseErrors(t *testing.T) {
	input := strings.NewReader("VALID: Good\nINVALID\nVALID: Also good\n")
	ch := make(chan gloo.Row[ParsedLog], 100)

	ctx := context.Background()
	go gloo.ReaderToChannelParsed(ctx, input, ch)

	var successes, errors int
	for row := range ch {
		if row.Err != nil {
			errors++
		} else {
			successes++
		}
	}

	if successes != 2 {
		t.Errorf("Expected 2 successful parses, got %d", successes)
	}
	if errors != 1 {
		t.Errorf("Expected 1 parse error, got %d", errors)
	}
}

