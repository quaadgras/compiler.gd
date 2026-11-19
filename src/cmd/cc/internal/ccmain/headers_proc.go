// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

import "strings"

// Processes, signals, scheduling and semaphores: sys/wait.h, spawn.h,
// sched.h, semaphore.h, and POSIX's part of signal.h and unistd.h
// (internal/c23's proc.go, sigaction.go, sched.go and sem.go).

// extendHeader adds text to the end of header name, inside its include
// guard.
func extendHeader(name, text string) {
	h := sysHeaders[name]
	i := strings.LastIndex(h, "#endif")
	if i < 0 {
		panic("cmd/cc: " + name + " has no #endif")
	}
	sysHeaders[name] = h[:i] + text + h[i:]
}

func init() {
	extendHeader("signal.h", `#include <sys/types.h>
#define SIGHUP 1
#define SIGQUIT 3
#define SIGTRAP 5
#define SIGBUS 7
#define SIGKILL 9
#define SIGUSR1 10
#define SIGUSR2 12
#define SIGPIPE 13
#define SIGALRM 14
#define SIGSTKFLT 16
#define SIGCHLD 17
#define SIGCONT 18
#define SIGSTOP 19
#define SIGTSTP 20
#define SIGTTIN 21
#define SIGTTOU 22
#define SIGURG 23
#define SIGXCPU 24
#define SIGXFSZ 25
#define SIGVTALRM 26
#define SIGPROF 27
#define SIGWINCH 28
#define SIGIO 29
#define SIGPOLL SIGIO
#define SIGPWR 30
#define SIGSYS 31
#define SIGRTMIN 34
#define SIGRTMAX 64
#define NSIG 65
#define _NSIG 65
typedef struct { unsigned long __bits[128 / sizeof(long)]; } sigset_t;
union sigval { int sival_int; void *sival_ptr; };
typedef struct {
	int si_signo;
	int si_errno;
	int si_code;
	pid_t si_pid;
	uid_t si_uid;
	int si_status;
	void *si_addr;
	union sigval si_value;
	long si_band;
	int __pad[20];
} siginfo_t;
struct sigaction {
	union {
		void (*sa_handler)(int);
		void (*sa_sigaction)(int, siginfo_t *, void *);
	};
	sigset_t sa_mask;
	int sa_flags;
	void (*sa_restorer)(void);
};
#define SA_NOCLDSTOP 1
#define SA_NOCLDWAIT 2
#define SA_SIGINFO 4
#define SA_ONSTACK 0x08000000
#define SA_RESTART 0x10000000
#define SA_NODEFER 0x40000000
#define SA_RESETHAND 0x80000000
#define SIG_BLOCK 0
#define SIG_UNBLOCK 1
#define SIG_SETMASK 2
#define SI_USER 0
#define SI_KERNEL 0x80
#define SI_QUEUE -1
#define SI_TIMER -2
#define SI_TKILL -6
#define CLD_EXITED 1
#define CLD_KILLED 2
#define CLD_DUMPED 3
int sigaction(int sig, const struct sigaction *restrict act, struct sigaction *restrict oact);
int sigemptyset(sigset_t *set);
int sigfillset(sigset_t *set);
int sigaddset(sigset_t *set, int sig);
int sigdelset(sigset_t *set, int sig);
int sigismember(const sigset_t *set, int sig);
int sigprocmask(int how, const sigset_t *restrict set, sigset_t *restrict oset);
int pthread_sigmask(int how, const sigset_t *restrict set, sigset_t *restrict oset);
int sigpending(sigset_t *set);
int sigsuspend(const sigset_t *mask);
int sigwait(const sigset_t *restrict set, int *restrict sig);
int kill(pid_t pid, int sig);
void psignal(int sig, const char *s);
`)
	extendHeader("string.h", `char *strsignal(int sig);
`)
	extendHeader("unistd.h", `pid_t fork(void);
pid_t vfork(void);
int execv(const char *path, char *const argv[]);
int execve(const char *path, char *const argv[], char *const envp[]);
int execvp(const char *file, char *const argv[]);
int execvpe(const char *file, char *const argv[], char *const envp[]);
int execl(const char *path, const char *arg, ...);
int execlp(const char *file, const char *arg, ...);
int execle(const char *path, const char *arg, ...);
unsigned int alarm(unsigned int seconds);
int pause(void);
pid_t getpgrp(void);
pid_t getpgid(pid_t pid);
pid_t getsid(pid_t pid);
int setpgid(pid_t pid, pid_t pgid);
pid_t setsid(void);
`)
	sysHeaders["sys/wait.h"] = `#ifndef __CC_SYS_WAIT_H
#define __CC_SYS_WAIT_H
#include <sys/types.h>
#include <signal.h>
#define WNOHANG 1
#define WUNTRACED 2
#define WCONTINUED 8
#define WEXITSTATUS(s) (((s) & 0xff00) >> 8)
#define WTERMSIG(s) ((s) & 0x7f)
#define WSTOPSIG(s) WEXITSTATUS(s)
#define WCOREDUMP(s) ((s) & 0x80)
#define WIFEXITED(s) (WTERMSIG(s) == 0)
#define WIFSIGNALED(s) (WTERMSIG(s) != 0 && WTERMSIG(s) != 0x7f)
#define WIFSTOPPED(s) (((s) & 0xff) == 0x7f)
#define WIFCONTINUED(s) ((s) == 0xffff)
pid_t wait(int *status);
pid_t waitpid(pid_t pid, int *status, int options);
#endif
`
	sysHeaders["spawn.h"] = `#ifndef __CC_SPAWN_H
#define __CC_SPAWN_H
#include <sys/types.h>
#include <signal.h>
#include <sched.h>
typedef struct { long __a[8]; } posix_spawnattr_t;
typedef struct { long __a[8]; } posix_spawn_file_actions_t;
#define POSIX_SPAWN_RESETIDS 0x01
#define POSIX_SPAWN_SETPGROUP 0x02
#define POSIX_SPAWN_SETSIGDEF 0x04
#define POSIX_SPAWN_SETSIGMASK 0x08
#define POSIX_SPAWN_SETSCHEDPARAM 0x10
#define POSIX_SPAWN_SETSCHEDULER 0x20
#define POSIX_SPAWN_USEVFORK 0x40
#define POSIX_SPAWN_SETSID 0x80
int posix_spawn(pid_t *restrict pid, const char *restrict path, const posix_spawn_file_actions_t *file_actions, const posix_spawnattr_t *restrict attrp, char *const argv[restrict], char *const envp[restrict]);
int posix_spawnp(pid_t *restrict pid, const char *restrict file, const posix_spawn_file_actions_t *file_actions, const posix_spawnattr_t *restrict attrp, char *const argv[restrict], char *const envp[restrict]);
int posix_spawn_file_actions_init(posix_spawn_file_actions_t *file_actions);
int posix_spawn_file_actions_destroy(posix_spawn_file_actions_t *file_actions);
int posix_spawn_file_actions_addopen(posix_spawn_file_actions_t *restrict file_actions, int fd, const char *restrict path, int oflag, mode_t mode);
int posix_spawn_file_actions_addclose(posix_spawn_file_actions_t *file_actions, int fd);
int posix_spawn_file_actions_adddup2(posix_spawn_file_actions_t *file_actions, int fd, int newfd);
int posix_spawnattr_init(posix_spawnattr_t *attr);
int posix_spawnattr_destroy(posix_spawnattr_t *attr);
int posix_spawnattr_setflags(posix_spawnattr_t *attr, short flags);
int posix_spawnattr_getflags(const posix_spawnattr_t *restrict attr, short *restrict flags);
int posix_spawnattr_setpgroup(posix_spawnattr_t *attr, pid_t pgroup);
int posix_spawnattr_getpgroup(const posix_spawnattr_t *restrict attr, pid_t *restrict pgroup);
int posix_spawnattr_setsigmask(posix_spawnattr_t *restrict attr, const sigset_t *restrict sigmask);
int posix_spawnattr_getsigmask(const posix_spawnattr_t *restrict attr, sigset_t *restrict sigmask);
int posix_spawnattr_setsigdefault(posix_spawnattr_t *restrict attr, const sigset_t *restrict sigdefault);
int posix_spawnattr_getsigdefault(const posix_spawnattr_t *restrict attr, sigset_t *restrict sigdefault);
int posix_spawnattr_setschedpolicy(posix_spawnattr_t *attr, int policy);
int posix_spawnattr_getschedpolicy(const posix_spawnattr_t *restrict attr, int *restrict policy);
int posix_spawnattr_setschedparam(posix_spawnattr_t *restrict attr, const struct sched_param *restrict param);
int posix_spawnattr_getschedparam(const posix_spawnattr_t *restrict attr, struct sched_param *restrict param);
#endif
`
	sysHeaders["sched.h"] = `#ifndef __CC_SCHED_H
#define __CC_SCHED_H
#include <sys/types.h>
#include <time.h>
struct sched_param { int sched_priority; };
#define SCHED_OTHER 0
#define SCHED_FIFO 1
#define SCHED_RR 2
#define SCHED_BATCH 3
#define SCHED_IDLE 5
#define CPU_SETSIZE 1024
typedef struct { unsigned long __bits[CPU_SETSIZE / (8 * sizeof(long))]; } cpu_set_t;
#define __CPU_BITS (8 * sizeof(unsigned long))
static inline void __cc_cpu_zero(size_t n, cpu_set_t *s) {
	for (size_t i = 0; i < n / sizeof(unsigned long); i++)
		s->__bits[i] = 0;
}
static inline int __cc_cpu_count(size_t n, const cpu_set_t *s) {
	int c = 0;
	for (size_t i = 0; i < n / sizeof(unsigned long); i++)
		for (unsigned long b = s->__bits[i]; b; b &= b - 1)
			c++;
	return c;
}
#define CPU_ALLOC_SIZE(n) ((((n) + __CPU_BITS - 1) / __CPU_BITS) * sizeof(unsigned long))
#define CPU_ZERO_S(n, s) __cc_cpu_zero(n, s)
#define CPU_SET_S(c, n, s) ((size_t)(c) / 8 < (n) ? ((s)->__bits[(size_t)(c) / __CPU_BITS] |= 1UL << ((size_t)(c) % __CPU_BITS)) : 0)
#define CPU_CLR_S(c, n, s) ((size_t)(c) / 8 < (n) ? ((s)->__bits[(size_t)(c) / __CPU_BITS] &= ~(1UL << ((size_t)(c) % __CPU_BITS))) : 0)
#define CPU_ISSET_S(c, n, s) ((size_t)(c) / 8 < (n) ? (int)(((s)->__bits[(size_t)(c) / __CPU_BITS] >> ((size_t)(c) % __CPU_BITS)) & 1) : 0)
#define CPU_COUNT_S(n, s) __cc_cpu_count(n, s)
#define CPU_ZERO(s) CPU_ZERO_S(sizeof(cpu_set_t), s)
#define CPU_SET(c, s) CPU_SET_S(c, sizeof(cpu_set_t), s)
#define CPU_CLR(c, s) CPU_CLR_S(c, sizeof(cpu_set_t), s)
#define CPU_ISSET(c, s) CPU_ISSET_S(c, sizeof(cpu_set_t), s)
#define CPU_COUNT(s) CPU_COUNT_S(sizeof(cpu_set_t), s)
int sched_yield(void);
int sched_getcpu(void);
int sched_getaffinity(pid_t pid, size_t cpusetsize, cpu_set_t *mask);
int sched_setaffinity(pid_t pid, size_t cpusetsize, const cpu_set_t *mask);
int sched_get_priority_max(int policy);
int sched_get_priority_min(int policy);
int sched_getscheduler(pid_t pid);
int sched_getparam(pid_t pid, struct sched_param *param);
#endif
`
	sysHeaders["semaphore.h"] = `#ifndef __CC_SEMAPHORE_H
#define __CC_SEMAPHORE_H
#include <time.h>
#include <fcntl.h>
typedef struct { long __s[4]; } sem_t;
#define SEM_FAILED ((sem_t *)0)
#define SEM_VALUE_MAX 0x7fffffff
int sem_init(sem_t *sem, int pshared, unsigned int value);
int sem_destroy(sem_t *sem);
int sem_wait(sem_t *sem);
int sem_trywait(sem_t *sem);
int sem_timedwait(sem_t *restrict sem, const struct timespec *restrict abstime);
int sem_post(sem_t *sem);
int sem_getvalue(sem_t *restrict sem, int *restrict sval);
sem_t *sem_open(const char *name, int oflag, ...);
int sem_close(sem_t *sem);
int sem_unlink(const char *name);
#endif
`
	for k, v := range map[string]string{
		"sigaction": "signal.Sigaction", "sigemptyset": "signal.Sigemptyset", "sigfillset": "signal.Sigfillset",
		"sigaddset": "signal.Sigaddset", "sigdelset": "signal.Sigdelset", "sigismember": "signal.Sigismember",
		"sigprocmask": "signal.Sigprocmask", "pthread_sigmask": "signal.PthreadSigmask", "sigpending": "signal.Sigpending",
		"sigsuspend": "signal.Sigsuspend", "sigwait": "signal.Sigwait", "kill": "signal.Kill", "psignal": "signal.Psignal",
		"strsignal": "string.Strsignal",
		"fork":      "unistd.Fork", "vfork": "unistd.Fork", "execv": "unistd.Execv", "execve": "unistd.Execve", "execvp": "unistd.Execvp",
		"execvpe": "unistd.Execvpe", "execl": "unistd.Execl", "execlp": "unistd.Execlp", "execle": "unistd.Execle",
		"alarm": "unistd.Alarm", "pause": "unistd.Pause", "getpgrp": "unistd.Getpgrp", "getpgid": "unistd.Getpgid",
		"getsid": "unistd.Getsid", "setpgid": "unistd.Setpgid", "setsid": "unistd.Setsid",
		"wait": "sys/wait.Wait", "waitpid": "sys/wait.Waitpid",
		"posix_spawn": "spawn.PosixSpawn", "posix_spawnp": "spawn.PosixSpawnp",
		"posix_spawn_file_actions_init":     "spawn.PosixSpawnFileActionsInit",
		"posix_spawn_file_actions_destroy":  "spawn.PosixSpawnFileActionsDestroy",
		"posix_spawn_file_actions_addopen":  "spawn.PosixSpawnFileActionsAddopen",
		"posix_spawn_file_actions_addclose": "spawn.PosixSpawnFileActionsAddclose",
		"posix_spawn_file_actions_adddup2":  "spawn.PosixSpawnFileActionsAdddup2",
		"posix_spawnattr_init":              "spawn.PosixSpawnattrInit",
		"posix_spawnattr_destroy":           "spawn.PosixSpawnattrDestroy",
		"posix_spawnattr_setflags":          "spawn.PosixSpawnattrSetflags",
		"posix_spawnattr_getflags":          "spawn.PosixSpawnattrGetflags",
		"posix_spawnattr_setpgroup":         "spawn.PosixSpawnattrSetpgroup",
		"posix_spawnattr_getpgroup":         "spawn.PosixSpawnattrGetpgroup",
		"posix_spawnattr_setsigmask":        "spawn.PosixSpawnattrSetsigmask",
		"posix_spawnattr_getsigmask":        "spawn.PosixSpawnattrGetsigmask",
		"posix_spawnattr_setsigdefault":     "spawn.PosixSpawnattrSetsigdefault",
		"posix_spawnattr_getsigdefault":     "spawn.PosixSpawnattrGetsigdefault",
		"posix_spawnattr_setschedpolicy":    "spawn.PosixSpawnattrSetschedpolicy",
		"posix_spawnattr_getschedpolicy":    "spawn.PosixSpawnattrGetschedpolicy",
		"posix_spawnattr_setschedparam":     "spawn.PosixSpawnattrSetschedparam",
		"posix_spawnattr_getschedparam":     "spawn.PosixSpawnattrGetschedparam",
		"sched_getcpu":                      "sched.SchedGetcpu",
		"sched_getaffinity":                 "sched.SchedGetaffinity",
		"sched_setaffinity":                 "sched.SchedSetaffinity",
		"sched_get_priority_max":            "sched.SchedGetPriorityMax",
		"sched_get_priority_min":            "sched.SchedGetPriorityMin",
		"sched_getscheduler":                "sched.SchedGetscheduler",
		"sched_getparam":                    "sched.SchedGetparam",
		"sem_init":                          "semaphore.SemInit", "sem_destroy": "semaphore.SemDestroy", "sem_wait": "semaphore.SemWait",
		"sem_trywait": "semaphore.SemTrywait", "sem_timedwait": "semaphore.SemTimedwait", "sem_post": "semaphore.SemPost",
		"sem_getvalue": "semaphore.SemGetvalue", "sem_open": "semaphore.SemOpen", "sem_close": "semaphore.SemClose",
		"sem_unlink": "semaphore.SemUnlink",
	} {
		libc[k] = v
	}
}
