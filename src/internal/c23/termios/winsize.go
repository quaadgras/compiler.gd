// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package termios

import (
	c23 "internal/c23"
)

// Tcgetwinsize is tcgetwinsize: the terminal's struct winsize
// {unsigned short ws_row, ws_col, ws_xpixel, ws_ypixel;}.
func Tcgetwinsize(fd int32, p c23.Ptr) int32 {
	f := fdTTY(fd)
	if f == nil {
		return -1
	}
	ws, err := getwinsize(f)
	if err != nil {
		return result(err)
	}
	for i, v := range ws {
		c23.Store16(c23.Add(p, 2*i), v)
	}
	return 0
}

// Tcsetwinsize is tcsetwinsize.
func Tcsetwinsize(fd int32, p c23.Ptr) int32 {
	f := fdTTY(fd)
	if f == nil {
		return -1
	}
	var ws [4]uint16
	for i := range ws {
		ws[i] = c23.Load16(c23.Add(p, 2*i))
	}
	return result(setwinsize(f, &ws))
}
