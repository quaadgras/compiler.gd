// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasm.gc

package runtime

import "unsafe"

// What packages os, syscall, internal/poll and internal/synctest ask of
// the runtime.

var (
	argslice []string
	envs     []string
	gotArgs  bool
)

// hostArgs reads the arguments and the environment from WASI.
func hostArgs() {
	if gotArgs {
		return
	}
	gotArgs = true
	argslice = wasiStrings(args_sizes_get, args_get)
	envs = wasiStrings(environ_sizes_get, environ_get)
}

func wasiStrings(sizes func(n, size unsafe.Pointer) uint32, get func(ptrs, buf unsafe.Pointer) uint32) []string {
	ns := make([]uint32, 2)
	if sizes(unsafe.Pointer(&ns[0]), unsafe.Pointer(&ns[1])) != 0 || ns[0] == 0 {
		return nil
	}
	ptrs := make([]uint32, ns[0])
	buf := make([]byte, ns[1]+1)
	if get(unsafe.Pointer(&ptrs[0]), unsafe.Pointer(&buf[0])) != 0 {
		return nil
	}
	base := uint32(uintptr(unsafe.Pointer(&buf[0])))
	out := make([]string, len(ptrs))
	for i, p := range ptrs {
		j := p - base
		k := j
		for k < ns[1] && buf[k] != 0 {
			k++
		}
		out[i] = string(buf[j:k])
	}
	return out
}

//go:wasmimport wasi_snapshot_preview1 args_sizes_get
func args_sizes_get(n, size unsafe.Pointer) uint32

//go:wasmimport wasi_snapshot_preview1 args_get
func args_get(ptrs, buf unsafe.Pointer) uint32

//go:wasmimport wasi_snapshot_preview1 environ_sizes_get
func environ_sizes_get(n, size unsafe.Pointer) uint32

//go:wasmimport wasi_snapshot_preview1 environ_get
func environ_get(ptrs, buf unsafe.Pointer) uint32

//go:linkname os_runtime_args os.runtime_args
func os_runtime_args() []string {
	hostArgs()
	return append([]string{}, argslice...)
}

//go:linkname syscall_runtime_envs syscall.runtime_envs
func syscall_runtime_envs() []string {
	hostArgs()
	return append([]string{}, envs...)
}

//go:linkname syscall_runtimeSetenv syscall.runtimeSetenv
func syscall_runtimeSetenv(key, value string) {
	if key == "GODEBUG" {
		p := new(string)
		*p = value
		setGodebugEnv(p)
	}
}

//go:linkname syscall_runtimeUnsetenv syscall.runtimeUnsetenv
func syscall_runtimeUnsetenv(key string) {
	if key == "GODEBUG" {
		setGodebugEnv(new(string))
	}
}

//go:linkname syscall_runtimeClearenv syscall.runtimeClearenv
func syscall_runtimeClearenv(env map[string]int) {
	setGodebugEnv(new(string))
}

// setGodebugEnv records GODEBUG as the program sets it, which the
// runtime reads from then on (godebugEnv), and tells internal/godebug.
func setGodebugEnv(p *string) {
	godebugEnvSet = p
	if godebugUpdate != nil {
		godebugUpdate(godebugDefault, *p)
	}
}

// godebugEnvSet is GODEBUG once the program has set it.
var godebugEnvSet *string

//go:linkname os_beforeExit os.runtime_beforeExit
func os_beforeExit(exitCode int) {}

//go:linkname os_sigpipe os.sigpipe
func os_sigpipe() {}

//go:linkname syscall_Exit syscall.Exit
func syscall_Exit(code int) { exit(int32(code)) }

//go:linkname syscall_Getpagesize syscall.Getpagesize
func syscall_Getpagesize() int { return 64 << 10 }

// The poller: there is none, so every file blocks.

//go:linkname poll_runtime_pollServerInit internal/poll.runtime_pollServerInit
func poll_runtime_pollServerInit() {}

//go:linkname poll_runtime_pollOpen internal/poll.runtime_pollOpen
func poll_runtime_pollOpen(fd uintptr) (uintptr, int) { return 0, 58 } // ENOTSUP

//go:linkname poll_runtime_pollClose internal/poll.runtime_pollClose
func poll_runtime_pollClose(ctx uintptr) {}

//go:linkname poll_runtime_pollWait internal/poll.runtime_pollWait
func poll_runtime_pollWait(ctx uintptr, mode int) int { return 0 }

//go:linkname poll_runtime_pollWaitCanceled internal/poll.runtime_pollWaitCanceled
func poll_runtime_pollWaitCanceled(ctx uintptr, mode int) {}

//go:linkname poll_runtime_pollReset internal/poll.runtime_pollReset
func poll_runtime_pollReset(ctx uintptr, mode int) int { return 0 }

//go:linkname poll_runtime_pollSetDeadline internal/poll.runtime_pollSetDeadline
func poll_runtime_pollSetDeadline(ctx uintptr, d int64, mode int) {}

//go:linkname poll_runtime_pollUnblock internal/poll.runtime_pollUnblock
func poll_runtime_pollUnblock(ctx uintptr) {}

//go:linkname poll_runtime_isPollServerDescriptor internal/poll.runtime_isPollServerDescriptor
func poll_runtime_isPollServerDescriptor(fd uintptr) bool { return false }

// Random numbers.

var randState uint64

//go:linkname rand
func rand() uint64 {
	if randState == 0 {
		randState = uint64(nanotime()) | 1
	}
	// xorshift64*
	x := randState
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	randState = x
	return x * 2685821657736338717
}

//go:linkname bootstrapRand
func bootstrapRand() uint64 { return rand() }

//go:linkname maps_rand internal/runtime/maps.rand
func maps_rand() uint64 { return rand() }

// stringDataHeap returns a pointer to the bytes of s that stays valid:
// those of a short string are copied out of its words.
func stringDataHeap(s string) *byte {
	sh := (*stringStruct)(unsafe.Pointer(&s))
	if sh.str != nil {
		return (*byte)(sh.str)
	}
	n := len(s)
	if n == 0 {
		return nil
	}
	b := make([]byte, n)
	copy(b, s)
	return &b[0]
}

// resolveForwardedRecvFieldMask is the escape mask of a method called
// through an interface in a field: here everything escapes.
func resolveForwardedRecvFieldMask(carrier unsafe.Pointer, fieldOffset uintptr, methodIdx int, heapMask uint64, depth int) uint64 {
	return ^uint64(1)
}

//go:linkname reflectcall
func reflectcall(stackArgsType *_type, fn, stackArgs unsafe.Pointer, stackArgsSize, stackRetOffset, frameSize uint32, regArgs unsafe.Pointer) {
	fatal("reflectcall is not supported with GOWASM=gc")
}

// What packages of crypto ask of the runtime.

// (There is no data-independent timing mode to set.)
//
//go:linkname subtle_setDITEnabled crypto/subtle.setDITEnabled
func subtle_setDITEnabled() bool { return false }

//go:linkname subtle_setDITDisabled crypto/subtle.setDITDisabled
func subtle_setDITDisabled() {}

//go:linkname fips140deps_monoTime crypto/internal/fips140deps/time.monoTime
func fips140deps_monoTime() int64 { return nanotime() }

//go:linkname boring_registerCache crypto/internal/boring/bcache.registerCache
func boring_registerCache(p unsafe.Pointer) {
	boringCaches = append(boringCaches, p)
}

// boringCaches are the caches of crypto/internal/boring/bcache, which a
// collection clears (see GC).
var boringCaches []unsafe.Pointer
