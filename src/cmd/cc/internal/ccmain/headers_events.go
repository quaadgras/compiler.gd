// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "strings"

// Events: sys/epoll.h, sys/eventfd.h, sys/timerfd.h and sys/signalfd.h
// (internal/c23/sys/epoll and so on, descriptors that are c23.Specials),
// and the rest of waiting and signals: waitid, wait3 and wait4,
// sigtimedwait and its kin, pthread_kill, sigaltstack, and setitimer.

// initEvents runs at the end of headers_proc.go's init, as it extends
// the headers that defines.
func initEvents() {
	sysHeaders["pthread.h"] = strings.Replace(sysHeaders["pthread.h"], "typedef unsigned long pthread_t;\n",
		"#ifndef __CC_PTHREAD_T\n#define __CC_PTHREAD_T\ntypedef unsigned long pthread_t;\n#endif\n", 1)
	extendHeader("sys/types.h", `#ifndef __CC_ID_T
#define __CC_ID_T
typedef unsigned int id_t;
#endif
`)
	extendHeader("time.h", `#define CLOCK_BOOTTIME 7
struct itimerspec {
	struct timespec it_interval;
	struct timespec it_value;
};
`)
	extendHeader("signal.h", `#include <time.h>
#define SS_ONSTACK 1
#define SS_DISABLE 2
#define MINSIGSTKSZ 2048
#define SIGSTKSZ 8192
typedef struct sigaltstack {
	void *ss_sp;
	int ss_flags;
	size_t ss_size;
} stack_t;
#ifndef __CC_PTHREAD_T
#define __CC_PTHREAD_T
typedef unsigned long pthread_t;
#endif
int sigtimedwait(const sigset_t *restrict set, siginfo_t *restrict info, const struct timespec *restrict timeout);
int sigwaitinfo(const sigset_t *restrict set, siginfo_t *restrict info);
int __cc_sigqueue(pid_t pid, int sig, const union sigval *value);
static inline int sigqueue(pid_t pid, int sig, const union sigval value) { return __cc_sigqueue(pid, sig, &value); }
int pthread_kill(pthread_t thread, int sig);
int sigaltstack(const stack_t *restrict ss, stack_t *restrict old_ss);
int siginterrupt(int sig, int flag);
`)
	extendHeader("sys/wait.h", `#define WSTOPPED 2
#define WEXITED 4
#define WNOWAIT 0x1000000
typedef enum { P_ALL, P_PID, P_PGID, P_PIDFD } idtype_t;
int waitid(idtype_t idtype, id_t id, siginfo_t *infop, int options);
struct rusage;
pid_t wait3(int *status, int options, struct rusage *rusage);
pid_t wait4(pid_t pid, int *status, int options, struct rusage *rusage);
`)
	extendHeader("sys/time.h", `struct itimerval {
	struct timeval it_interval;
	struct timeval it_value;
};
#define ITIMER_REAL 0
#define ITIMER_VIRTUAL 1
#define ITIMER_PROF 2
int getitimer(int which, struct itimerval *curr_value);
int setitimer(int which, const struct itimerval *restrict new_value, struct itimerval *restrict old_value);
`)
	for name, text := range eventHeaders {
		sysHeaders[name] = text
	}
	for c, goName := range eventLibc {
		libc[c] = goName
	}
}

var eventLibc = map[string]string{
	"epoll_create": "sys/epoll.EpollCreate", "epoll_create1": "sys/epoll.EpollCreate1",
	"epoll_ctl": "sys/epoll.EpollCtl", "epoll_wait": "sys/epoll.EpollWait", "epoll_pwait": "sys/epoll.EpollPwait",
	"eventfd": "sys/eventfd.Eventfd", "eventfd_read": "sys/eventfd.EventfdRead", "eventfd_write": "sys/eventfd.EventfdWrite",
	"timerfd_create": "sys/timerfd.TimerfdCreate", "timerfd_settime": "sys/timerfd.TimerfdSettime",
	"timerfd_gettime": "sys/timerfd.TimerfdGettime",
	"signalfd":        "sys/signalfd.Signalfd",
	"waitid":          "sys/wait.Waitid", "wait3": "sys/wait.Wait3", "wait4": "sys/wait.Wait4",
	"sigtimedwait": "signal.Sigtimedwait", "sigwaitinfo": "signal.Sigwaitinfo", "__cc_sigqueue": "signal.Sigqueue",
	"pthread_kill": "signal.PthreadKill", "sigaltstack": "signal.Sigaltstack", "siginterrupt": "signal.Siginterrupt",
	"getitimer": "sys/time.Getitimer", "setitimer": "sys/time.Setitimer",
}

var eventHeaders = map[string]string{
	// struct epoll_event is 16 bytes everywhere: not packed on x86-64 as
	// Linux's (12 bytes), where its pointer would be unaligned, and an
	// unaligned pointer has no capability.
	"sys/epoll.h": `#ifndef __CC_SYS_EPOLL_H
#define __CC_SYS_EPOLL_H
#include <stdint.h>
#include <signal.h>
#define EPOLL_CLOEXEC 02000000
#define EPOLLIN 0x001
#define EPOLLPRI 0x002
#define EPOLLOUT 0x004
#define EPOLLERR 0x008
#define EPOLLHUP 0x010
#define EPOLLRDNORM 0x040
#define EPOLLRDBAND 0x080
#define EPOLLWRNORM 0x100
#define EPOLLWRBAND 0x200
#define EPOLLMSG 0x400
#define EPOLLRDHUP 0x2000
#define EPOLLEXCLUSIVE (1U << 28)
#define EPOLLWAKEUP (1U << 29)
#define EPOLLONESHOT (1U << 30)
#define EPOLLET (1U << 31)
#define EPOLL_CTL_ADD 1
#define EPOLL_CTL_DEL 2
#define EPOLL_CTL_MOD 3
typedef union epoll_data {
	void *ptr;
	int fd;
	uint32_t u32;
	uint64_t u64;
} epoll_data_t;
struct epoll_event {
	uint32_t events;
	epoll_data_t data;
};
int epoll_create(int size);
int epoll_create1(int flags);
int epoll_ctl(int epfd, int op, int fd, struct epoll_event *event);
int epoll_wait(int epfd, struct epoll_event *events, int maxevents, int timeout);
int epoll_pwait(int epfd, struct epoll_event *events, int maxevents, int timeout, const sigset_t *sigmask);
#endif
`,
	"sys/eventfd.h": `#ifndef __CC_SYS_EVENTFD_H
#define __CC_SYS_EVENTFD_H
#include <stdint.h>
typedef uint64_t eventfd_t;
#define EFD_SEMAPHORE 1
#define EFD_CLOEXEC 02000000
#define EFD_NONBLOCK 04000
int eventfd(unsigned int initval, int flags);
int eventfd_read(int fd, eventfd_t *value);
int eventfd_write(int fd, eventfd_t value);
#endif
`,
	"sys/timerfd.h": `#ifndef __CC_SYS_TIMERFD_H
#define __CC_SYS_TIMERFD_H
#include <time.h>
#define TFD_NONBLOCK 04000
#define TFD_CLOEXEC 02000000
#define TFD_TIMER_ABSTIME 1
#define TFD_TIMER_CANCEL_ON_SET 2
int timerfd_create(int clockid, int flags);
int timerfd_settime(int fd, int flags, const struct itimerspec *new_value, struct itimerspec *old_value);
int timerfd_gettime(int fd, struct itimerspec *curr_value);
#endif
`,
	"sys/signalfd.h": `#ifndef __CC_SYS_SIGNALFD_H
#define __CC_SYS_SIGNALFD_H
#include <stdint.h>
#include <signal.h>
#define SFD_CLOEXEC 02000000
#define SFD_NONBLOCK 04000
struct signalfd_siginfo {
	uint32_t ssi_signo;
	int32_t ssi_errno;
	int32_t ssi_code;
	uint32_t ssi_pid;
	uint32_t ssi_uid;
	int32_t ssi_fd;
	uint32_t ssi_tid;
	uint32_t ssi_band;
	uint32_t ssi_overrun;
	uint32_t ssi_trapno;
	int32_t ssi_status;
	int32_t ssi_int;
	uint64_t ssi_ptr;
	uint64_t ssi_utime;
	uint64_t ssi_stime;
	uint64_t ssi_addr;
	uint16_t ssi_addr_lsb;
	uint16_t __pad2;
	int32_t ssi_syscall;
	uint64_t ssi_call_addr;
	uint32_t ssi_arch;
	uint8_t __pad[28];
};
int signalfd(int fd, const sigset_t *mask, int flags);
#endif
`,
}
