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

#include <poll.h>
int main(void){ struct pollfd *p = malloc(sizeof *p); p->fd = 0; p->events = 0; vn = 4096; poll(p, vn, 0); unsafe("poll of 4096 pollfds where there is one"); }
