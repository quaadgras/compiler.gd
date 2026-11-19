// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package threads

import (
	c23 "internal/c23"
	c23_pthread "internal/c23/pthread"
	"runtime"
	"time"
)

const thrdSuccess = 0

const thrdBusy = 1

const thrdError = 2

const thrdNomem = 3

const thrdTimedout = 4

// thrdResult is the threads.h result for a pthread.h one.
func thrdResult(e int32) int32 {
	switch e {
	case 0:
		return thrdSuccess
	case c23.EBUSY:
		return thrdBusy
	case c23.ETIMEDOUT:
		return thrdTimedout
	case c23.ENOMEM:
		return thrdNomem
	}
	return thrdError
}

// ThrdCreate is thrd_create: start is an int (*)(void *).
func ThrdCreate(thr, start, arg c23.Ptr) int32 {
	f := c23.FuncOf[func(c23.Ptr) int32](start)
	c23.Spawn(thr, func(t *c23.Thread) { t.Res = f(arg) })
	return thrdSuccess
}

// ThrdExit is thrd_exit.
func ThrdExit(res int32) { panic(c23.ThreadExit{Res: res}) }

// ThrdJoin is thrd_join, which stores the thread's result at res unless
// it is null.
func ThrdJoin(id uint64, res c23.Ptr) int32 {
	t := c23.Join(id)
	if t == nil {
		return thrdError
	}
	if c23.Addr(res) != 0 {
		c23.Store32(res, uint32(t.Res))
	}
	return thrdSuccess
}

func ThrdDetach(id uint64) int32 { return thrdResult(c23.Detach(id)) }

// ThrdSleep is thrd_sleep, which is not interrupted: 0, or -2 for a
// duration that is not one.
func ThrdSleep(dur, rem c23.Ptr) int32 {
	sec, nsec := int64(c23.Load64(dur)), c23.LoadLong(c23.Add(dur, 8))
	if sec < 0 || nsec < 0 || nsec >= 1e9 {
		return -2
	}
	time.Sleep(time.Duration(sec)*time.Second + time.Duration(nsec))
	return 0
}

func ThrdYield() { runtime.Gosched() }

const mtxRecursive = 1

const mtxTimed = 2

// MtxInit is mtx_init: any mutex may be timed.
func MtxInit(m c23.Ptr, typ int32) int32 {
	if typ&^(mtxRecursive|mtxTimed) != 0 {
		return thrdError
	}
	c23.Check(m, 4)
	mu := new(c23.Mutex)
	if typ&mtxRecursive != 0 {
		mu.Kind = c23.PTHREAD_MUTEX_RECURSIVE
	}
	c23.Mutexes.Store(c23.Addr(m), mu)
	return thrdSuccess
}

func MtxDestroy(m c23.Ptr) { c23_pthread.PthreadMutexDestroy(m) }

func MtxLock(m c23.Ptr) int32 { return thrdResult(c23_pthread.PthreadMutexLock(m)) }

func MtxTrylock(m c23.Ptr) int32 { return thrdResult(c23_pthread.PthreadMutexTrylock(m)) }

func MtxTimedlock(m, ts c23.Ptr) int32 { return thrdResult(c23_pthread.PthreadMutexTimedlock(m, ts)) }

func MtxUnlock(m c23.Ptr) int32 { return thrdResult(c23_pthread.PthreadMutexUnlock(m)) }

func CndInit(c c23.Ptr) int32 { c23.Check(c, 4); return thrdSuccess }

func CndDestroy(c c23.Ptr) { c23_pthread.PthreadCondDestroy(c) }

func CndSignal(c c23.Ptr) int32 { c23_pthread.PthreadCondSignal(c); return thrdSuccess }

func CndBroadcast(c c23.Ptr) int32 { c23_pthread.PthreadCondBroadcast(c); return thrdSuccess }

func CndWait(c, m c23.Ptr) int32 { return thrdResult(c23_pthread.PthreadCondWait(c, m)) }

func CndTimedwait(c, m, ts c23.Ptr) int32 {
	return thrdResult(c23_pthread.PthreadCondTimedwait(c, m, ts))
}

func TssCreate(k, dtor c23.Ptr) int32 { return thrdResult(c23_pthread.PthreadKeyCreate(k, dtor)) }

func TssDelete(k uint32) { c23_pthread.PthreadKeyDelete(k) }

func TssGet(k uint32) c23.Ptr { return c23_pthread.PthreadGetspecific(k) }

func TssSet(k uint32, v c23.Ptr) int32 { return thrdResult(c23_pthread.PthreadSetspecific(k, v)) }
