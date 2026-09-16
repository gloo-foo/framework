package verification

// Shell command pipeline examples.
//
// Demonstrates that the framework can express Unix shell pipelines in pure Go,
// with each command behaving like its shell counterpart and pipelines composing
// exactly as shell pipes would.
//
// Shell commands implemented:
//   - cat (passthrough / identity)
//   - grep (filter lines matching a pattern)
//   - sed s/old/new/ (substitution)
//   - cut -d -f (field extraction)
//   - tr (character translation)
//   - head -n N (first N lines)
//   - tail -n N (last N lines)
//   - sort (sort lines)
//   - uniq (deduplicate adjacent lines)
//   - nl (number lines)
//   - tac (reverse line order)
//   - wc -l (count lines — as a Sink)
//
// Pipeline patterns demonstrated:
//   - Source → Command (From)
//   - Source → Command → Sink (Into)
//   - Same-type chaining (Chain/To)
//   - Type-changing composition (Pipe)
//   - Fluent pipeline (Chain/To/Collect)
//   - File source pipelines (FileSource)
//   - Multi-file concatenation
//   - WriteTo sink (output redirection)
//   - Nested chains
//   - Stateful commands

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/destel/rill"
	"github.com/spf13/afero"

	gloo "github.com/gloo-foo/framework"
	"github.com/gloo-foo/framework/patterns"
)

// ---------------------------------------------------------------------------
// Shell command implementations — each uses a pattern from patterns/
// ---------------------------------------------------------------------------

// cat: passthrough (identity command).
// Shell: cat
func cat() gloo.Command[string, string] {
	return patterns.Map(func(line string) (string, error) {
		return line, nil
	})
}

// grep: keep lines containing pattern.
// Shell: grep <pattern>
func grep(pattern string) gloo.Command[string, string] {
	return patterns.Filter(func(line string) (bool, error) {
		return strings.Contains(line, pattern), nil
	})
}

// grepV: keep lines NOT containing pattern.
// Shell: grep -v <pattern>
func grepV(pattern string) gloo.Command[string, string] {
	return patterns.Filter(func(line string) (bool, error) {
		return !strings.Contains(line, pattern), nil
	})
}

// sed: substitute first occurrence of old with new in each line.
// Shell: sed 's/old/new/'
func sed(pattern, replacement string) gloo.Command[string, string] {
	return patterns.Map(func(line string) (string, error) {
		return strings.Replace(line, pattern, replacement, 1), nil
	})
}

// cut: extract a field by delimiter.
// Shell: cut -d<delim> -f<field> (1-based)
func cut(delim string, field int) gloo.Command[string, string] {
	return patterns.Map(func(line string) (string, error) {
		parts := strings.Split(line, delim)
		if field < 1 || field > len(parts) {
			return "", nil
		}
		return parts[field-1], nil
	})
}

// tr: translate characters.
// Shell: tr <from> <to>
func tr(fn func(string) string) gloo.Command[string, string] {
	return patterns.Map(func(line string) (string, error) {
		return fn(line), nil
	})
}

// head: emit only the first n lines.
// Shell: head -n <n>
func head(n int) gloo.Command[string, string] {
	return patterns.StatefulFilter(func() func(string) (bool, error) {
		count := 0
		return func(_ string) (bool, error) {
			count++
			return count <= n, nil
		}
	})
}

// tail: emit only the last n lines.
// Shell: tail -n <n>
func tail(n int) gloo.Command[string, string] {
	return patterns.Accumulate(func(lines []string) ([]string, error) {
		if len(lines) <= n {
			return lines, nil
		}
		return lines[len(lines)-n:], nil
	})
}

// sortCmd: sort all lines lexicographically.
// Shell: sort
func sortCmd() gloo.Command[string, string] {
	return patterns.Accumulate(func(lines []string) ([]string, error) {
		sort.Strings(lines)
		return lines, nil
	})
}

// uniq: deduplicate adjacent identical lines.
// Shell: uniq
func uniq() gloo.Command[string, string] {
	return patterns.StatefulFilter(func() func(string) (bool, error) {
		var prev string
		first := true
		return func(line string) (bool, error) {
			if first || line != prev {
				prev = line
				first = false
				return true, nil
			}
			return false, nil
		}
	})
}

// nl: number each line (1-based).
// Shell: nl
func nl() gloo.Command[string, string] {
	return patterns.StatefulMap(func() func(string) (string, error) {
		n := 0
		return func(line string) (string, error) {
			n++
			return fmt.Sprintf("%d\t%s", n, line), nil
		}
	})
}

// tac: reverse line order.
// Shell: tac
func tac() gloo.Command[string, string] {
	return patterns.Accumulate(func(lines []string) ([]string, error) {
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
		return lines, nil
	})
}

// wcL: count lines (terminal operation — implemented as a Sink).
// Shell: wc -l
type wcL struct{}

func (w wcL) Consume(_ context.Context, input gloo.Stream[string]) (int, error) {
	count := 0
	err := rill.ForEach(input.Chan(), 1, func(_ string) error {
		count++
		return nil
	})
	return count, err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func expectLines(t *testing.T, got []string, expected ...string) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("expected %d lines, got %d: %v", len(expected), len(got), got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Errorf("line %d: expected %q, got %q", i, expected[i], got[i])
		}
	}
}

func collectStream(t *testing.T, stream gloo.Stream[string]) []string {
	t.Helper()
	results, err := stream.Collect()
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}
	return results
}

// ---------------------------------------------------------------------------
// Pipeline tests — each mirrors an equivalent shell pipeline
// ---------------------------------------------------------------------------

// Shell: echo -e "apple\nbanana\ncherry" | grep an
func TestShell_Grep(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"apple", "banana", "cherry"})

	results := collectStream(t, gloo.From(ctx, src, grep("an")))
	expectLines(t, results, "banana")
}

// Shell: echo -e "apple\nbanana\ncherry" | grep -v an
func TestShell_GrepInvert(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"apple", "banana", "cherry"})

	results := collectStream(t, gloo.From(ctx, src, grepV("an")))
	expectLines(t, results, "apple", "cherry")
}

// Shell: echo -e "hello world\nfoo bar" | sed 's/world/earth/'
func TestShell_Sed(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"hello world", "foo bar"})

	results := collectStream(t, gloo.From(ctx, src, sed("world", "earth")))
	expectLines(t, results, "hello earth", "foo bar")
}

// Shell: echo -e "a:b:c\nd:e:f" | cut -d: -f2
func TestShell_Cut(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"a:b:c", "d:e:f"})

	results := collectStream(t, gloo.From(ctx, src, cut(":", 2)))
	expectLines(t, results, "b", "e")
}

// Shell: echo -e "Hello\nWorld" | tr 'a-zA-Z' 'A-Za-z' (uppercase)
func TestShell_Tr(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"Hello", "World"})

	results := collectStream(t, gloo.From(ctx, src, tr(strings.ToUpper)))
	expectLines(t, results, "HELLO", "WORLD")
}

// Shell: seq 1 5 | head -3
func TestShell_Head(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"1", "2", "3", "4", "5"})

	results := collectStream(t, gloo.From(ctx, src, head(3)))
	expectLines(t, results, "1", "2", "3")
}

// Shell: seq 1 5 | tail -2
func TestShell_Tail(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"1", "2", "3", "4", "5"})

	results := collectStream(t, gloo.From(ctx, src, tail(2)))
	expectLines(t, results, "4", "5")
}

// Shell: echo -e "banana\napple\ncherry" | sort
func TestShell_Sort(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"banana", "apple", "cherry"})

	results := collectStream(t, gloo.From(ctx, src, sortCmd()))
	expectLines(t, results, "apple", "banana", "cherry")
}

// Shell: echo -e "a\na\nb\nb\nb\nc" | uniq
func TestShell_Uniq(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"a", "a", "b", "b", "b", "c"})

	results := collectStream(t, gloo.From(ctx, src, uniq()))
	expectLines(t, results, "a", "b", "c")
}

// Shell: echo -e "first\nsecond\nthird" | nl
func TestShell_Nl(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"first", "second", "third"})

	results := collectStream(t, gloo.From(ctx, src, nl()))
	expectLines(t, results, "1\tfirst", "2\tsecond", "3\tthird")
}

// Shell: echo -e "first\nsecond\nthird" | tac
func TestShell_Tac(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"first", "second", "third"})

	results := collectStream(t, gloo.From(ctx, src, tac()))
	expectLines(t, results, "third", "second", "first")
}

// Shell: echo -e "a\nb\nc" | wc -l
func TestShell_WcL(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"a", "b", "c"})

	count, err := gloo.Into(ctx, src, cat(), wcL{})
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("expected 3, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Multi-stage pipeline tests
// ---------------------------------------------------------------------------

// Shell: cat data.txt | grep error | cut -d' ' -f1
// Pattern: From + Chain (same-type, 3 stages)
func TestShell_GrepThenCut(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{
		"2024-01-01 error disk full",
		"2024-01-02 info started",
		"2024-01-03 error timeout",
	})

	cmd := gloo.Compose(grep("error")).To(cut(" ", 1))
	results := collectStream(t, gloo.From(ctx, src, cmd))
	expectLines(t, results, "2024-01-01", "2024-01-03")
}

// Shell: cat data.txt | grep error | wc -l
// Pattern: Into (source → command → sink)
func TestShell_GrepAndCount(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{
		"error: one",
		"info: two",
		"error: three",
		"debug: four",
	})

	count, err := gloo.Into(ctx, src, grep("error"), wcL{})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2, got %d", count)
	}
}

// Shell: cat log.txt | grep WARN | sed 's/WARN/WARNING/' | tr 'a-z' 'A-Z' | head -2
// Pattern: Chain with 4 stages including stateful head
func TestShell_FourStagePipeline(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{
		"INFO: all good",
		"WARN: disk low",
		"ERROR: crash",
		"WARN: memory high",
		"WARN: cpu hot",
	})

	cmd := gloo.Compose(grep("WARN")).
		To(sed("WARN", "WARNING")).
		To(tr(strings.ToUpper)).
		To(head(2))

	results := collectStream(t, gloo.From(ctx, src, cmd))
	expectLines(
		t, results,
		"WARNING: DISK LOW",
		"WARNING: MEMORY HIGH",
	)
}

// Shell: cat data.txt | cut -d, -f2 | sort | uniq
// Pattern: Chain with sort + uniq (classic dedup pipeline)
func TestShell_SortUniq(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{
		"alice,engineering",
		"bob,sales",
		"charlie,engineering",
		"dave,sales",
		"eve,marketing",
	})

	cmd := gloo.Compose(cut(",", 2)).
		To(sortCmd()).
		To(uniq())

	results := collectStream(t, gloo.From(ctx, src, cmd))
	expectLines(t, results, "engineering", "marketing", "sales")
}

// Shell: cat data.txt | sort | uniq | tac | nl
// Pattern: Chain with 4 stages, buffering commands
func TestShell_SortUniqTacNl(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"c", "a", "b", "a", "c"})

	cmd := gloo.Compose(sortCmd()).
		To(uniq()).
		To(tac()).
		To(nl())

	results := collectStream(t, gloo.From(ctx, src, cmd))
	expectLines(t, results, "1\tc", "2\tb", "3\ta")
}

// Shell: cat file1.txt file2.txt | grep pattern
// Pattern: FileSource with multiple files (cat file1 file2)
func TestShell_MultiFileCat(t *testing.T) {
	ctx := context.Background()
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "access.log", []byte("GET /index\nPOST /login\nGET /dashboard"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "error.log", []byte("ERROR: timeout\nWARN: slow\nERROR: 500"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := gloo.FileSource(fs, []gloo.File{"access.log", "error.log"})
	cmd := grep("ERROR")

	results := collectStream(t, gloo.From(ctx, src, cmd))
	expectLines(t, results, "ERROR: timeout", "ERROR: 500")
}

// Shell: cat data.txt | tr 'a-z' 'A-Z' > output.txt
// Pattern: Into with WriteTo sink (output redirection)
func TestShell_OutputRedirection(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"hello", "world"})

	var buf bytes.Buffer
	count, err := gloo.Into(ctx, src, tr(strings.ToUpper), gloo.WriteTo(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 lines written, got %d", count)
	}
	if buf.String() != "HELLO\nWORLD\n" {
		t.Errorf("expected %q, got %q", "HELLO\nWORLD\n", buf.String())
	}
}

// Shell: cat data.txt | grep pattern | sed 's/old/new/' > out.txt
// Pattern: Into with Chain + WriteTo (full pipeline with redirection)
func TestShell_PipelineToFile(t *testing.T) {
	ctx := context.Background()
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "input.txt", []byte("foo bar\nbaz bar\nqux quux"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := gloo.FileSource(fs, []gloo.File{"input.txt"})
	cmd := gloo.Compose(grep("bar")).To(sed("bar", "BAR"))

	var buf bytes.Buffer
	count, err := gloo.Into(ctx, src, cmd, gloo.WriteTo(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 lines, got %d", count)
	}
	if buf.String() != "foo BAR\nbaz BAR\n" {
		t.Errorf("got %q", buf.String())
	}
}

// ---------------------------------------------------------------------------
// Composition pattern tests
// ---------------------------------------------------------------------------

// Pattern: Pipe for type-changing composition (string → int via Pipe)
// Shell analogy: cat file | wc -c (where wc -c returns byte count per line)
func TestShell_PipeTypeChange(t *testing.T) {
	ctx := context.Background()

	// Command that converts string → int (line length)
	strlen := patterns.Map(func(line string) (int, error) {
		return len(line), nil
	})

	// Command that doubles int → int
	double := patterns.Map(func(n int) (int, error) {
		return n * 2, nil
	})

	// Pipe composes string→int with int→int = string→int
	composed := gloo.Pipe(strlen, double)
	src := gloo.SliceSource([]string{"hi", "hello"})

	stream := gloo.From(ctx, src, composed)
	results, err := gloo.Collect(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	// "hi" = 2*2=4, "hello" = 5*2=10
	if len(results) != 2 || results[0] != 4 || results[1] != 10 {
		t.Errorf("expected [4, 10], got %v", results)
	}
}

// Pattern: FluentPipeline for type-changing chains
// Equivalent to: echo data | string_cmd | bytes_cmd | string_cmd
func TestShell_FluentTypeChanging(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"hello", "world"})

	// string → []byte
	toBytes := patterns.Map(func(s string) ([]byte, error) {
		return []byte(strings.ToUpper(s)), nil
	})

	// []byte → string
	fromBytes := patterns.Map(func(b []byte) (string, error) {
		return "[" + string(b) + "]", nil
	})

	result, err := gloo.ChainContext(ctx, src).
		To(toBytes).
		To(fromBytes).
		Collect()
	if err != nil {
		t.Fatal(err)
	}

	items := result.([]string)
	expectLines(t, items, "[HELLO]", "[WORLD]")
}

// Pattern: Nested chains — a Chain used as a stage inside another Chain.
// Shell analogy: defining an alias and using it in a pipeline.
// alias cleanup='tr A-Z a-z | sed s/foo/bar/'
// cat file | cleanup | sort
func TestShell_NestedChains(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"FOO baz", "FOO qux", "hello FOO"})

	// Inner chain: tr lowercase | sed foo→bar
	cleanup := gloo.Compose(tr(strings.ToLower)).To(sed("foo", "bar"))

	// Outer chain: cleanup | sort
	pipeline := gloo.Compose[string](cleanup).To(sortCmd())

	results := collectStream(t, gloo.From(ctx, src, pipeline))
	expectLines(t, results, "bar baz", "bar qux", "hello bar")
}

// Pattern: Collect convenience function (used without From/Into).
func TestShell_DirectCollect(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"z", "a", "m"})

	stream := sortCmd().Execute(ctx, src.Stream(ctx))
	results, err := gloo.Collect(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	expectLines(t, results, "a", "m", "z")
}

// Pattern: FluentPipeline with ForEach terminal (side-effect processing).
// Shell analogy: cat file | grep pattern | xargs -I{} echo "found: {}"
func TestShell_FluentForEach(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"alpha", "beta", "gamma"})

	var collected []string
	err := gloo.ChainContext(ctx, src).
		To(tr(strings.ToUpper)).
		ForEach(func(line string) error {
			collected = append(collected, "found: "+line)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}

	expectLines(t, collected, "found: ALPHA", "found: BETA", "found: GAMMA")
}

// Pattern: FluentPipeline with Sink terminal.
// Shell: cat file | grep error | wc -l (using fluent API)
func TestShell_FluentSink(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{"error: a", "info: b", "error: c", "error: d"})

	result, err := gloo.ChainContext(ctx, src).
		To(grep("error")).
		Sink(wcL{})
	if err != nil {
		t.Fatal(err)
	}

	count := result.(int)
	if count != 3 {
		t.Errorf("expected 3 errors, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Realistic scenario: log analysis pipeline
// ---------------------------------------------------------------------------

// Shell equivalent of a realistic log analysis:
//
//	cat access.log error.log | grep "500\|error" | cut -d' ' -f1 | sort | uniq | nl
func TestShell_LogAnalysis(t *testing.T) {
	ctx := context.Background()
	fs := afero.NewMemMapFs()

	if err := afero.WriteFile(fs, "access.log", []byte(strings.Join([]string{
		"10.0.0.1 GET /api 200",
		"10.0.0.2 POST /login 500",
		"10.0.0.1 GET /dashboard 200",
		"10.0.0.3 GET /api 500",
	}, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := afero.WriteFile(fs, "error.log", []byte(strings.Join([]string{
		"10.0.0.2 error: auth failed",
		"10.0.0.3 error: timeout",
		"10.0.0.2 error: rate limit",
	}, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	src := gloo.FileSource(fs, []gloo.File{"access.log", "error.log"})
	cmd := gloo.Compose(grep("500")).
		To(cut(" ", 1)).
		To(sortCmd()).
		To(uniq()).
		To(nl())

	results := collectStream(t, gloo.From(ctx, src, cmd))
	expectLines(
		t, results,
		"1\t10.0.0.2",
		"2\t10.0.0.3",
	)
}

// Shell: cat data.csv | tail -3 | cut -d, -f2 | sort
func TestShell_CsvTailSortPipeline(t *testing.T) {
	ctx := context.Background()
	src := gloo.SliceSource([]string{
		"id,name,dept",
		"1,alice,eng",
		"2,bob,sales",
		"3,charlie,eng",
		"4,dave,marketing",
	})

	cmd := gloo.Compose(tail(3)).
		To(cut(",", 2)).
		To(sortCmd())

	results := collectStream(t, gloo.From(ctx, src, cmd))
	expectLines(t, results, "bob", "charlie", "dave")
}
