// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stdlib

import (
	"internal/c23"
	"os"
	"strings"
	"sync"
)

// environ: the process's environment as C has it, an array of
// "name=value" strings ending in a null pointer, which the library makes
// when C first uses environ and makes again when setenv, unsetenv,
// putenv or clearenv change the environment. C that writes to the
// array or to environ itself changes only what it sees there, not the
// environment getenv and the children see.

var (
	envMu  sync.Mutex
	envObj c23.Ptr // the char ** that is environ, once C uses it
)

// Environ returns where environ is (cmd/cc's libcVars).
func Environ() c23.Ptr {
	envMu.Lock()
	defer envMu.Unlock()
	if c23.Addr(envObj) == 0 {
		envObj = c23.Malloc(uint64(c23.WordSize))
		makeEnviron()
	}
	return envObj
}

// makeEnviron makes environ's array of the environment. envMu is held.
func makeEnviron() {
	env := os.Environ()
	ws := int(c23.WordSize)
	arr := c23.Malloc(uint64((len(env) + 1) * ws))
	for i, kv := range env {
		c23.StorePtr(c23.Add(arr, i*ws), c23.CString(kv))
	}
	c23.StorePtr(c23.Add(arr, len(env)*ws), c23.Ptr{})
	c23.StorePtr(envObj, arr)
}

// envChanged makes environ again, if C has used it.
func envChanged() {
	envMu.Lock()
	defer envMu.Unlock()
	if c23.Addr(envObj) != 0 {
		makeEnviron()
	}
}

// Putenv is putenv: "name=value" sets name, "name" unsets it (as glibc).
func Putenv(s c23.Ptr) int32 {
	kv := c23.GoString(s)
	name, value, ok := strings.Cut(kv, "=")
	if name == "" {
		c23.SetErrno(c23.EINVAL)
		return -1
	}
	var err error
	if ok {
		err = os.Setenv(name, value)
	} else {
		err = os.Unsetenv(name)
	}
	envChanged()
	return c23.ErrRet(err)
}

// Clearenv is clearenv.
func Clearenv() int32 {
	os.Clearenv()
	envChanged()
	return 0
}
