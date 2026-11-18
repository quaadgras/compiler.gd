// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"internal/buildcfg"
	"os"
	"strings"
)

// setToolTarget sets internal/buildcfg, the configuration compile, asm
// and link build for, to the build's: the environment a tool would
// have been run with (this process's, and cfgChangedEnv). They run in
// this process (gc_inproc.go), so a build for another GOOS or GOARCH
// than the go command's own needs no tool binaries (gd fork).
//
// The tools keep some state for the life of the process (types of the
// universe, caches of the target's layouts...): every build that runs
// them in-process has the target set here, before its first action.
func setToolTarget() {
	env := make(map[string]string)
	for _, kv := range cfgChangedEnv {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	buildcfg.SetTarget(func(key string) string {
		if v, ok := env[key]; ok {
			return v
		}
		return os.Getenv(key)
	})
}
