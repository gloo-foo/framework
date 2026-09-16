package patterns

import (
	gloo "github.com/gloo-foo/framework"
)

// Map creates a Command that transforms each input item independently.
// The function is stateless — it receives one item and returns one item.
//
// This is the pattern for commands where each input line produces exactly one
// output line with no memory of previous lines.
//
// Shell equivalents: tr, sed s/old/new/, cut -d -f, basename, dirname
//
// Example — building a Tr command:
//
//	func Tr(from, to string) gloo.Command[string, string] {
//	    return patterns.Map(func(line string) (string, error) {
//	        return translate(line, from, to), nil
//	    })
//	}
func Map[In, Out any](fn func(In) (Out, error)) gloo.Command[In, Out] {
	return StatefulMap(func() func(In) (Out, error) { return fn })
}

// StatefulMap creates a Command where each Execute call gets fresh state.
// The factory is called once per Execute, returning a transform function
// with its own captured state. This ensures the command is reusable as a value.
//
// This is the pattern for commands that transform each item but need to track
// something across items (a counter, running total, etc.).
//
// Shell equivalents: nl (line counter), paste (position tracking)
//
// Example — building an Nl command:
//
//	func Nl() gloo.Command[string, string] {
//	    return patterns.StatefulMap(func() func(string) (string, error) {
//	        n := 0
//	        return func(line string) (string, error) {
//	            n++
//	            return fmt.Sprintf("%d\t%s", n, line), nil
//	        }
//	    })
//	}
func StatefulMap[In, Out any](factory func() func(In) (Out, error)) gloo.Command[In, Out] {
	return produce(func(in gloo.Stream[In], send func(Out) bool, sendErr func(error)) {
		fn := factory()
		eachValue(in, sendErr, func(v In) bool {
			out, err := fn(v)
			if err != nil {
				sendErr(err)
				return true // the stream continues past a failed item
			}
			return send(out)
		})
	})
}
