#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <stddef.h>
static volatile int vn; /* defeats constant folding */
static void *volatile vg;
static void *launder(void *p) { vg = p; return vg; }
static void unsafe(const char *what) { printf("UNSAFE: %s\n", what); exit(0); }
static void ok(void) { printf("OK\n"); exit(0); }
char victim[16] = "safe";
#include <sys/epoll.h>
int main(void){ struct epoll_event ev; ev.data.u64 = (uint64_t)(uintptr_t)victim; struct epoll_event *e = launder(&ev); char *p = e->data.ptr; *p = 88; if (victim[0] == 88) unsafe("the integer of an epoll_data_t was used as its pointer"); ok(); }
