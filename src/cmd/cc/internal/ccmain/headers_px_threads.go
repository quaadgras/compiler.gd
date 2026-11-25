// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "strings"

// The rest of POSIX's threads, timers and scheduling: pthread.h's
// attributes, read-write locks, barriers, spin locks, cancellation and
// cleanup handlers, robust mutexes and scheduling (internal/c23/pthread,
// and internal/c23's cancel.go); time.h's timer_create and its kin,
// clock_nanosleep, clock_settime and clock_getcpuclockid
// (internal/c23/time); sched.h's sched_setparam, sched_setscheduler and
// sched_rr_get_interval; signal.h's struct sigevent, killpg, psiginfo
// and the System V signal functions; and setjmp.h's sigsetjmp.

// This runs after headers_proc.go's init (files initialize in order),
// as it extends the headers that defines.
func init() {
	const oldAttr = "typedef struct { int __a; } pthread_attr_t;\n"
	if !strings.Contains(sysHeaders["pthread.h"], oldAttr) {
		panic("cmd/cc: pthread.h has no pthread_attr_t")
	}
	sysHeaders["pthread.h"] = strings.Replace(sysHeaders["pthread.h"], oldAttr,
		"typedef struct { void *__p; unsigned long __l[2]; int __i[6]; } pthread_attr_t;\n", 1)
	const oldSetjmp = "#define sigsetjmp(env, save) __builtin_setjmp(env)\n"
	if !strings.Contains(sysHeaders["setjmp.h"], oldSetjmp) {
		panic("cmd/cc: setjmp.h has no sigsetjmp")
	}
	sysHeaders["setjmp.h"] = strings.Replace(sysHeaders["setjmp.h"], oldSetjmp,
		"#define sigsetjmp(env, save) __builtin_sigsetjmp(env, save)\n", 1)
	extendHeader("limits.h", `#define PTHREAD_STACK_MIN 16384
`)
	extendHeader("pthread.h", `#include <sched.h>
typedef struct { long __r[7]; } pthread_rwlock_t;
typedef struct { unsigned __a[2]; } pthread_rwlockattr_t;
typedef struct { long __b[4]; } pthread_barrier_t;
typedef struct { unsigned __a; } pthread_barrierattr_t;
typedef int pthread_spinlock_t;
#define PTHREAD_RWLOCK_INITIALIZER {{0}}
#define PTHREAD_CREATE_JOINABLE 0
#define PTHREAD_CREATE_DETACHED 1
#define PTHREAD_MUTEX_STALLED 0
#define PTHREAD_MUTEX_ROBUST 1
#define PTHREAD_PRIO_NONE 0
#define PTHREAD_PRIO_INHERIT 1
#define PTHREAD_PRIO_PROTECT 2
#define PTHREAD_INHERIT_SCHED 0
#define PTHREAD_EXPLICIT_SCHED 1
#define PTHREAD_SCOPE_SYSTEM 0
#define PTHREAD_SCOPE_PROCESS 1
#define PTHREAD_PROCESS_PRIVATE 0
#define PTHREAD_PROCESS_SHARED 1
#define PTHREAD_CANCEL_ENABLE 0
#define PTHREAD_CANCEL_DISABLE 1
#define PTHREAD_CANCEL_DEFERRED 0
#define PTHREAD_CANCEL_ASYNCHRONOUS 1
#define PTHREAD_CANCELED ((void *)(~0UL & ~(1UL << 31) & ~(1UL << (sizeof(void *) == 8 ? 55 : 31))))
#define PTHREAD_BARRIER_SERIAL_THREAD (-1)
#define PTHREAD_NULL ((pthread_t)0)
int pthread_attr_init(pthread_attr_t *attr);
int pthread_attr_destroy(pthread_attr_t *attr);
int pthread_attr_getdetachstate(const pthread_attr_t *attr, int *detachstate);
int pthread_attr_setdetachstate(pthread_attr_t *attr, int detachstate);
int pthread_attr_getguardsize(const pthread_attr_t *restrict attr, size_t *restrict guardsize);
int pthread_attr_setguardsize(pthread_attr_t *attr, size_t guardsize);
int pthread_attr_getinheritsched(const pthread_attr_t *restrict attr, int *restrict inheritsched);
int pthread_attr_setinheritsched(pthread_attr_t *attr, int inheritsched);
int pthread_attr_getschedparam(const pthread_attr_t *restrict attr, struct sched_param *restrict param);
int pthread_attr_setschedparam(pthread_attr_t *restrict attr, const struct sched_param *restrict param);
int pthread_attr_getschedpolicy(const pthread_attr_t *restrict attr, int *restrict policy);
int pthread_attr_setschedpolicy(pthread_attr_t *attr, int policy);
int pthread_attr_getscope(const pthread_attr_t *restrict attr, int *restrict contentionscope);
int pthread_attr_setscope(pthread_attr_t *attr, int contentionscope);
int pthread_attr_getstack(const pthread_attr_t *restrict attr, void **restrict stackaddr, size_t *restrict stacksize);
int pthread_attr_setstack(pthread_attr_t *attr, void *stackaddr, size_t stacksize);
int pthread_attr_getstacksize(const pthread_attr_t *restrict attr, size_t *restrict stacksize);
int pthread_attr_setstacksize(pthread_attr_t *attr, size_t stacksize);
int pthread_barrier_init(pthread_barrier_t *restrict barrier, const pthread_barrierattr_t *restrict attr, unsigned count);
int pthread_barrier_destroy(pthread_barrier_t *barrier);
int pthread_barrier_wait(pthread_barrier_t *barrier);
int pthread_barrierattr_init(pthread_barrierattr_t *attr);
int pthread_barrierattr_destroy(pthread_barrierattr_t *attr);
int pthread_barrierattr_getpshared(const pthread_barrierattr_t *restrict attr, int *restrict pshared);
int pthread_barrierattr_setpshared(pthread_barrierattr_t *attr, int pshared);
int pthread_rwlock_init(pthread_rwlock_t *restrict rwlock, const pthread_rwlockattr_t *restrict attr);
int pthread_rwlock_destroy(pthread_rwlock_t *rwlock);
int pthread_rwlock_rdlock(pthread_rwlock_t *rwlock);
int pthread_rwlock_tryrdlock(pthread_rwlock_t *rwlock);
int pthread_rwlock_timedrdlock(pthread_rwlock_t *restrict rwlock, const struct timespec *restrict abstime);
int pthread_rwlock_wrlock(pthread_rwlock_t *rwlock);
int pthread_rwlock_trywrlock(pthread_rwlock_t *rwlock);
int pthread_rwlock_timedwrlock(pthread_rwlock_t *restrict rwlock, const struct timespec *restrict abstime);
int pthread_rwlock_unlock(pthread_rwlock_t *rwlock);
int pthread_rwlockattr_init(pthread_rwlockattr_t *attr);
int pthread_rwlockattr_destroy(pthread_rwlockattr_t *attr);
int pthread_rwlockattr_getpshared(const pthread_rwlockattr_t *restrict attr, int *restrict pshared);
int pthread_rwlockattr_setpshared(pthread_rwlockattr_t *attr, int pshared);
int pthread_spin_init(pthread_spinlock_t *lock, int pshared);
int pthread_spin_destroy(pthread_spinlock_t *lock);
int pthread_spin_lock(pthread_spinlock_t *lock);
int pthread_spin_trylock(pthread_spinlock_t *lock);
int pthread_spin_unlock(pthread_spinlock_t *lock);
int pthread_condattr_init(pthread_condattr_t *attr);
int pthread_condattr_destroy(pthread_condattr_t *attr);
int pthread_condattr_getclock(const pthread_condattr_t *restrict attr, clockid_t *restrict clock_id);
int pthread_condattr_setclock(pthread_condattr_t *attr, clockid_t clock_id);
int pthread_condattr_getpshared(const pthread_condattr_t *restrict attr, int *restrict pshared);
int pthread_condattr_setpshared(pthread_condattr_t *attr, int pshared);
int pthread_mutexattr_getprioceiling(const pthread_mutexattr_t *restrict attr, int *restrict prioceiling);
int pthread_mutexattr_setprioceiling(pthread_mutexattr_t *attr, int prioceiling);
int pthread_mutexattr_getprotocol(const pthread_mutexattr_t *restrict attr, int *restrict protocol);
int pthread_mutexattr_setprotocol(pthread_mutexattr_t *attr, int protocol);
int pthread_mutexattr_getpshared(const pthread_mutexattr_t *restrict attr, int *restrict pshared);
int pthread_mutexattr_setpshared(pthread_mutexattr_t *attr, int pshared);
int pthread_mutexattr_getrobust(const pthread_mutexattr_t *restrict attr, int *restrict robust);
int pthread_mutexattr_setrobust(pthread_mutexattr_t *attr, int robust);
int pthread_mutex_consistent(pthread_mutex_t *mutex);
int pthread_mutex_getprioceiling(const pthread_mutex_t *restrict mutex, int *restrict prioceiling);
int pthread_mutex_setprioceiling(pthread_mutex_t *restrict mutex, int prioceiling, int *restrict old_ceiling);
int pthread_cancel(pthread_t thread);
int pthread_setcancelstate(int state, int *oldstate);
int pthread_setcanceltype(int type, int *oldtype);
void pthread_testcancel(void);
struct __ptcb {
	void (*__f)(void *);
	void *__x;
	struct __ptcb *__next;
};
void _pthread_cleanup_push(struct __ptcb *cb, void (*routine)(void *), void *arg);
void _pthread_cleanup_pop(struct __ptcb *cb, int execute);
#define pthread_cleanup_push(f, x) do { struct __ptcb __cb; _pthread_cleanup_push(&__cb, f, x);
#define pthread_cleanup_pop(r) _pthread_cleanup_pop(&__cb, (r)); } while (0)
int pthread_atfork(void (*prepare)(void), void (*parent)(void), void (*child)(void));
int pthread_getconcurrency(void);
int pthread_setconcurrency(int new_level);
int pthread_getschedparam(pthread_t thread, int *restrict policy, struct sched_param *restrict param);
int pthread_setschedparam(pthread_t thread, int policy, const struct sched_param *param);
int pthread_setschedprio(pthread_t thread, int prio);
int pthread_getcpuclockid(pthread_t thread, clockid_t *clock_id);
`)
	extendHeader("time.h", `#include <sys/types.h>
typedef void *timer_t;
struct sigevent;
#define TIMER_ABSTIME 1
int timer_create(clockid_t clockid, struct sigevent *restrict evp, timer_t *restrict timerid);
int timer_delete(timer_t timerid);
int timer_settime(timer_t timerid, int flags, const struct itimerspec *restrict value, struct itimerspec *restrict ovalue);
int timer_gettime(timer_t timerid, struct itimerspec *value);
int timer_getoverrun(timer_t timerid);
int clock_nanosleep(clockid_t clock_id, int flags, const struct timespec *rqtp, struct timespec *rmtp);
int clock_settime(clockid_t clock_id, const struct timespec *tp);
int clock_getcpuclockid(pid_t pid, clockid_t *clock_id);
`)
	// struct sigevent is glibc's, of 64 bytes; the IPC headers define
	// it too, under the same guard.
	extendHeader("signal.h", `#ifndef __CC_SIGEVENT
#define __CC_SIGEVENT
#define SIGEV_SIGNAL 0
#define SIGEV_NONE 1
#define SIGEV_THREAD 2
typedef struct sigevent {
	union sigval sigev_value;
	int sigev_signo;
	int sigev_notify;
	void (*sigev_notify_function)(union sigval);
	void *sigev_notify_attributes;
	pid_t sigev_notify_thread_id;
	char __pad[64 - sizeof(union sigval) - 2 * sizeof(int) - 2 * sizeof(void *) - sizeof(pid_t)];
} sigevent_t;
#endif
#define SIG_HOLD ((void (*)(int))2)
int killpg(pid_t pgrp, int sig);
void psiginfo(const siginfo_t *pinfo, const char *message);
int sighold(int sig);
int sigrelse(int sig);
int sigignore(int sig);
int sigpause(int sig);
void (*sigset(int sig, void (*disp)(int)))(int);
`)
	extendHeader("sched.h", `int sched_setparam(pid_t pid, const struct sched_param *param);
int sched_setscheduler(pid_t pid, int policy, const struct sched_param *param);
int sched_rr_get_interval(pid_t pid, struct timespec *interval);
`)
	for k, v := range map[string]string{
		"pthread_attr_init": "pthread.PthreadAttrInit", "pthread_attr_destroy": "pthread.PthreadAttrDestroy",
		"pthread_attr_getdetachstate": "pthread.PthreadAttrGetdetachstate", "pthread_attr_setdetachstate": "pthread.PthreadAttrSetdetachstate",
		"pthread_attr_getguardsize": "pthread.PthreadAttrGetguardsize", "pthread_attr_setguardsize": "pthread.PthreadAttrSetguardsize",
		"pthread_attr_getinheritsched": "pthread.PthreadAttrGetinheritsched", "pthread_attr_setinheritsched": "pthread.PthreadAttrSetinheritsched",
		"pthread_attr_getschedparam": "pthread.PthreadAttrGetschedparam", "pthread_attr_setschedparam": "pthread.PthreadAttrSetschedparam",
		"pthread_attr_getschedpolicy": "pthread.PthreadAttrGetschedpolicy", "pthread_attr_setschedpolicy": "pthread.PthreadAttrSetschedpolicy",
		"pthread_attr_getscope": "pthread.PthreadAttrGetscope", "pthread_attr_setscope": "pthread.PthreadAttrSetscope",
		"pthread_attr_getstack": "pthread.PthreadAttrGetstack", "pthread_attr_setstack": "pthread.PthreadAttrSetstack",
		"pthread_attr_getstacksize": "pthread.PthreadAttrGetstacksize", "pthread_attr_setstacksize": "pthread.PthreadAttrSetstacksize",
		"pthread_barrier_init": "pthread.PthreadBarrierInit", "pthread_barrier_destroy": "pthread.PthreadBarrierDestroy",
		"pthread_barrier_wait":     "pthread.PthreadBarrierWait",
		"pthread_barrierattr_init": "pthread.PthreadBarrierattrInit", "pthread_barrierattr_destroy": "pthread.PthreadBarrierattrDestroy",
		"pthread_barrierattr_getpshared": "pthread.PthreadBarrierattrGetpshared", "pthread_barrierattr_setpshared": "pthread.PthreadBarrierattrSetpshared",
		"pthread_rwlock_init": "pthread.PthreadRwlockInit", "pthread_rwlock_destroy": "pthread.PthreadRwlockDestroy",
		"pthread_rwlock_rdlock": "pthread.PthreadRwlockRdlock", "pthread_rwlock_tryrdlock": "pthread.PthreadRwlockTryrdlock",
		"pthread_rwlock_timedrdlock": "pthread.PthreadRwlockTimedrdlock", "pthread_rwlock_wrlock": "pthread.PthreadRwlockWrlock",
		"pthread_rwlock_trywrlock": "pthread.PthreadRwlockTrywrlock", "pthread_rwlock_timedwrlock": "pthread.PthreadRwlockTimedwrlock",
		"pthread_rwlock_unlock":   "pthread.PthreadRwlockUnlock",
		"pthread_rwlockattr_init": "pthread.PthreadRwlockattrInit", "pthread_rwlockattr_destroy": "pthread.PthreadRwlockattrDestroy",
		"pthread_rwlockattr_getpshared": "pthread.PthreadRwlockattrGetpshared", "pthread_rwlockattr_setpshared": "pthread.PthreadRwlockattrSetpshared",
		"pthread_spin_init": "pthread.PthreadSpinInit", "pthread_spin_destroy": "pthread.PthreadSpinDestroy",
		"pthread_spin_lock": "pthread.PthreadSpinLock", "pthread_spin_trylock": "pthread.PthreadSpinTrylock",
		"pthread_spin_unlock":   "pthread.PthreadSpinUnlock",
		"pthread_condattr_init": "pthread.PthreadCondattrInit", "pthread_condattr_destroy": "pthread.PthreadCondattrDestroy",
		"pthread_condattr_getclock": "pthread.PthreadCondattrGetclock", "pthread_condattr_setclock": "pthread.PthreadCondattrSetclock",
		"pthread_condattr_getpshared": "pthread.PthreadCondattrGetpshared", "pthread_condattr_setpshared": "pthread.PthreadCondattrSetpshared",
		"pthread_mutexattr_getprioceiling": "pthread.PthreadMutexattrGetprioceiling", "pthread_mutexattr_setprioceiling": "pthread.PthreadMutexattrSetprioceiling",
		"pthread_mutexattr_getprotocol": "pthread.PthreadMutexattrGetprotocol", "pthread_mutexattr_setprotocol": "pthread.PthreadMutexattrSetprotocol",
		"pthread_mutexattr_getpshared": "pthread.PthreadMutexattrGetpshared", "pthread_mutexattr_setpshared": "pthread.PthreadMutexattrSetpshared",
		"pthread_mutexattr_getrobust": "pthread.PthreadMutexattrGetrobust", "pthread_mutexattr_setrobust": "pthread.PthreadMutexattrSetrobust",
		"pthread_mutex_consistent": "pthread.PthreadMutexConsistent", "pthread_mutex_getprioceiling": "pthread.PthreadMutexGetprioceiling",
		"pthread_mutex_setprioceiling": "pthread.PthreadMutexSetprioceiling",
		"pthread_cancel":               "pthread.PthreadCancel", "pthread_setcancelstate": "pthread.PthreadSetcancelstate",
		"pthread_setcanceltype": "pthread.PthreadSetcanceltype", "pthread_testcancel": "pthread.PthreadTestcancel",
		"_pthread_cleanup_push": "pthread.PthreadCleanupPush", "_pthread_cleanup_pop": "pthread.PthreadCleanupPop",
		"pthread_atfork": "pthread.PthreadAtfork", "pthread_getconcurrency": "pthread.PthreadGetconcurrency",
		"pthread_setconcurrency": "pthread.PthreadSetconcurrency", "pthread_getschedparam": "pthread.PthreadGetschedparam",
		"pthread_setschedparam": "pthread.PthreadSetschedparam", "pthread_setschedprio": "pthread.PthreadSetschedprio",
		"pthread_getcpuclockid": "pthread.PthreadGetcpuclockid",
		"timer_create":          "time.TimerCreate", "timer_delete": "time.TimerDelete", "timer_settime": "time.TimerSettime",
		"timer_gettime": "time.TimerGettime", "timer_getoverrun": "time.TimerGetoverrun",
		"clock_nanosleep": "time.ClockNanosleep", "clock_settime": "time.ClockSettime",
		"clock_getcpuclockid": "time.ClockGetcpuclockid",
		"killpg":              "signal.Killpg", "psiginfo": "signal.Psiginfo", "sighold": "signal.Sighold",
		"sigrelse": "signal.Sigrelse", "sigignore": "signal.Sigignore", "sigpause": "signal.Sigpause",
		"sigset":         "signal.Sigset",
		"sched_setparam": "sched.SchedSetparam", "sched_setscheduler": "sched.SchedSetscheduler",
		"sched_rr_get_interval": "sched.SchedRrGetInterval",
	} {
		libc[k] = v
	}
}
