package patterns

import (
	gloo "github.com/gloo-foo/framework"
)

// FilterMap creates a Command that transforms each item AND decides whether to
// emit it — one pass, one function. The function returns the transformed value,
// whether to keep it, and any error.
//
// This is the pattern for commands that must transform and select together,
// where composing Filter after Map would lose the information the decision
// needs (or compute the transform twice).
//
// Shell equivalents: grep -n (emit "N:line" only for matches), sed -n s//p
//
// Example — building a numbering Grep:
//
//	func GrepN(pattern string) gloo.Command[string, string] {
//	    return patterns.StatefulFilterMap(func() func(string) (string, bool, error) {
//	        n := 0
//	        return func(line string) (string, bool, error) {
//	            n++
//	            if !strings.Contains(line, pattern) {
//	                return "", false, nil
//	            }
//	            return fmt.Sprintf("%d:%s", n, line), true, nil
//	        }
//	    })
//	}
func FilterMap[In, Out any](fn func(In) (Out, bool, error)) gloo.Command[In, Out] {
	return StatefulFilterMap(func() func(In) (Out, bool, error) { return fn })
}

// StatefulFilterMap is FilterMap where each Execute call gets fresh state from
// the factory, keeping the command reusable as a value.
func StatefulFilterMap[In, Out any](factory func() func(In) (Out, bool, error)) gloo.Command[In, Out] {
	return produce(func(in gloo.Stream[In], send func(Out) bool, sendErr func(error)) {
		fn := factory()
		eachValue(in, sendErr, func(v In) bool {
			out, keep, err := fn(v)
			if err != nil {
				sendErr(err)
				return true // the stream continues past a failed item
			}
			return !keep || send(out)
		})
	})
}
