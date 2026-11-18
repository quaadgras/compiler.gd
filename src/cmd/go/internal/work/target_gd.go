// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"cmd/go/internal/cfg"
	"cmd/internal/sys"
	"internal/buildcfg"
	"os"
	"slices"
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
//
// The target's tool tags are set here too. go/build took them from this
// process's environment when it started, as buildcfg did, and
// cfg.BuildContext keeps them but for the goexperiment ones, which
// cmd/go sets from cfg.Experiment: GOARCH's variants (386.sse2,
// amd64.v1...) and the fork's wasm.gc, wasm.split and wasm.ptr32, which
// select files of std. They differ from the build's for a tool built for
// the host ("GOARCH=wasm go tool pprof", cfg.ForceHost) and for a target
// from a GOENV file, whose GOOS and GOARCH this process's environment
// does not have when it starts. Only those are replaced: cmd/go adds
// tags of its own before (instrumentInit's race, msan or asan).
func setToolTarget() {
	stale := make(map[string]bool)
	for _, tag := range buildcfg.ToolTags {
		if !strings.HasPrefix(tag, "goexperiment.") {
			stale[tag] = true
		}
	}

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
	// cmd/internal/sys took wasm's word size from GOWASM when the
	// process started.
	sys.ArchWasm.PtrSize = buildcfg.GOWASM.PtrSize()

	var tags []string
	for _, tag := range cfg.BuildContext.ToolTags {
		if !stale[tag] {
			tags = append(tags, tag)
		}
	}
	for _, tag := range buildcfg.ToolTags {
		if !strings.HasPrefix(tag, "goexperiment.") && !slices.Contains(tags, tag) {
			tags = append(tags, tag)
		}
	}
	cfg.BuildContext.ToolTags = tags
}
