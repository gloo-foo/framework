package verification

// Wave 1 pattern integration tests for []byte commands.
//
// Proves all 7 canonical patterns compose in multi-stage pipelines using
// []byte streams — matching how the actual cmd.* commands (rev, nl, grep,
// head, tac, wc, split) are built.

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"testing"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

// ---------------------------------------------------------------------------
// []byte command implementations — one per Wave 1 pattern
// ---------------------------------------------------------------------------

// rev: reverse each line character-by-character. Pattern: Map.
func revB() gloo.Command[[]byte, []byte] {
	return patterns.Map(func(line []byte) ([]byte, error) {
		runes := []rune(string(line))
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return []byte(string(runes)), nil
	})
}

// nlB: number each line. Pattern: StatefulMap.
func nlB() gloo.Command[[]byte, []byte] {
	return patterns.StatefulMap(func() func([]byte) ([]byte, error) {
		n := 0
		return func(line []byte) ([]byte, error) {
			n++
			return []byte(fmt.Sprintf("%6d\t%s", n, string(line))), nil
		}
	})
}

// grepB: keep lines containing pattern. Pattern: Filter.
func grepB(pattern []byte) gloo.Command[[]byte, []byte] {
	return patterns.Filter(func(line []byte) (bool, error) {
		return bytes.Contains(line, pattern), nil
	})
}

// headB: emit first n lines. Pattern: StatefulFilter.
func headB(n int) gloo.Command[[]byte, []byte] {
	return patterns.StatefulFilter(func() func([]byte) (bool, error) {
		count := 0
		return func(_ []byte) (bool, error) {
			count++
			return count <= n, nil
		}
	})
}

// tacB: reverse line order. Pattern: Accumulate.
func tacB() gloo.Command[[]byte, []byte] {
	return patterns.Accumulate(func(lines [][]byte) ([][]byte, error) {
		slices.Reverse(lines)
		return lines, nil
	})
}

// wcLinesB: count lines, emit as single []byte. Pattern: Aggregate.
func wcLinesB() gloo.Command[[]byte, []byte] {
	return patterns.Aggregate(func(lines [][]byte) ([]byte, error) {
		return []byte(fmt.Sprintf("%d", len(lines))), nil
	})
}

// splitFieldsB: split each line on whitespace into multiple lines. Pattern: Expand.
func splitFieldsB() gloo.Command[[]byte, []byte] {
	return patterns.Expand(func(line []byte) ([][]byte, error) {
		return bytes.Fields(line), nil
	})
}

// ---------------------------------------------------------------------------
// Integration tests
// ---------------------------------------------------------------------------

func TestWave1_AllPatternsCompose(t *testing.T) {
	ctx := context.Background()
	input := gloo.SliceSource([][]byte{
		[]byte("hello world"),
		[]byte("foo bar baz"),
		[]byte("hello again"),
	})

	// Pipeline: grep "hello" | head 1 | rev
	// Expect: "hello world" matched, head takes 1 line, rev reverses it.
	pipeline := gloo.Compose(grepB([]byte("hello"))).
		To(headB(1)).
		To(revB())

	stream := gloo.From(ctx, input, pipeline)
	got, err := gloo.Collect(ctx, stream)
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}

	if len(got) != 1 || string(got[0]) != "dlrow olleh" {
		t.Errorf("got %q, want [\"dlrow olleh\"]", got)
	}
}

func TestWave1_AccumulateInPipeline(t *testing.T) {
	ctx := context.Background()
	input := gloo.SliceSource([][]byte{
		[]byte("alpha"),
		[]byte("bravo"),
		[]byte("charlie"),
	})

	// Pipeline: tac | head 2
	// Expect: reversed to [charlie, bravo, alpha], head takes first 2.
	pipeline := gloo.Compose(tacB()).To(headB(2))

	stream := gloo.From(ctx, input, pipeline)
	got, err := gloo.Collect(ctx, stream)
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}

	want := []string{"charlie", "bravo"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i, w := range want {
		if string(got[i]) != w {
			t.Errorf("line %d: got %q, want %q", i, got[i], w)
		}
	}
}

func TestWave1_ExpandThenAggregate(t *testing.T) {
	ctx := context.Background()
	input := gloo.SliceSource([][]byte{
		[]byte("one two"),
		[]byte("three four five"),
	})

	// Pipeline: split (expand to words) | wc -l (count)
	// "one two" → 2 words, "three four five" → 3 words = 5 total.
	pipeline := gloo.Pipe(splitFieldsB(), wcLinesB())

	stream := gloo.From(ctx, input, pipeline)
	got, err := gloo.Collect(ctx, stream)
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}

	if len(got) != 1 || string(got[0]) != "5" {
		t.Errorf("got %q, want [\"5\"]", got)
	}
}

func TestWave1_StatefulMapInChain(t *testing.T) {
	ctx := context.Background()
	input := gloo.SliceSource([][]byte{
		[]byte("alpha"),
		[]byte("bravo"),
	})

	// Pipeline: nl | grep "1" (match line 1 only)
	pipeline := gloo.Compose(nlB()).To(grepB([]byte("1")))

	stream := gloo.From(ctx, input, pipeline)
	got, err := gloo.Collect(ctx, stream)
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}

	if len(got) != 1 || !bytes.Contains(got[0], []byte("alpha")) {
		t.Errorf("got %q, want line containing 'alpha' with number 1", got)
	}
}

func TestWave1_FluentPipeline(t *testing.T) {
	ctx := context.Background()
	input := gloo.SliceSource([][]byte{
		[]byte("delta"),
		[]byte("echo foxtrot"),
		[]byte("golf"),
	})

	// Fluent: source → split → grep "o" → tac → nl
	result, err := gloo.ChainContext(ctx, input).
		To(splitFieldsB()).
		To(grepB([]byte("o"))).
		To(tacB()).
		To(nlB()).
		Collect()
	if err != nil {
		t.Fatalf("pipeline error: %v", err)
	}

	lines := result.([][]byte)
	// split produces: delta, echo, foxtrot, golf
	// grep "o": echo, foxtrot, golf (all contain "o")
	// tac: golf, foxtrot, echo
	// nl: "     1\tgolf", "     2\tfoxtrot", "     3\techo"
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), lines)
	}
	if !bytes.Contains(lines[0], []byte("golf")) {
		t.Errorf("line 0: got %q, want to contain 'golf'", lines[0])
	}
	if !bytes.Contains(lines[1], []byte("foxtrot")) {
		t.Errorf("line 1: got %q, want to contain 'foxtrot'", lines[1])
	}
	if !bytes.Contains(lines[2], []byte("echo")) {
		t.Errorf("line 2: got %q, want to contain 'echo'", lines[2])
	}
}

func TestWave1_EmptyInput(t *testing.T) {
	ctx := context.Background()
	input := gloo.SliceSource([][]byte{})

	// All 7 patterns should handle empty input gracefully.
	cmds := []struct {
		cmd  gloo.Command[[]byte, []byte]
		name string
	}{
		{name: "Map(rev)", cmd: revB()},
		{name: "StatefulMap(nl)", cmd: nlB()},
		{name: "Filter(grep)", cmd: grepB([]byte("x"))},
		{name: "StatefulFilter(head)", cmd: headB(5)},
		{name: "Accumulate(tac)", cmd: tacB()},
		{name: "Expand(split)", cmd: splitFieldsB()},
	}

	for _, tc := range cmds {
		t.Run(tc.name, func(t *testing.T) {
			stream := gloo.From(ctx, input, tc.cmd)
			got, err := gloo.Collect(ctx, stream)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("got %d items, want 0", len(got))
			}
		})
	}

	// Aggregate on empty input produces one output (the aggregate result).
	t.Run("Aggregate(wc)", func(t *testing.T) {
		stream := gloo.From(ctx, input, wcLinesB())
		got, err := gloo.Collect(ctx, stream)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if len(got) != 1 || string(got[0]) != "0" {
			t.Errorf("got %q, want [\"0\"]", got)
		}
	})
}
