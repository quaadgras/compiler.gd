// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !ape && !apegen

package c23

import (
	"runtime"
	"time"
)

// <threads.h>: C's threads are pthread.h's (goroutines), with its own
// results.

// threads.h's results (glibc's and musl's values).
const (
	thrdSuccess  = 0
	thrdBusy     = 1
	thrdError    = 2
	thrdNomem    = 3
	thrdTimedout = 4
)

// thrdResult is the threads.h result for a pthread.h one.
func thrdResult(e int32) int32 {
	switch e {
	case 0:
		return thrdSuccess
	case EBUSY:
		return thrdBusy
	case ETIMEDOUT:
		return thrdTimedout
	case ENOMEM:
		return thrdNomem
	}
	return thrdError
}

// ThrdCreate is thrd_create: start is an int (*)(void *).
func ThrdCreate(thr, start, arg Ptr) int32 {
	f := FuncOf[func(Ptr) int32](start)
	spawn(thr, func(t *thread) { t.res = f(arg) })
	return thrdSuccess
}

// ThrdExit is thrd_exit.
func ThrdExit(res int32) { panic(threadExit{res: res}) }

// ThrdJoin is thrd_join, which stores the thread's result at res unless
// it is null.
func ThrdJoin(id uint64, res Ptr) int32 {
	t := join(id)
	if t == nil {
		return thrdError
	}
	if Addr(res) != 0 {
		Store32(res, uint32(t.res))
	}
	return thrdSuccess
}

func ThrdDetach(id uint64) int32 {
	threadsMu.Lock()
	_, ok := threads[id]
	delete(threads, id)
	threadsMu.Unlock()
	return If[int32](ok, thrdSuccess, thrdError)
}

// ThrdSleep is thrd_sleep, which is not interrupted: 0, or -2 for a
// duration that is not one.
func ThrdSleep(dur, rem Ptr) int32 {
	sec, nsec := int64(Load64(dur)), loadLong(Add(dur, 8))
	if sec < 0 || nsec < 0 || nsec >= 1e9 {
		return -2
	}
	time.Sleep(time.Duration(sec)*time.Second + time.Duration(nsec))
	return 0
}

func ThrdYield() { runtime.Gosched() }

// mtx_init's types.
const (
	mtxPlain     = 0
	mtxRecursive = 1
	mtxTimed     = 2
)

// MtxInit is mtx_init: any mutex may be timed.
func MtxInit(m Ptr, typ int32) int32 {
	if typ&^(mtxRecursive|mtxTimed) != 0 {
		return thrdError
	}
	check(m, 4)
	mu := new(mutex)
	if typ&mtxRecursive != 0 {
		mu.kind = PTHREAD_MUTEX_RECURSIVE
	}
	mutexes.Store(Addr(m), mu)
	return thrdSuccess
}

func MtxDestroy(m Ptr)             { PthreadMutexDestroy(m) }
func MtxLock(m Ptr) int32          { return thrdResult(PthreadMutexLock(m)) }
func MtxTrylock(m Ptr) int32       { return thrdResult(PthreadMutexTrylock(m)) }
func MtxTimedlock(m, ts Ptr) int32 { return thrdResult(PthreadMutexTimedlock(m, ts)) }
func MtxUnlock(m Ptr) int32        { return thrdResult(PthreadMutexUnlock(m)) }

func CndInit(c Ptr) int32             { check(c, 4); return thrdSuccess }
func CndDestroy(c Ptr)                { PthreadCondDestroy(c) }
func CndSignal(c Ptr) int32           { PthreadCondSignal(c); return thrdSuccess }
func CndBroadcast(c Ptr) int32        { PthreadCondBroadcast(c); return thrdSuccess }
func CndWait(c, m Ptr) int32          { return thrdResult(PthreadCondWait(c, m)) }
func CndTimedwait(c, m, ts Ptr) int32 { return thrdResult(PthreadCondTimedwait(c, m, ts)) }

// Thread-specific storage: tss_t is a pthread_key_t.

func TssCreate(k, dtor Ptr) int32  { return thrdResult(PthreadKeyCreate(k, dtor)) }
func TssDelete(k uint32)           { PthreadKeyDelete(k) }
func TssGet(k uint32) Ptr          { return PthreadGetspecific(k) }
func TssSet(k uint32, v Ptr) int32 { return thrdResult(PthreadSetspecific(k, v)) }
