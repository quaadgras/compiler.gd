// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package err

import (
	c23 "internal/c23"
)

// warnf writes the program's name, then what fmt formats (unless fmt is
// null), then, if errno is true, ": " and the message of errno, on
// stderr.
func warnf(format, ap c23.Ptr, errno bool) {
	e := c23.Errno()
	msg := c23.Progname() + ": "
	if c23.Addr(format) != 0 {
		msg += string(c23.FormatC(c23.GoString(format), c23.VaOf(ap)))
		if errno {
			msg += ": "
		}
	}
	if errno {
		msg += c23.ErrnoString(e)
	}
	c23.Fflush(c23.Stdio(1))
	c23.FileOf(c23.Stdio(2)).Write([]byte(msg + "\n"))
}

func Warn(format c23.Ptr, va ...any) { warnf(format, c23.VaStart(va), true) }

func Vwarn(format, ap c23.Ptr) { warnf(format, ap, true) }

func Warnx(format c23.Ptr, va ...any) { warnf(format, c23.VaStart(va), false) }

func Vwarnx(format, ap c23.Ptr) { warnf(format, ap, false) }

func Err(eval int32, format c23.Ptr, va ...any) { Verr(eval, format, c23.VaStart(va)) }

func Verr(eval int32, format, ap c23.Ptr) {
	warnf(format, ap, true)
	c23.Exit(eval)
}

func Errx(eval int32, format c23.Ptr, va ...any) { Verrx(eval, format, c23.VaStart(va)) }

func Verrx(eval int32, format, ap c23.Ptr) {
	warnf(format, ap, false)
	c23.Exit(eval)
}
