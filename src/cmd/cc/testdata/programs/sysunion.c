// The unions of the system's headers that have a pointer in a member: an
// epoll_data_t, a union sigval. Each member is stored and read where
// the union is a local, a local that a pointer is to, an object of
// malloc's and a global.
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/epoll.h>
#include <unistd.h>

static struct epoll_event gev;
static union sigval gsv;

static void set(struct epoll_event *e, int i) {
	switch (i % 4) {
	case 0: e->data.fd = 40 + i; break;
	case 1: e->data.u32 = 0x80000000u + i; break;
	case 2: e->data.u64 = 0xfedcba9876543210ull + i; break;
	case 3: e->data.ptr = e; break;
	}
}

static void show(const char *what, struct epoll_event *e, int i) {
	switch (i % 4) {
	case 0: printf("%s fd %d\n", what, e->data.fd); break;
	case 1: printf("%s u32 %u\n", what, e->data.u32); break;
	case 2: printf("%s u64 %llx\n", what, (unsigned long long)e->data.u64); break;
	case 3: printf("%s ptr %d\n", what, e->data.ptr == e); break;
	}
}

static int mark;

int main(void) {
	for (int i = 0; i < 8; i++) {
		struct epoll_event local;
		memset(&local, 0, sizeof local);
		local.events = EPOLLIN;
		local.data.fd = 7;
		if (i % 4 == 0)
			printf("local fd %d\n", local.data.fd);
		struct epoll_event seen;
		memset(&seen, 0, sizeof seen);
		set(&seen, i);
		show("pointed", &seen, i);
		struct epoll_event *h = malloc(sizeof *h);
		memset(h, 0, sizeof *h);
		set(h, i);
		show("malloc", h, i);
		memset(&gev, 0, sizeof gev);
		set(&gev, i);
		show("global", &gev, i);
		free(h);
	}

	union sigval v;
	v.sival_int = 42;
	printf("sigval int %d\n", v.sival_int);
	v.sival_ptr = &mark;
	printf("sigval ptr %d\n", v.sival_ptr == &mark);
	union sigval *hv = malloc(sizeof *hv);
	hv->sival_ptr = &mark;
	printf("malloc sigval ptr %d\n", hv->sival_ptr == &mark);
	hv->sival_int = 9;
	printf("malloc sigval int %d\n", hv->sival_int);
	gsv.sival_int = 5;
	printf("global sigval int %d\n", gsv.sival_int);
	gsv.sival_ptr = hv;
	printf("global sigval ptr %d\n", gsv.sival_ptr == hv);

	// Through the library: what epoll_ctl is given comes back.
	int ep = epoll_create1(0);
	int fds[2];
	if (ep < 0 || pipe(fds) != 0) {
		printf("no epoll\n");
		return 1;
	}
	struct epoll_event ev;
	memset(&ev, 0, sizeof ev);
	ev.events = EPOLLOUT;
	ev.data.u64 = 0x1122334455667788ull;
	epoll_ctl(ep, EPOLL_CTL_ADD, fds[1], &ev);
	struct epoll_event out[2];
	memset(out, 0, sizeof out);
	int n = epoll_wait(ep, out, 2, 1000);
	printf("epoll u64 %d %llx\n", n, (unsigned long long)out[0].data.u64);
	ev.data.ptr = &mark;
	epoll_ctl(ep, EPOLL_CTL_MOD, fds[1], &ev);
	n = epoll_wait(ep, out, 2, 1000);
	printf("epoll ptr %d %d\n", n, out[0].data.ptr == &mark);
	return 0;
}
