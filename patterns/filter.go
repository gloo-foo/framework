package patterns

import (
	gloo "github.com/gloo-foo/framework"
)

// Filter creates a Command that keeps or drops items based on a predicate.
// The function is stateless — it decides per item with no memory of previous items.
//
// This is the pattern for commands where each input line either passes through
// unchanged or is dropped entirely.
//
// Shell equivalents: grep, grep -v
//
// Example — building a Grep command:
//
//	func Grep(pattern string) gloo.Command[string, string] {
//	    return patterns.Filter(func(line string) (bool, error) {
//	        return strings.Contains(line, pattern), nil
//	    })
//	}
func Filter[T any](fn func(T) (bool, error)) gloo.Command[T, T] {
	return StatefulFilter(func() func(T) (bool, error) { return fn })
}

// StatefulFilter creates a Command where each Execute call gets fresh state.
// The factory is called once per Execute, returning a predicate function
// with its own captured state. This ensures the command is reusable as a value.
//
// This is the pattern for commands that filter but need to track something
// across items (a counter, previous line, etc.).
//
// Shell equivalents: head -n (counter), uniq (previous line)
//
// Example — building a Head command:
//
//	func Head(n int) gloo.Command[string, string] {
//	    return patterns.StatefulFilter(func() func(string) (bool, error) {
//	        count := 0
//	        return func(line string) (bool, error) {
//	            count++
//	            return count <= n, nil
//	        }
//	    })
//	}
//
// Example — building a Uniq command:
//
//	func Uniq() gloo.Command[string, string] {
//	    return patterns.StatefulFilter(func() func(string) (bool, error) {
//	        var prev string
//	        first := true
//	        return func(line string) (bool, error) {
//	            if first || line != prev {
//	                prev = line
//	                first = false
//	                return true, nil
//	            }
//	            return false, nil
//	        }
//	    })
//	}
func StatefulFilter[T any](factory func() func(T) (bool, error)) gloo.Command[T, T] {
	return produce(func(in gloo.Stream[T], send func(T) bool, sendErr func(error)) {
		fn := factory()
		eachValue(in, sendErr, func(v T) bool {
			keep, err := fn(v)
			if err != nil {
				sendErr(err)
				return true // the stream continues past a failed item
			}
			return !keep || send(v)
		})
	})
}
