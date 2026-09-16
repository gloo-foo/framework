package patterns

import (
	gloo "github.com/gloo-foo/framework"
)

// Head emits the first n items and stops the upstream — the shell `head -n`.
// It is a named alias for Take, provided because pipelines read more clearly
// with the familiar command name:
//
//	gloo.Compose(grep("error")).To(patterns.Head[string](10))
//
// Like Take, Head over an infinite or huge source terminates promptly and
// reads only a small prefix. Use Take directly when the early-exit count is
// not a "head" in spirit (e.g. grep -m, sed q).
func Head[T any](n Count) gloo.Command[T, T] {
	return Take[T](n)
}

// Drop discards the first n items and passes the rest through — the shell
// `tail -n +N` (skip a header, drop a prefix). Unlike Head it has no early
// exit: it must read every item, so it does not stop the upstream.
//
//	gloo.Compose(patterns.Drop[string](1)).To(sortLines) // skip a CSV header
func Drop[T any](n Count) gloo.Command[T, T] {
	return StatefulFilter(func() func(T) (bool, error) {
		seen := 0
		return func(T) (bool, error) {
			seen++
			return seen > int(n), nil
		}
	})
}
