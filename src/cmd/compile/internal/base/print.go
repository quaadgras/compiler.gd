// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package base

import (
	"fmt"
	"internal/buildcfg"
	"internal/types/errors"
	"os"
	"runtime/debug"
	"sort"
	"strings"

	"cmd/internal/src"
	"cmd/internal/telemetry/counter"
)

// An errorMsg is a queued error message, waiting to be printed.
type errorMsg struct {
	pos  src.XPos
	msg  string
	code errors.Code
}

// Errors returns the number of errors reported.
func (gd *Invocation) Errors() int {
	return gd.numErrors
}

// SyntaxErrors returns the number of syntax errors reported.
func (gd *Invocation) SyntaxErrors() int {
	return gd.numSyntaxErrors
}

// addErrorMsg adds a new errorMsg (which may be a warning) to errorMsgs.
func (gd *Invocation) addErrorMsg(pos src.XPos, code errors.Code, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	// Only add the position if know the position.
	// See issue golang.org/issue/11361.
	if pos.IsKnown() {
		msg = fmt.Sprintf("%v: %s", gd.FmtPos(pos), msg)
	}
	gd.errorMsgs = append(gd.errorMsgs, errorMsg{
		pos:  pos,
		msg:  msg + "\n",
		code: code,
	})
}

// FmtPos formats pos as a file:line string.
func (gd *Invocation) FmtPos(pos src.XPos) string {
	if gd.Ctxt == nil {
		return "???"
	}
	return gd.Ctxt.OutermostPos(pos).Format(gd.Flag.C == 0, gd.Flag.L == 1)
}

// byPos sorts errors by source position.
type byPos []errorMsg

func (x byPos) Len() int           { return len(x) }
func (x byPos) Less(i, j int) bool { return x[i].pos.Before(x[j].pos) }
func (x byPos) Swap(i, j int)      { x[i], x[j] = x[j], x[i] }

// FlushErrors sorts errors seen so far by line number, prints them to stderr,
// and empties the errors array.
//
// Stock cmd/compile uses fmt.Print (stdout) here because cmd/go captures
// the compile subprocess's combined stdout+stderr into one buffer and
// re-emits it on cmd/go's own stderr — so the user always sees errors
// on stderr regardless. Under cmd/compile/host.Run (in-process from
// cmd/go) there's no capture: the compile writes go directly to cmd/go's
// stdout, so script tests like cmd/compile/TestScript/embedbad that
// expect "stderr: invalid go:embed: …" find their match on stdout
// instead and fail. Writing to stderr here matches the user-observable
// behaviour and keeps fork/exec callers working since they already
// merge the two streams.
func (gd *Invocation) FlushErrors() {
	if gd.Ctxt != nil && gd.Ctxt.Bso != nil {
		gd.Ctxt.Bso.Flush()
	}
	if len(gd.errorMsgs) == 0 {
		return
	}
	sort.Stable(byPos(gd.errorMsgs))
	for i, err := range gd.errorMsgs {
		if i == 0 || err.msg != gd.errorMsgs[i-1].msg {
			fmt.Fprint(os.Stderr, err.msg)
		}
	}
	gd.errorMsgs = gd.errorMsgs[:0]
}

// sameline reports whether two positions a, b are on the same line.
func (gd *Invocation) sameline(a, b src.XPos) bool {
	p := gd.Ctxt.PosTable.Pos(a)
	q := gd.Ctxt.PosTable.Pos(b)
	return p.Base() == q.Base() && p.Line() == q.Line()
}

// Errorf reports a formatted error at the current line.
func (gd *Invocation) Errorf(format string, args ...any) {
	gd.ErrorfAt(gd.Pos, 0, format, args...)
}

// ErrorfAt reports a formatted error message at pos.
func (gd *Invocation) ErrorfAt(pos src.XPos, code errors.Code, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)

	if strings.HasPrefix(msg, "syntax error") {
		gd.numSyntaxErrors++
		// only one syntax error per line, no matter what error
		if gd.sameline(gd.lasterror.syntax, pos) {
			return
		}
		gd.lasterror.syntax = pos
	} else {
		// only one of multiple equal non-syntax errors per line
		// (FlushErrors shows only one of them, so we filter them
		// here as best as we can (they may not appear in order)
		// so that we don't count them here and exit early, and
		// then have nothing to show for.)
		if gd.sameline(gd.lasterror.other, pos) && gd.lasterror.msg == msg {
			return
		}
		gd.lasterror.other = pos
		gd.lasterror.msg = msg
	}

	gd.addErrorMsg(pos, code, "%s", msg)
	gd.numErrors++

	gd.hcrash()
	if gd.numErrors >= 10 && gd.Flag.LowerE == 0 {
		gd.FlushErrors()
		fmt.Fprintf(os.Stderr, "%v: too many errors\n", gd.FmtPos(pos))
		gd.ErrorExit()
	}
}

// UpdateErrorDot is a clumsy hack that rewrites the last error,
// if it was "LINE: undefined: NAME", to be "LINE: undefined: NAME in EXPR".
// It is used to give better error messages for dot (selector) expressions.
func (gd *Invocation) UpdateErrorDot(line string, name, expr string) {
	if len(gd.errorMsgs) == 0 {
		return
	}
	e := &gd.errorMsgs[len(gd.errorMsgs)-1]
	if strings.HasPrefix(e.msg, line) && e.msg == fmt.Sprintf("%v: undefined: %v\n", line, name) {
		e.msg = fmt.Sprintf("%v: undefined: %v in %v\n", line, name, expr)
	}
}

// Warn reports a formatted warning at the current line.
// In general the Go compiler does NOT generate warnings,
// so this should be used only when the user has opted in
// to additional output by setting a particular flag.
func (gd *Invocation) Warn(format string, args ...any) {
	gd.WarnfAt(gd.Pos, format, args...)
}

// WarnfAt reports a formatted warning at pos.
// In general the Go compiler does NOT generate warnings,
// so this should be used only when the user has opted in
// to additional output by setting a particular flag.
func (gd *Invocation) WarnfAt(pos src.XPos, format string, args ...any) {
	gd.addErrorMsg(pos, 0, format, args...)
	if gd.Flag.LowerM != 0 {
		gd.FlushErrors()
	}
}

// Fatalf reports a fatal error - an internal problem - at the current line and exits.
// If other errors have already been printed, then Fatalf just quietly exits.
// (The internal problem may have been caused by incomplete information
// after the already-reported errors, so best to let users fix those and
// try again without being bothered about a spurious internal error.)
//
// But if no errors have been printed, or if -d panic has been specified,
// Fatalf prints the error as an "internal compiler error". In a released build,
// it prints an error asking to file a bug report. In development builds, it
// prints a stack trace.
//
// If -h has been specified, Fatalf panics to force the usual runtime info dump.
func (gd *Invocation) Fatalf(format string, args ...any) {
	gd.FatalfAt(gd.Pos, format, args...)
}

var bugStack = counter.NewStack("compile/bug", 16) // 16 is arbitrary; used by gopls and crashmonitor

// FatalfAt reports a fatal error - an internal problem - at pos and exits.
// If other errors have already been printed, then FatalfAt just quietly exits.
// (The internal problem may have been caused by incomplete information
// after the already-reported errors, so best to let users fix those and
// try again without being bothered about a spurious internal error.)
//
// But if no errors have been printed, or if -d panic has been specified,
// FatalfAt prints the error as an "internal compiler error". In a released build,
// it prints an error asking to file a bug report. In development builds, it
// prints a stack trace.
//
// If -h has been specified, FatalfAt panics to force the usual runtime info dump.
func (gd *Invocation) FatalfAt(pos src.XPos, format string, args ...any) {
	gd.FlushErrors()

	bugStack.Inc()

	if gd.Debug.Panic != 0 || gd.numErrors == 0 {
		fmt.Fprintf(os.Stderr, "%v: internal compiler error: ", gd.FmtPos(pos))
		fmt.Fprintf(os.Stderr, format, args...)
		fmt.Fprintln(os.Stderr)

		// If this is a released compiler version, ask for a bug report.
		if gd.Debug.Panic == 0 && strings.HasPrefix(buildcfg.Version, "go") && !strings.Contains(buildcfg.Version, "devel") {
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "Please file a bug report including a short program that triggers the error.")
			fmt.Fprintln(os.Stderr, "https://go.dev/issue/new")
		} else {
			// Not a release; dump a stack trace, too.
			fmt.Fprintln(os.Stderr)
			os.Stderr.Write(debug.Stack())
			fmt.Fprintln(os.Stderr)
		}
	}

	gd.hcrash()
	gd.ErrorExit()
}

// Assert reports "assertion failed" with Fatalf, unless b is true.
func (gd *Invocation) Assert(b bool) {
	if !b {
		gd.Fatalf("assertion failed")
	}
}

// Assertf reports a fatal error with Fatalf, unless b is true.
func (gd *Invocation) Assertf(b bool, format string, args ...any) {
	if !b {
		gd.Fatalf(format, args...)
	}
}

// AssertfAt reports a fatal error with FatalfAt, unless b is true.
func (gd *Invocation) AssertfAt(b bool, pos src.XPos, format string, args ...any) {
	if !b {
		gd.FatalfAt(pos, format, args...)
	}
}

// hcrash crashes the compiler when -h is set, to find out where a message is generated.
func (gd *Invocation) hcrash() {
	if gd.Flag.LowerH != 0 {
		gd.FlushErrors()
		if gd.Flag.LowerO != "" {
			os.Remove(gd.Flag.LowerO)
		}
		panic("-h")
	}
}

// ErrorExit handles an error-status exit.
// It flushes any pending errors, removes the output file, and exits.
//
// Goes through gd.Exit (runtime.Goexit + Status=2) rather than
// os.Exit so an in-process embedder can recover and continue. The
// outermost cmd/compile/main.go converts gd.Status into a process
// exit code via os.Exit.
func (gd *Invocation) ErrorExit() {
	gd.FlushErrors()
	if gd.Flag.LowerO != "" {
		os.Remove(gd.Flag.LowerO)
	}
	gd.Exit(2)
}

// ExitIfErrors calls ErrorExit if any errors have been reported.
func (gd *Invocation) ExitIfErrors() {
	if gd.Errors() > 0 {
		gd.ErrorExit()
	}
}
