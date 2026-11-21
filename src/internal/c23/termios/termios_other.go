// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !darwin && !(linux && (386 || amd64 || arm || arm64 || loong64 || riscv64 || s390x))

package termios

import (
	c23 "internal/c23"
	"os"
)

// Where the system's terminal interface is not one termios.h has, no
// descriptor is a terminal.

var errNotTTY = c23.ErrnoErr(c23.ENOTTY)

func getattr(f *os.File) (termios, error) { return termios{}, errNotTTY }

func setattr(f *os.File, act int32, t *termios) error { return errNotTTY }

func flush(f *os.File, queue int32) error { return errNotTTY }

func drain(f *os.File) error { return errNotTTY }

func flow(f *os.File, act int32) error { return errNotTTY }

func sendBreak(f *os.File) error { return errNotTTY }

func getpgrp(f *os.File) (int32, error) { return 0, errNotTTY }

func setpgrp(f *os.File, pgrp int32) error { return errNotTTY }

func getsid(f *os.File) (int32, error) { return 0, errNotTTY }

func getwinsize(f *os.File) ([4]uint16, error) { return [4]uint16{}, errNotTTY }

func setwinsize(f *os.File, ws *[4]uint16) error { return errNotTTY }
