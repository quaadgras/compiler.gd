// Interprocess communication within one process, threads as the other
// parties: mqueue.h, XSI message queues, semaphores and shared memory,
// and aio.h. TestPxIPC compares the output with the host C library's.
// Linux's objects are the system's, so the names have the pid in them,
// the keys are IPC_PRIVATE, and everything is removed at the end.

#define _GNU_SOURCE
#include <aio.h>
#include <errno.h>
#include <fcntl.h>
#include <mqueue.h>
#include <poll.h>
#include <pthread.h>
#include <semaphore.h>
#include <signal.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ipc.h>
#include <sys/msg.h>
#include <sys/sem.h>
#include <sys/shm.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

static void sleepms(int ms) {
	struct timespec ts = {ms / 1000, (ms % 1000) * 1000000L};
	nanosleep(&ts, NULL);
}

static const char *err(int e) {
	switch (e) {
	case 0: return "0";
	case EAGAIN: return "EAGAIN";
	case EINVAL: return "EINVAL";
	case EEXIST: return "EEXIST";
	case ENOENT: return "ENOENT";
	case EMSGSIZE: return "EMSGSIZE";
	case ETIMEDOUT: return "ETIMEDOUT";
	case EBUSY: return "EBUSY";
	case EBADF: return "EBADF";
	case ENOMSG: return "ENOMSG";
	case E2BIG: return "E2BIG";
	case EFBIG: return "EFBIG";
	case ERANGE: return "ERANGE";
	case EIDRM: return "EIDRM";
	case EINPROGRESS: return "EINPROGRESS";
	case ECANCELED: return "ECANCELED";
	}
	static char buf[32];
	snprintf(buf, sizeof buf, "errno %d", e);
	return buf;
}

#define TRY(what, call) do { errno = 0; long r_ = (long)(call); printf("%s: %ld %s\n", what, r_, r_ < 0 ? err(errno) : "ok"); } while (0)

// mqueue.h

static char qname[64];
static mqd_t mq;
static sem_t notified;
static int notifiedValue;

static void *mqSender(void *arg) {
	sleepms(30);
	mq_send(mq, "fromthread", 10, 2);
	return NULL;
}

static void mqNotifyThread(union sigval v) {
	notifiedValue = v.sival_int;
	sem_post(&notified);
}

static void testMqueue(void) {
	printf("-- mqueue\n");
	snprintf(qname, sizeof qname, "/pxipc-mq-%d", (int)getpid());
	mq_unlink(qname);
	struct mq_attr attr = {0};
	attr.mq_maxmsg = 4;
	attr.mq_msgsize = 64;
	mq = mq_open(qname, O_CREAT | O_EXCL | O_RDWR | O_NONBLOCK, 0600, &attr);
	printf("open: %s\n", mq >= 0 ? "ok" : err(errno));
	TRY("open excl", mq_open(qname, O_CREAT | O_EXCL | O_RDWR, 0600, &attr));
	attr.mq_maxmsg = 0;
	TRY("open bad attr", mq_open("/pxipc-bad", O_CREAT | O_RDWR, 0600, &attr));
	struct mq_attr got;
	mq_getattr(mq, &got);
	printf("attr: flags %ld maxmsg %ld msgsize %ld curmsgs %ld\n", got.mq_flags, got.mq_maxmsg, got.mq_msgsize, got.mq_curmsgs);
	struct pollfd pfd = {mq, POLLIN | POLLOUT, 0};
	int np = poll(&pfd, 1, 0);
	printf("poll empty: %d revents %#x\n", np, pfd.revents);

	TRY("send low", mq_send(mq, "low", 3, 1));
	TRY("send high", mq_send(mq, "high", 4, 5));
	TRY("send mid1", mq_send(mq, "mid1", 4, 3));
	TRY("send mid2", mq_send(mq, "mid2", 4, 3));
	TRY("send full", mq_send(mq, "x", 1, 0));
	TRY("send big", mq_send(mq, "x", 65, 0));
	TRY("send prio", mq_send(mq, "x", 1, 32768));
	pfd.revents = 0;
	np = poll(&pfd, 1, 0);
	printf("poll full: %d revents %#x\n", np, pfd.revents);
	mq_getattr(mq, &got);
	printf("curmsgs %ld\n", got.mq_curmsgs);
	char buf[128];
	unsigned prio;
	TRY("receive small", mq_receive(mq, buf, 10, &prio));
	for (int i = 0; i < 4; i++) {
		ssize_t n = mq_receive(mq, buf, sizeof buf, &prio);
		printf("receive: %.*s prio %u\n", (int)n, buf, prio);
	}
	TRY("receive empty", mq_receive(mq, buf, sizeof buf, &prio));

	struct mq_attr nb = {0}, old;
	TRY("setattr", mq_setattr(mq, &nb, &old));
	mq_getattr(mq, &got);
	printf("flags old %ld new %ld\n", old.mq_flags, got.mq_flags);
	nb.mq_flags = 1;
	TRY("setattr bad", mq_setattr(mq, &nb, NULL));

	struct timespec ts;
	clock_gettime(CLOCK_REALTIME, &ts);
	ts.tv_nsec += 50000000;
	if (ts.tv_nsec >= 1000000000) {
		ts.tv_sec++;
		ts.tv_nsec -= 1000000000;
	}
	TRY("timedreceive", mq_timedreceive(mq, buf, sizeof buf, &prio, &ts));
	ts.tv_nsec = 2000000000;
	TRY("timedreceive bad", mq_timedreceive(mq, buf, sizeof buf, &prio, &ts));

	pthread_t th;
	pthread_create(&th, NULL, mqSender, NULL);
	ssize_t n = mq_receive(mq, buf, sizeof buf, &prio);
	printf("blocking receive: %.*s prio %u\n", (int)n, buf, prio);
	pthread_join(th, NULL);

	// SIGEV_THREAD
	sem_init(&notified, 0, 0);
	struct sigevent sev;
	memset(&sev, 0, sizeof sev);
	sev.sigev_notify = SIGEV_THREAD;
	sev.sigev_notify_function = mqNotifyThread;
	sev.sigev_value.sival_int = 42;
	TRY("notify thread", mq_notify(mq, &sev));
	TRY("notify again", mq_notify(mq, &sev));
	mq_send(mq, "n1", 2, 0);
	sem_wait(&notified);
	printf("notified by thread: %d\n", notifiedValue);
	n = mq_receive(mq, buf, sizeof buf, &prio);
	printf("receive: %.*s\n", (int)n, buf);

	// SIGEV_SIGNAL, taken by sigwaitinfo.
	sigset_t set;
	sigemptyset(&set);
	sigaddset(&set, SIGUSR1);
	pthread_sigmask(SIG_BLOCK, &set, NULL);
	memset(&sev, 0, sizeof sev);
	sev.sigev_notify = SIGEV_SIGNAL;
	sev.sigev_signo = SIGUSR1;
	sev.sigev_value.sival_int = 7;
	TRY("notify signal", mq_notify(mq, &sev));
	mq_send(mq, "n2", 2, 0);
	siginfo_t si;
	int sig = sigwaitinfo(&set, &si);
	printf("signal %d code %s value %d pid %d\n", sig == SIGUSR1, si.si_code == SI_MESGQ ? "SI_MESGQ" : "other", si.si_value.sival_int, si.si_pid == getpid());
	mq_receive(mq, buf, sizeof buf, &prio);
	TRY("notify remove", mq_notify(mq, NULL));
	sev.sigev_signo = 0;
	sev.sigev_notify = 99;
	TRY("notify bad", mq_notify(mq, &sev));

	TRY("close", mq_close(mq));
	TRY("close again", mq_close(mq));
	TRY("unlink", mq_unlink(qname));
	TRY("unlink again", mq_unlink(qname));
	TRY("open gone", mq_open(qname, O_RDWR));
}

// sys/msg.h

struct mymsg {
	long mtype;
	char mtext[32];
};

static int msqid;

static void *msgSender(void *arg) {
	sleepms(30);
	struct mymsg m = {7, "late"};
	msgsnd(msqid, &m, 4, 0);
	return NULL;
}

static void testMsg(void) {
	printf("-- msg\n");
	msqid = msgget(IPC_PRIVATE, IPC_CREAT | 0600);
	printf("msgget: %s\n", msqid >= 0 ? "ok" : err(errno));
	struct mymsg m;
	struct { long t; const char *s; } in[] = {{3, "c1"}, {1, "a"}, {2, "b"}, {3, "c2"}};
	for (int i = 0; i < 4; i++) {
		m.mtype = in[i].t;
		strcpy(m.mtext, in[i].s);
		TRY("msgsnd", msgsnd(msqid, &m, strlen(in[i].s), 0));
	}
	m.mtype = 0;
	TRY("msgsnd type 0", msgsnd(msqid, &m, 1, 0));
	struct msqid_ds ds;
	msgctl(msqid, IPC_STAT, &ds);
	printf("stat: qnum %lu cbytes %lu qbytes %lu lspid %d mode %o\n", (unsigned long)ds.msg_qnum, (unsigned long)ds.__msg_cbytes, (unsigned long)ds.msg_qbytes, ds.msg_lspid == getpid(), ds.msg_perm.mode & 0777);
	long types[] = {3, -2, 0, 3};
	for (int i = 0; i < 4; i++) {
		memset(&m, 0, sizeof m);
		ssize_t n = msgrcv(msqid, &m, sizeof m.mtext, types[i], 0);
		printf("msgrcv %ld: %zd type %ld %s\n", types[i], n, m.mtype, m.mtext);
	}
	TRY("msgrcv nowait", msgrcv(msqid, &m, sizeof m.mtext, 5, IPC_NOWAIT));
	m.mtype = 9;
	strcpy(m.mtext, "0123456789");
	msgsnd(msqid, &m, 10, 0);
	TRY("msgrcv small", msgrcv(msqid, &m, 4, 0, 0));
	memset(&m, 0, sizeof m);
	TRY("msgrcv noerror", msgrcv(msqid, &m, 4, 0, MSG_NOERROR));
	printf("cut: %s\n", m.mtext);
	m.mtype = 4;
	msgsnd(msqid, &m, 1, 0);
	m.mtype = 5;
	msgsnd(msqid, &m, 1, 0);
	TRY("msgrcv except", msgrcv(msqid, &m, sizeof m.mtext, 4, MSG_EXCEPT));
	printf("except type %ld\n", m.mtype);
	msgrcv(msqid, &m, sizeof m.mtext, 0, 0);

	pthread_t th;
	pthread_create(&th, NULL, msgSender, NULL);
	memset(&m, 0, sizeof m);
	ssize_t n = msgrcv(msqid, &m, sizeof m.mtext, 7, 0);
	printf("blocking msgrcv: %zd type %ld %s\n", n, m.mtype, m.mtext);
	pthread_join(th, NULL);

	TRY("rmid", msgctl(msqid, IPC_RMID, NULL));
	TRY("msgsnd removed", msgsnd(msqid, &m, 1, 0));
	TRY("msgget missing", msgget(IPC_PRIVATE + 0x7a7a0000 + getpid() % 1000, 0600));
}

// sys/sem.h

union semun {
	int val;
	struct semid_ds *buf;
	unsigned short *array;
};

static int semid;

static void *semWaiter(void *arg) {
	struct sembuf op = {1, -1, 0};
	semop(semid, &op, 1);
	return (void *)1;
}

static void testSem(void) {
	printf("-- sem\n");
	semid = semget(IPC_PRIVATE, 2, IPC_CREAT | 0600);
	printf("semget: %s\n", semid >= 0 ? "ok" : err(errno));
	TRY("semget zero", semget(IPC_PRIVATE, 0, IPC_CREAT | 0600));
	union semun arg;
	arg.val = 1;
	TRY("setval", semctl(semid, 0, SETVAL, arg));
	TRY("getval", semctl(semid, 0, GETVAL));
	arg.val = 40000;
	TRY("setval range", semctl(semid, 0, SETVAL, arg));
	unsigned short vals[2] = {2, 0};
	arg.array = vals;
	TRY("setall", semctl(semid, 0, SETALL, arg));
	vals[0] = vals[1] = 99;
	TRY("getall", semctl(semid, 0, GETALL, arg));
	printf("values %d %d\n", vals[0], vals[1]);
	TRY("getpid", semctl(semid, 0, GETPID) == getpid());

	struct sembuf op = {1, -1, IPC_NOWAIT};
	TRY("semop nowait", semop(semid, &op, 1));
	struct sembuf two[2] = {{0, -1, 0}, {1, -1, IPC_NOWAIT}};
	TRY("semop atomic", semop(semid, two, 2));
	TRY("still 2", semctl(semid, 0, GETVAL));
	op.sem_num = 5;
	TRY("semop efbig", semop(semid, &op, 1));

	pthread_t th;
	pthread_create(&th, NULL, semWaiter, NULL);
	sleepms(50);
	TRY("ncnt", semctl(semid, 1, GETNCNT));
	struct sembuf post = {1, 1, 0};
	TRY("post", semop(semid, &post, 1));
	void *ret;
	pthread_join(th, &ret);
	printf("waiter got it: %ld\n", (long)ret);
	TRY("ncnt after", semctl(semid, 1, GETNCNT));

	struct sembuf undo = {0, -2, SEM_UNDO};
	TRY("semop undo", semop(semid, &undo, 1));
	TRY("val", semctl(semid, 0, GETVAL));
	struct sembuf zero = {0, 0, IPC_NOWAIT};
	TRY("wait zero", semop(semid, &zero, 1));
	struct timespec ts = {0, 30000000};
	struct sembuf take = {1, -1, 0};
	TRY("semtimedop", semtimedop(semid, &take, 1, &ts));
	struct sembuf over = {0, 32767, 0};
	TRY("semop add", semop(semid, &over, 1));
	over.sem_op = 1;
	TRY("semop range", semop(semid, &over, 1));

	struct semid_ds ds;
	arg.buf = &ds;
	TRY("stat", semctl(semid, 0, IPC_STAT, arg));
	printf("nsems %lu mode %o\n", (unsigned long)ds.sem_nsems, ds.sem_perm.mode & 0777);
	ds.sem_perm.mode = 0640;
	TRY("set", semctl(semid, 0, IPC_SET, arg));
	semctl(semid, 0, IPC_STAT, arg);
	printf("mode %o\n", ds.sem_perm.mode & 0777);
	TRY("rmid", semctl(semid, 0, IPC_RMID));
	TRY("semop removed", semop(semid, &post, 1));
}

// sys/shm.h

static void testShm(void) {
	printf("-- shm\n");
	int id = shmget(IPC_PRIVATE, 4096, IPC_CREAT | 0600);
	printf("shmget: %s\n", id >= 0 ? "ok" : err(errno));
	TRY("shmget zero", shmget(IPC_PRIVATE, 0, IPC_CREAT | 0600));
	char *p = shmat(id, NULL, 0);
	char *q = shmat(id, NULL, 0);
	printf("attached: %d %d\n", p != (void *)-1, q != (void *)-1);
	printf("zeroed: %d\n", p[0] == 0 && p[4095] == 0);
	strcpy(p, "shared bytes");
	printf("seen: %s\n", q);
	struct shmid_ds ds;
	TRY("stat", shmctl(id, IPC_STAT, &ds));
	printf("segsz %zu nattch %lu cpid %d mode %o\n", ds.shm_segsz, (unsigned long)ds.shm_nattch, ds.shm_cpid == getpid(), ds.shm_perm.mode & 0777);
	TRY("shmdt", shmdt(p));
	TRY("rmid", shmctl(id, IPC_RMID, NULL));
	shmctl(id, IPC_STAT, &ds);
	printf("nattch %lu dest %d\n", (unsigned long)ds.shm_nattch, (ds.shm_perm.mode & SHM_DEST) != 0);
	printf("still: %s\n", q);
	TRY("shmdt last", shmdt(q));
	TRY("stat gone", shmctl(id, IPC_STAT, &ds));
	int x;
	TRY("shmdt bad", shmdt(&x));

	FILE *f = fopen("ftokfile", "w");
	fclose(f);
	struct stat st;
	stat("ftokfile", &st);
	key_t k = ftok("ftokfile", 'A');
	printf("ftok: %d\n", k == (key_t)((st.st_ino & 0xffff) | ((st.st_dev & 0xff) << 16) | ('A' << 24)));
	TRY("ftok missing", ftok("no-such-file", 1));
	unlink("ftokfile");
}

// aio.h

static sem_t aioDone;
static int aioValue;

static void aioThread(union sigval v) {
	aioValue = v.sival_int;
	sem_post(&aioDone);
}

static void waitFor(struct aiocb *cb) {
	const struct aiocb *list[1] = {cb};
	while (aio_error(cb) == EINPROGRESS)
		aio_suspend(list, 1, NULL);
}

static void testAio(void) {
	printf("-- aio\n");
	int fd = open("aiofile", O_RDWR | O_CREAT | O_TRUNC, 0600);
	struct aiocb w;
	memset(&w, 0, sizeof w);
	w.aio_fildes = fd;
	w.aio_buf = "hello aio world";
	w.aio_nbytes = 15;
	w.aio_sigevent.sigev_notify = SIGEV_NONE;
	TRY("aio_write", aio_write(&w));
	waitFor(&w);
	printf("write: error %s return %zd\n", err(aio_error(&w)), aio_return(&w));

	char buf[64] = {0};
	struct aiocb r;
	memset(&r, 0, sizeof r);
	r.aio_fildes = fd;
	r.aio_buf = buf;
	r.aio_nbytes = 3;
	r.aio_offset = 6;
	TRY("aio_read", aio_read(&r));
	waitFor(&r);
	printf("read: error %s return %zd %s\n", err(aio_error(&r)), aio_return(&r), buf);
	TRY("cancel done", aio_cancel(fd, &r));
	TRY("cancel none", aio_cancel(fd, NULL));

	struct aiocb a, b, nop;
	memset(&a, 0, sizeof a);
	memset(&b, 0, sizeof b);
	memset(&nop, 0, sizeof nop);
	a.aio_fildes = b.aio_fildes = nop.aio_fildes = fd;
	a.aio_lio_opcode = LIO_WRITE;
	a.aio_buf = "AAAA";
	a.aio_nbytes = 4;
	a.aio_offset = 20;
	b.aio_lio_opcode = LIO_WRITE;
	b.aio_buf = "BBBB";
	b.aio_nbytes = 4;
	b.aio_offset = 30;
	nop.aio_lio_opcode = LIO_NOP;
	struct aiocb *list[4] = {&a, NULL, &nop, &b};
	TRY("lio_listio wait", lio_listio(LIO_WAIT, list, 4, NULL));
	printf("returns %zd %zd\n", aio_return(&a), aio_return(&b));
	memset(buf, 0, sizeof buf);
	pread(fd, buf, 34, 0);
	for (int i = 0; i < 34; i++)
		putchar(buf[i] ? buf[i] : '.');
	putchar('\n');

	sem_init(&aioDone, 0, 0);
	struct sigevent sev;
	memset(&sev, 0, sizeof sev);
	sev.sigev_notify = SIGEV_THREAD;
	sev.sigev_notify_function = aioThread;
	sev.sigev_value.sival_int = 31;
	memset(buf, 0, sizeof buf);
	a.aio_lio_opcode = LIO_READ;
	a.aio_buf = buf;
	b.aio_lio_opcode = LIO_READ;
	b.aio_buf = buf + 8;
	TRY("lio_listio nowait", lio_listio(LIO_NOWAIT, list, 4, &sev));
	sem_wait(&aioDone);
	printf("list notified: %d %s %s\n", aioValue, buf, buf + 8);
	aio_return(&a);
	aio_return(&b);

	// SIGEV_SIGNAL, taken by sigwaitinfo.
	sigset_t set;
	sigemptyset(&set);
	sigaddset(&set, SIGUSR2);
	pthread_sigmask(SIG_BLOCK, &set, NULL);
	memset(&w, 0, sizeof w);
	w.aio_fildes = fd;
	w.aio_buf = "!";
	w.aio_nbytes = 1;
	w.aio_offset = 40;
	w.aio_sigevent.sigev_notify = SIGEV_SIGNAL;
	w.aio_sigevent.sigev_signo = SIGUSR2;
	w.aio_sigevent.sigev_value.sival_int = 77;
	TRY("aio_write signal", aio_write(&w));
	siginfo_t si;
	int sig = sigwaitinfo(&set, &si);
	printf("signal %d code %s value %d\n", sig == SIGUSR2, si.si_code == SI_ASYNCIO ? "SI_ASYNCIO" : "other", si.si_value.sival_int);
	waitFor(&w);
	printf("return %zd\n", aio_return(&w));

	memset(&w, 0, sizeof w);
	w.aio_fildes = fd;
	TRY("aio_fsync", aio_fsync(O_SYNC, &w));
	waitFor(&w);
	printf("fsync: error %s return %zd\n", err(aio_error(&w)), aio_return(&w));
	TRY("aio_fsync bad", aio_fsync(12345, &w));

	// A read of a pipe waits for its writer.
	int pp[2];
	pipe(pp);
	memset(&r, 0, sizeof r);
	memset(buf, 0, sizeof buf);
	r.aio_fildes = pp[0];
	r.aio_buf = buf;
	r.aio_nbytes = 5;
	TRY("aio_read pipe", aio_read(&r));
	const struct aiocb *rl[2] = {NULL, &r};
	struct timespec ts = {0, 30000000};
	TRY("suspend timeout", aio_suspend(rl, 2, &ts));
	printf("in progress: %s\n", err(aio_error(&r)));
	write(pp[1], "piped", 5);
	TRY("suspend", aio_suspend(rl, 2, NULL));
	waitFor(&r);
	printf("pipe read: %zd %s\n", aio_return(&r), buf);
	close(pp[0]);
	close(pp[1]);

	// A read of a closed descriptor fails when it runs.
	memset(&r, 0, sizeof r);
	r.aio_fildes = fd;
	r.aio_buf = buf;
	r.aio_nbytes = 1;
	r.aio_offset = -1;
	TRY("aio_read negative offset", aio_read(&r));
	waitFor(&r);
	printf("error %s return %zd\n", err(aio_error(&r)), aio_return(&r));
	close(fd);
	unlink("aiofile");
}

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	printf("sizes: mq_attr %zu ipc_perm %zu msqid_ds %zu semid_ds %zu shmid_ds %zu sembuf %zu aiocb %zu sigevent %zu\n",
		sizeof(struct mq_attr), sizeof(struct ipc_perm), sizeof(struct msqid_ds), sizeof(struct semid_ds),
		sizeof(struct shmid_ds), sizeof(struct sembuf), sizeof(struct aiocb), sizeof(struct sigevent));
	printf("offsets: aio_buf %zu aio_sigevent %zu aio_offset %zu notify_function %zu msg_qnum %zu shm_nattch %zu sem_nsems %zu\n",
		offsetof(struct aiocb, aio_buf), offsetof(struct aiocb, aio_sigevent), offsetof(struct aiocb, aio_offset),
		offsetof(struct sigevent, sigev_notify_function), offsetof(struct msqid_ds, msg_qnum),
		offsetof(struct shmid_ds, shm_nattch), offsetof(struct semid_ds, sem_nsems));
	testMqueue();
	testMsg();
	testSem();
	testShm();
	testAio();
	return 0;
}
