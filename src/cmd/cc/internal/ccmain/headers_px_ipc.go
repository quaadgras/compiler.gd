// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ccmain

// Interprocess communication: mqueue.h (internal/c23/mqueue), XSI's
// sys/ipc.h, sys/msg.h, sys/sem.h and sys/shm.h (internal/c23/sys/ipc
// and so on), and aio.h (internal/c23/aio), with struct sigevent's
// notifications in signal.h. The queues, semaphore sets and segments are
// the process's: other processes do not see them. The structures are
// Linux's (glibc's and musl's) layouts.

func init() {
	extendHeader("sys/types.h", `#ifndef __CC_KEY_T
#define __CC_KEY_T
typedef int key_t;
#endif
`)
	extendHeader("signal.h", `#define SI_MESGQ -3
#define SI_ASYNCIO -4
#ifndef __CC_SIGEVENT
#define __CC_SIGEVENT
#define SIGEV_SIGNAL 0
#define SIGEV_NONE 1
#define SIGEV_THREAD 2
#define SIGEV_THREAD_ID 4
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
`)
	for name, text := range ipcHeaders {
		sysHeaders[name] = text
	}
	for c, goName := range ipcLibc {
		libc[c] = goName
	}
}

var ipcLibc = map[string]string{
	"mq_open": "mqueue.MqOpen", "mq_close": "mqueue.MqClose", "mq_unlink": "mqueue.MqUnlink",
	"mq_send": "mqueue.MqSend", "mq_timedsend": "mqueue.MqTimedsend",
	"mq_receive": "mqueue.MqReceive", "mq_timedreceive": "mqueue.MqTimedreceive",
	"mq_notify": "mqueue.MqNotify", "mq_getattr": "mqueue.MqGetattr", "mq_setattr": "mqueue.MqSetattr",

	"ftok":   "sys/ipc.Ftok",
	"msgget": "sys/msg.Msgget", "msgsnd": "sys/msg.Msgsnd", "msgrcv": "sys/msg.Msgrcv", "msgctl": "sys/msg.Msgctl",
	"semget": "sys/sem.Semget", "semop": "sys/sem.Semop", "semtimedop": "sys/sem.Semtimedop", "semctl": "sys/sem.Semctl",
	"shmget": "sys/shm.Shmget", "shmat": "sys/shm.Shmat", "shmdt": "sys/shm.Shmdt", "shmctl": "sys/shm.Shmctl",

	"aio_read": "aio.AioRead", "aio_write": "aio.AioWrite", "aio_fsync": "aio.AioFsync",
	"aio_error": "aio.AioError", "aio_return": "aio.AioReturn", "aio_cancel": "aio.AioCancel",
	"aio_suspend": "aio.AioSuspend", "lio_listio": "aio.LioListio",
}

var ipcHeaders = map[string]string{
	"mqueue.h": `#ifndef __CC_MQUEUE_H
#define __CC_MQUEUE_H
#include <sys/types.h>
#include <fcntl.h>
#include <signal.h>
#include <time.h>
typedef int mqd_t;
struct mq_attr {
	long mq_flags;
	long mq_maxmsg;
	long mq_msgsize;
	long mq_curmsgs;
	long __pad[4];
};
#define MQ_PRIO_MAX 32768
mqd_t mq_open(const char *name, int oflag, ...);
int mq_close(mqd_t mqdes);
int mq_unlink(const char *name);
int mq_send(mqd_t mqdes, const char *msg_ptr, size_t msg_len, unsigned int msg_prio);
int mq_timedsend(mqd_t mqdes, const char *msg_ptr, size_t msg_len, unsigned int msg_prio, const struct timespec *abs_timeout);
ssize_t mq_receive(mqd_t mqdes, char *msg_ptr, size_t msg_len, unsigned int *msg_prio);
ssize_t mq_timedreceive(mqd_t mqdes, char *restrict msg_ptr, size_t msg_len, unsigned int *restrict msg_prio, const struct timespec *restrict abs_timeout);
int mq_notify(mqd_t mqdes, const struct sigevent *sevp);
int mq_getattr(mqd_t mqdes, struct mq_attr *attr);
int mq_setattr(mqd_t mqdes, const struct mq_attr *restrict newattr, struct mq_attr *restrict oldattr);
#endif
`,
	"sys/ipc.h": `#ifndef __CC_SYS_IPC_H
#define __CC_SYS_IPC_H
#include <sys/types.h>
#define IPC_CREAT 01000
#define IPC_EXCL 02000
#define IPC_NOWAIT 04000
#define IPC_RMID 0
#define IPC_SET 1
#define IPC_STAT 2
#define IPC_INFO 3
#define IPC_PRIVATE ((key_t)0)
struct ipc_perm {
	key_t __key;
	uid_t uid;
	gid_t gid;
	uid_t cuid;
	gid_t cgid;
	mode_t mode;
	unsigned short __seq;
	unsigned short __pad2;
	unsigned long __unused1;
	unsigned long __unused2;
};
key_t ftok(const char *pathname, int proj_id);
#endif
`,
	"sys/msg.h": `#ifndef __CC_SYS_MSG_H
#define __CC_SYS_MSG_H
#include <sys/ipc.h>
#include <time.h>
typedef unsigned long msgqnum_t;
typedef unsigned long msglen_t;
#define MSG_NOERROR 010000
#define MSG_EXCEPT 020000
#define MSG_COPY 040000
struct msqid_ds {
	struct ipc_perm msg_perm;
	time_t msg_stime;
	time_t msg_rtime;
	time_t msg_ctime;
	unsigned long __msg_cbytes;
	msgqnum_t msg_qnum;
	msglen_t msg_qbytes;
	pid_t msg_lspid;
	pid_t msg_lrpid;
	unsigned long __unused4;
	unsigned long __unused5;
};
#define msg_cbytes __msg_cbytes
struct msgbuf {
	long mtype;
	char mtext[1];
};
int msgget(key_t key, int msgflg);
int msgsnd(int msqid, const void *msgp, size_t msgsz, int msgflg);
ssize_t msgrcv(int msqid, void *msgp, size_t msgsz, long msgtyp, int msgflg);
int msgctl(int msqid, int cmd, struct msqid_ds *buf);
#endif
`,
	"sys/sem.h": `#ifndef __CC_SYS_SEM_H
#define __CC_SYS_SEM_H
#include <sys/ipc.h>
#include <time.h>
#define SEM_UNDO 0x1000
#define GETPID 11
#define GETVAL 12
#define GETALL 13
#define GETNCNT 14
#define GETZCNT 15
#define SETVAL 16
#define SETALL 17
struct semid_ds {
	struct ipc_perm sem_perm;
	time_t sem_otime;
	unsigned long __unused1;
	time_t sem_ctime;
	unsigned long __unused2;
	unsigned long sem_nsems;
	unsigned long __unused3;
	unsigned long __unused4;
};
struct sembuf {
	unsigned short sem_num;
	short sem_op;
	short sem_flg;
};
int semget(key_t key, int nsems, int semflg);
int semop(int semid, struct sembuf *sops, size_t nsops);
int semtimedop(int semid, struct sembuf *sops, size_t nsops, const struct timespec *timeout);
int semctl(int semid, int semnum, int cmd, ...);
#endif
`,
	"sys/shm.h": `#ifndef __CC_SYS_SHM_H
#define __CC_SYS_SHM_H
#include <sys/ipc.h>
#include <time.h>
#define SHM_RDONLY 010000
#define SHM_RND 020000
#define SHM_REMAP 040000
#define SHM_EXEC 0100000
#define SHM_LOCK 11
#define SHM_UNLOCK 12
#define SHM_DEST 01000
#define SHMLBA 4096
typedef unsigned long shmatt_t;
struct shmid_ds {
	struct ipc_perm shm_perm;
	size_t shm_segsz;
	time_t shm_atime;
	time_t shm_dtime;
	time_t shm_ctime;
	pid_t shm_cpid;
	pid_t shm_lpid;
	shmatt_t shm_nattch;
	unsigned long __unused5;
	unsigned long __unused6;
};
int shmget(key_t key, size_t size, int shmflg);
void *shmat(int shmid, const void *shmaddr, int shmflg);
int shmdt(const void *shmaddr);
int shmctl(int shmid, int cmd, struct shmid_ds *buf);
#endif
`,
	"aio.h": `#ifndef __CC_AIO_H
#define __CC_AIO_H
#include <sys/types.h>
#include <signal.h>
#include <time.h>
#define AIO_CANCELED 0
#define AIO_NOTCANCELED 1
#define AIO_ALLDONE 2
#define LIO_READ 0
#define LIO_WRITE 1
#define LIO_NOP 2
#define LIO_WAIT 0
#define LIO_NOWAIT 1
struct aiocb {
	int aio_fildes;
	int aio_lio_opcode;
	int aio_reqprio;
	volatile void *aio_buf;
	size_t aio_nbytes;
	struct sigevent aio_sigevent;
	struct aiocb *__next_prio;
	int __abs_prio;
	int __policy;
	int __error_code;
	ssize_t __return_value;
	off_t aio_offset;
	char __glibc_reserved[32];
};
#define aiocb64 aiocb
#define aio_read64 aio_read
#define aio_write64 aio_write
#define aio_fsync64 aio_fsync
#define aio_error64 aio_error
#define aio_return64 aio_return
#define aio_cancel64 aio_cancel
#define aio_suspend64 aio_suspend
#define lio_listio64 lio_listio
int aio_read(struct aiocb *aiocbp);
int aio_write(struct aiocb *aiocbp);
int aio_fsync(int op, struct aiocb *aiocbp);
int aio_error(const struct aiocb *aiocbp);
ssize_t aio_return(struct aiocb *aiocbp);
int aio_cancel(int fd, struct aiocb *aiocbp);
int aio_suspend(const struct aiocb *const list[], int nent, const struct timespec *restrict timeout);
int lio_listio(int mode, struct aiocb *const list[restrict], int nent, struct sigevent *restrict sevp);
#endif
`,
}
