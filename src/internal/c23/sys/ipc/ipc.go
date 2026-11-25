// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ipc is sys/ipc.h: ftok. The objects of XSI IPC are the
// process's (internal/c23/sys/msg, sys/sem and sys/shm).
package ipc

import (
	c23 "internal/c23"
	"os"
	"reflect"
)

// Ftok is ftok, glibc's and musl's: the low 16 bits of the file's inode
// number, the low 8 of its device's and the low 8 of id. Where the system
// gives no inode or device numbers (Windows, wasm), they are 0.
func Ftok(path *c23.Void, id int32) int32 {
	fi, err := os.Stat(c23.GoString(path))
	if err != nil {
		c23.SetErrnoOf(err)
		return -1
	}
	sys := reflect.Indirect(reflect.ValueOf(fi.Sys()))
	if sys.IsValid() && sys.Kind() != reflect.Struct {
		sys = reflect.Value{}
	}
	ino, _ := c23.SysField(sys, "Ino")
	dev, _ := c23.SysField(sys, "Dev")
	return int32(uint32(ino&0xffff) | uint32(dev&0xff)<<16 | uint32(id&0xff)<<24)
}
