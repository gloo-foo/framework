package gloo

import (
	"strconv"

	errs "github.com/gomatic/go-error"
)

// Error is the framework's sentinel-error type — an alias of [errs.Const] from
// github.com/gomatic/go-error, which owns the mechanism (Error, With). The
// alias exists for hub compatibility: the fleet of cmd-* consumers declares
// sentinels as consts of this type (const ErrX gloo.Error = "...") and wraps
// with .With(cause, args...); because an alias is the same type, every such
// declaration, .With call, and errors.Is match keeps compiling and behaving
// identically. New code may use errs.Const directly.
type Error = errs.Const

// ExitStatus is an error carrying a process-style exit status, completing the
// Unix contract's second half: a tool like grep signals "no match" with exit 1
// — a status, not a diagnostic. A command emits it on its stream
// (sendErr(gloo.ExitStatus(1))); a binary wrapper extracts it with errors.As
// and passes Status() to os.Exit instead of printing it as a failure.
//
// It is a comparable value type, so errors.Is(err, gloo.ExitStatus(1)) matches
// exactly and distinct statuses stay distinct.
type ExitStatus int

// Error renders the conventional shell description of the status.
func (e ExitStatus) Error() string {
	return "exit status " + strconv.Itoa(int(e))
}

// Status is the numeric exit status for a wrapper to hand to os.Exit.
func (e ExitStatus) Status() int {
	return int(e)
}
